package notifier

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/store"
)

// Dispatcher resolves which notifiers + escalation steps apply to a monitor
// and sends alerts accordingly.
type Dispatcher struct {
	store   *store.Store
	client  *http.Client
	mu      sync.Mutex
	watched map[string]*watchState // monitorID -> tracking down duration for escalation
	stopCh  chan struct{}
}

type watchState struct {
	monitorID   string
	downSince   time.Time
	alertedStep map[int]bool // escalation step index -> already fired
}

func NewDispatcher(st *store.Store) *Dispatcher {
	d := &Dispatcher{
		store: st,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		watched: map[string]*watchState{},
		stopCh:  make(chan struct{}),
	}
	return d
}

func (d *Dispatcher) Stop() { close(d.stopCh) }

// OnStatusChange is called by the monitor engine whenever a check flips state.
func (d *Dispatcher) OnStatusChange(ctx context.Context, m *models.Monitor, oldStatus, newStatus models.MonitorStatus, message string, downSince time.Time) {
	switch newStatus {
	case models.StatusDown, models.StatusDegraded:
		d.mu.Lock()
		ws, ok := d.watched[m.ID]
		if !ok || oldStatus != models.StatusDown && oldStatus != models.StatusDegraded {
			ws = &watchState{monitorID: m.ID, downSince: downSince, alertedStep: map[int]bool{}}
			d.watched[m.ID] = ws
		}
		d.mu.Unlock()
		// immediate alert on the base notifier(s) attached to the monitor
		d.sendImmediate(ctx, m, newStatus, message)
		// escalation steps are handled by the ticker loop below
	case models.StatusUp:
		d.mu.Lock()
		delete(d.watched, m.ID)
		d.mu.Unlock()
		d.sendImmediate(ctx, m, newStatus, message)
	}
}

// StartEscalationLoop periodically checks how long each currently-down monitor
// has been down and fires escalation steps once their threshold is passed.
func (d *Dispatcher) StartEscalationLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.runEscalations(ctx)
		}
	}
}

func (d *Dispatcher) runEscalations(ctx context.Context) {
	d.mu.Lock()
	watches := make([]*watchState, 0, len(d.watched))
	for _, w := range d.watched {
		watches = append(watches, w)
	}
	d.mu.Unlock()

	for _, w := range watches {
		m, err := d.store.GetMonitor(ctx, w.monitorID)
		if err != nil || m.EscalationID == nil {
			continue
		}
		policy, err := d.store.GetEscalation(ctx, *m.EscalationID)
		if err != nil || policy == nil {
			continue
		}
		downFor := time.Since(w.downSince)
		for i, step := range policy.Steps {
			if downFor >= time.Duration(step.AfterDownForSec)*time.Second && !w.alertedStep[i] {
				w.alertedStep[i] = true
				n, err := d.store.GetNotification(ctx, step.NotifierID)
				if err != nil {
					log.Printf("[escalation] notifier %s not found: %v", step.NotifierID, err)
					continue
				}
				msg := fmt.Sprintf("[ESCALATION step %d - down for %s] %s on %s: %s",
					i+1, downFor.Round(time.Second), strings.ToUpper(string(m.Status)), m.Name, w.reason())
				go func(n *models.Notification, msg string) {
					if err := d.Send(ctx, n, models.StatusDown, msg); err != nil {
						log.Printf("[escalation] send failed: %v", err)
					}
				}(n, msg)
			}
		}
	}
}

func (w *watchState) reason() string { return "service unavailable" }

func (d *Dispatcher) sendImmediate(ctx context.Context, m *models.Monitor, status models.MonitorStatus, message string) {
	for _, nid := range m.NotificationIDs {
		n, err := d.store.GetNotification(ctx, nid)
		if err != nil {
			log.Printf("[notifier] notification %s not found: %v", nid, err)
			continue
		}
		msg := fmt.Sprintf("%s on %s: %s", strings.ToUpper(string(status)), m.Name, message)
		go func(n *models.Notification, msg string) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := d.Send(ctx, n, status, msg); err != nil {
				log.Printf("[notifier] send to %s (%s) failed: %v", n.Name, n.Type, err)
			}
		}(n, msg)
	}
}

// Send dispatches a message through a single notification config.
func (d *Dispatcher) Send(ctx context.Context, n *models.Notification, status models.MonitorStatus, message string) error {
	switch n.Type {
	case "telegram":
		return d.sendTelegram(ctx, n.Config, message)
	case "discord":
		return d.sendDiscord(ctx, n.Config, message, status)
	case "slack":
		return d.sendSlack(ctx, n.Config, message)
	case "webhook":
		return d.sendWebhook(ctx, n.Config, message, string(status))
	case "email":
		return d.sendEmail(ctx, n.Config, message)
	default:
		return fmt.Errorf("unknown notifier type %q", n.Type)
	}
}

// ---------- Telegram ----------

type telegramConfig struct {
	BotToken string `json:"botToken"`
	ChatID   string `json:"chatId"`
}

func (d *Dispatcher) sendTelegram(ctx context.Context, raw json.RawMessage, message string) error {
	var cfg telegramConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.BotToken == "" || cfg.ChatID == "" {
		return fmt.Errorf("telegram: missing botToken/chatId")
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	payload, _ := json.Marshal(map[string]string{"chat_id": cfg.ChatID, "text": message})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram api error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ---------- Discord (webhook based) ----------

type discordConfig struct {
	WebhookURL string `json:"webhookUrl"`
}

func (d *Dispatcher) sendDiscord(ctx context.Context, raw json.RawMessage, message string, status models.MonitorStatus) error {
	var cfg discordConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("discord: missing webhookUrl")
	}
	color := 0x2ecc71 // green
	switch status {
	case models.StatusDown:
		color = 0xe74c3c
	case models.StatusDegraded:
		color = 0xf1c40f
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"username": "Uptime Monitor",
		"embeds": []map[string]interface{}{{
			"description": message,
			"color":       color,
		}},
	})
	return d.postJSON(ctx, cfg.WebhookURL, payload)
}

// ---------- Slack (webhook based) ----------

type slackConfig struct {
	WebhookURL string `json:"webhookUrl"`
}

func (d *Dispatcher) sendSlack(ctx context.Context, raw json.RawMessage, message string) error {
	var cfg slackConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack: missing webhookUrl")
	}
	payload, _ := json.Marshal(map[string]string{"text": message})
	return d.postJSON(ctx, cfg.WebhookURL, payload)
}

// ---------- Generic webhook ----------

type webhookConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

func (d *Dispatcher) sendWebhook(ctx context.Context, raw json.RawMessage, message, status string) error {
	var cfg webhookConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook: missing url")
	}
	method := strings.ToUpper(cfg.Method)
	if method == "" {
		method = http.MethodPost
	}
	payload, _ := json.Marshal(map[string]string{"status": status, "message": message})
	req, err := http.NewRequestWithContext(ctx, method, cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ---------- Email (SMTP) ----------

type emailConfig struct {
	SMTPHost string   `json:"smtpHost"`
	SMTPPort int      `json:"smtpPort"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	UseTLS   bool     `json:"useTls"`
}

func (d *Dispatcher) sendEmail(ctx context.Context, raw json.RawMessage, message string) error {
	var cfg emailConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.SMTPHost == "" || len(cfg.To) == 0 {
		return fmt.Errorf("email: missing smtpHost/to")
	}
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	}
	msg := []byte(fmt.Sprintf("Subject: [Uptime Monitor Alert]\r\n\r\n%s\r\n", message))

	if cfg.UseTLS {
		return d.sendEmailTLS(addr, cfg, msg)
	}
	return smtp.SendMail(addr, auth, cfg.From, cfg.To, msg)
}

func (d *Dispatcher) sendEmailTLS(addr string, cfg emailConfig, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.SMTPHost,
		InsecureSkipVerify: false,
	})
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Quit()

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	for _, to := range cfg.To {
		if err := client.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(msg)
	if err != nil {
		return err
	}
	return w.Close()
}

func (d *Dispatcher) postJSON(ctx context.Context, url string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("post %s error %d: %s", url, resp.StatusCode, string(body))
	}
	return nil
}

// TestSend is used by the UI "Test notification" button.
func (d *Dispatcher) TestSend(ctx context.Context, n *models.Notification) error {
	return d.Send(ctx, n, models.StatusUp, "Test notification from Uptime Monitor")
}
