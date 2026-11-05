package notifier

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/store"
	"github.com/uptime/monitoring/internal/sysdns"
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
		d.sendImmediate(ctx, m, newStatus, message, 0)
		// escalation steps are handled by the ticker loop below
	case models.StatusUp:
		d.mu.Lock()
		var downFor time.Duration
		if ws, ok := d.watched[m.ID]; ok {
			downFor = time.Since(ws.downSince)
		}
		delete(d.watched, m.ID)
		d.mu.Unlock()
		d.sendImmediate(ctx, m, newStatus, message, downFor)
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
				v := buildVars(ctx, m, models.StatusDown, w.reason(), downFor, i+1)
				subject, body := d.compose(ctx, n, kindEscalation, v)
				go func(n *models.Notification, subject, body string) {
					if err := d.sendTyped(ctx, n, models.StatusDown, subject, body); err != nil {
						log.Printf("[escalation] send failed: %v", err)
					}
				}(n, subject, body)
			}
		}
	}
}

func (w *watchState) reason() string { return "service unavailable" }

func (d *Dispatcher) sendImmediate(ctx context.Context, m *models.Monitor, status models.MonitorStatus, message string, downFor time.Duration) {
	kind := kindDown
	switch status {
	case models.StatusUp:
		kind = kindUp
	case models.StatusDegraded:
		kind = kindDegraded
	}
	ids := m.NotificationIDs
	// Global fallback: when a monitor has no channel explicitly attached,
	// alert through every configured notification channel. This mirrors the
	// Uptime-Kuma mental model where adding a notification "just works" for
	// all monitors, so admins don't have to link each of many monitors.
	if len(ids) == 0 {
		if all, err := d.store.ListNotifications(ctx); err == nil {
			for _, n := range all {
				ids = append(ids, n.ID)
			}
		}
	}
	for _, nid := range ids {
		n, err := d.store.GetNotification(ctx, nid)
		if err != nil {
			log.Printf("[notifier] notification %s not found: %v", nid, err)
			continue
		}
		v := buildVars(ctx, m, status, message, downFor, 0)
		subject, body := d.compose(ctx, n, kind, v)
		go func(n *models.Notification, subject, body string) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := d.sendTyped(ctx, n, status, subject, body); err != nil {
				log.Printf("[notifier] send to %s (%s) failed: %v", n.Name, n.Type, err)
			}
		}(n, subject, body)
	}
}

// sendTyped dispatches a rendered subject+body through a single notification
// config. subject is only used by email; chat/webhook senders use body alone.
func (d *Dispatcher) sendTyped(ctx context.Context, n *models.Notification, status models.MonitorStatus, subject, message string) error {
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
		return d.sendEmail(ctx, n.Config, subject, message)
	default:
		return fmt.Errorf("unknown notifier type %q", n.Type)
	}
}

// SendToChannels delivers a pre-rendered subject+body (no templating) to every
// notification channel in ids. Sends run in background goroutines with a
// per-channel timeout; failures are logged, mirroring sendImmediate. Used by
// the periodic report scheduler for the legacy shared-channel selection.
func (d *Dispatcher) SendToChannels(ctx context.Context, ids []string, subject, body string) {
	ns := make([]*models.Notification, 0, len(ids))
	for _, nid := range ids {
		n, err := d.store.GetNotification(ctx, nid)
		if err != nil {
			log.Printf("[report] notification %s not found: %v", nid, err)
			continue
		}
		ns = append(ns, n)
	}
	d.SendToNotifications(ctx, ns, subject, body)
}

// SendToNotifications delivers a pre-rendered subject+body to the given
// notification configs (used for report channels defined in Settings, which
// are not stored as Notification records). Sends run in background goroutines
// with a per-channel timeout; failures are logged.
func (d *Dispatcher) SendToNotifications(ctx context.Context, ns []*models.Notification, subject, body string) {
	for _, n := range ns {
		n := n
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := d.sendTyped(ctx, n, models.StatusUp, subject, body); err != nil {
				log.Printf("[report] send to %s (%s) failed: %v", n.Name, n.Type, err)
			}
		}()
	}
}

// errNoFileSupport signals that a channel type cannot attach a file; callers
// fall back to delivering the plain-text summary instead.
var errNoFileSupport = errors.New("notifier: channel does not support file attachments")

// SendFileToChannels delivers a report file (with the summary text as the
// caption/comment) to every channel in ids. Channels that support file uploads
// (Slack bot token, Telegram) receive the actual Excel/CSV file; others fall
// back to the rendered text body. Sends run in background goroutines with a
// per-channel timeout; failures are logged, mirroring SendToChannels.
func (d *Dispatcher) SendFileToChannels(ctx context.Context, ids []string, subject, body, filename string, data []byte) {
	ns := make([]*models.Notification, 0, len(ids))
	for _, nid := range ids {
		n, err := d.store.GetNotification(ctx, nid)
		if err != nil {
			log.Printf("[report] notification %s not found: %v", nid, err)
			continue
		}
		ns = append(ns, n)
	}
	d.SendFileToNotifications(ctx, ns, subject, body, filename, data)
}

// SendFileToNotifications delivers a report file to the given notification
// configs (used for report channels defined in Settings). Channels that
// support uploads receive the file; others fall back to the text body.
func (d *Dispatcher) SendFileToNotifications(ctx context.Context, ns []*models.Notification, subject, body, filename string, data []byte) {
	for _, n := range ns {
		n := n
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := d.sendFile(ctx, n, subject, body, filename, data); err != nil {
				if errors.Is(err, errNoFileSupport) {
					if err := d.sendTyped(ctx, n, models.StatusUp, subject, body); err != nil {
						log.Printf("[report] text fallback to %s (%s) failed: %v", n.Name, n.Type, err)
					}
					return
				}
				log.Printf("[report] file send to %s (%s) failed: %v", n.Name, n.Type, err)
			}
		}()
	}
}

// SendNotificationsSync delivers a subject+body to the given notification
// configs and blocks until every send finishes, returning an aggregated error
// describing any failures. Used by the manual "send report now" path so the
// caller (and UI) learns whether delivery actually succeeded, unlike the
// fire-and-forget SendToNotifications used by the periodic scheduler.
func (d *Dispatcher) SendNotificationsSync(ctx context.Context, ns []*models.Notification, subject, body string) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fails []string
	for _, n := range ns {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := d.sendTyped(sctx, n, models.StatusUp, subject, body); err != nil {
				mu.Lock()
				fails = append(fails, fmt.Sprintf("%s (%s): %v", n.Name, n.Type, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(fails) > 0 {
		return errors.New(strings.Join(fails, "; "))
	}
	return nil
}

// SendFileToNotificationsSync delivers a report file to the given configs and
// blocks until done, falling back to the text body for channels that cannot
// attach files. Returns an aggregated error of any failures.
func (d *Dispatcher) SendFileToNotificationsSync(ctx context.Context, ns []*models.Notification, subject, body, filename string, data []byte) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fails []string
	for _, n := range ns {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := d.sendFile(sctx, n, subject, body, filename, data); err != nil {
				if errors.Is(err, errNoFileSupport) {
					if err := d.sendTyped(sctx, n, models.StatusUp, subject, body); err != nil {
						mu.Lock()
						fails = append(fails, fmt.Sprintf("%s (%s): %v", n.Name, n.Type, err))
						mu.Unlock()
					}
					return
				}
				mu.Lock()
				fails = append(fails, fmt.Sprintf("%s (%s): %v", n.Name, n.Type, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(fails) > 0 {
		return errors.New(strings.Join(fails, "; "))
	}
	return nil
}

// sendFile dispatches a report file through a single notification config,
// choosing the channel-specific upload path. Returns errNoFileSupport for
// channel types that cannot attach files.
func (d *Dispatcher) sendFile(ctx context.Context, n *models.Notification, subject, comment, filename string, data []byte) error {
	switch n.Type {
	case "slack":
		return d.sendSlackFile(ctx, n.Config, comment, filename, data)
	case "telegram":
		return d.sendTelegramFile(ctx, n.Config, comment, filename, data)
	case "discord":
		return d.sendDiscordFile(ctx, n.Config, subject, comment, filename, data)
	case "email":
		return d.sendEmailFile(ctx, n.Config, subject, comment, filename, data)
	default:
		return errNoFileSupport
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

// sendTelegramFile uploads a report file to the chat via sendDocument
// (multipart/form-data) with the summary text as the caption.
func (d *Dispatcher) sendTelegramFile(ctx context.Context, raw json.RawMessage, caption, filename string, data []byte) error {
	var cfg telegramConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.BotToken == "" || cfg.ChatID == "" {
		return errNoFileSupport
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", cfg.ChatID)
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", cfg.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode/100 != 2 || strings.Contains(string(body), `"ok":false`) {
		return fmt.Errorf("telegram sendDocument error %d: %s", resp.StatusCode, string(body))
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

// sendDiscordFile posts the report file to the Discord webhook as a
// multipart/form-data upload (payload_json + files[0]) with the summary text
// as the message content.
func (d *Dispatcher) sendDiscordFile(ctx context.Context, raw json.RawMessage, subject, content, filename string, data []byte) error {
	var cfg discordConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.WebhookURL == "" {
		return errNoFileSupport
	}
	if content == "" {
		content = subject
	}
	if len(content) > 2000 {
		content = content[:2000]
	}
	payloadJSON, _ := json.Marshal(map[string]interface{}{"username": "Uptime Monitor", "content": content})
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("payload_json", string(payloadJSON))
	part, err := w.CreateFormFile("files[0]", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("discord webhook file error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ---------- Slack (webhook or bot token) ----------

type slackConfig struct {
	WebhookURL string `json:"webhookUrl"`
	BotToken   string `json:"botToken"`
	Channel    string `json:"channel"`
}

// useBot reports whether the channel is configured with a bot token + target
// channel, which is required for file uploads (incoming webhooks cannot attach
// files). Otherwise the legacy incoming-webhook path is used.
func (c slackConfig) useBot() bool {
	return strings.TrimSpace(c.BotToken) != "" && strings.TrimSpace(c.Channel) != ""
}

func (d *Dispatcher) sendSlack(ctx context.Context, raw json.RawMessage, message string) error {
	var cfg slackConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if cfg.useBot() {
		form := url.Values{"channel": {cfg.Channel}, "text": {message}}
		return d.slackAPI(ctx, cfg.BotToken, "chat.postMessage", form)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack: missing webhookUrl or botToken+channel")
	}
	payload, _ := json.Marshal(map[string]string{"text": message})
	return d.postJSON(ctx, cfg.WebhookURL, payload)
}

// slackAPI posts a form-encoded request to a Slack Web API method using the
// bot token and reports the API-level error when "ok" is false.
func (d *Dispatcher) slackAPI(ctx context.Context, token, method string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack %s http %d: %s", method, resp.StatusCode, string(body))
	}
	var out struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("slack %s decode: %w", method, err)
	}
	if !out.Ok {
		return fmt.Errorf("slack %s api error: %s", method, out.Error)
	}
	return nil
}

// sendSlackFile uploads a file to a Slack channel via the Web API (requires a
// bot token with the files:write scope). It uses the external upload flow:
// files.getUploadURLExternal -> PUT bytes -> files.completeUploadExternal.
func (d *Dispatcher) sendSlackFile(ctx context.Context, raw json.RawMessage, comment, filename string, data []byte) error {
	var cfg slackConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if !cfg.useBot() {
		return errNoFileSupport
	}

	// 1. request an upload URL for the file.
	upForm := url.Values{"filename": {filename}, "length": {strconv.Itoa(len(data))}}
	uploadURL, fileID, err := d.slackGetUploadURL(ctx, cfg.BotToken, upForm)
	if err != nil {
		return err
	}

	// 2. PUT the raw bytes to the (pre-signed) upload URL.
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putResp, err := d.client.Do(putReq)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(putResp.Body, 1024))
	putResp.Body.Close()
	if putResp.StatusCode/100 != 2 {
		return fmt.Errorf("slack upload http %d", putResp.StatusCode)
	}

	// 3. finalize the upload and post it to the channel with the summary text.
	filesJSON, _ := json.Marshal([]map[string]string{{"id": fileID, "title": filename}})
	form := url.Values{
		"files":           {string(filesJSON)},
		"channel":         {cfg.Channel},
		"initial_comment": {comment},
	}
	return d.slackAPI(ctx, cfg.BotToken, "files.completeUploadExternal", form)
}

// SendSlackFile uploads a report file to an arbitrary Slack channel using the
// given bot token — used for the Settings-configured direct Slack delivery
// (no notification record involved).
func (d *Dispatcher) SendSlackFile(ctx context.Context, token, channel, comment, filename string, data []byte) error {
	raw, _ := json.Marshal(slackConfig{BotToken: token, Channel: channel})
	return d.sendSlackFile(ctx, raw, comment, filename, data)
}

// SendSlackText posts a plain message to a Slack channel via bot token.
func (d *Dispatcher) SendSlackText(ctx context.Context, token, channel, message string) error {
	form := url.Values{"channel": {channel}, "text": {message}}
	return d.slackAPI(ctx, token, "chat.postMessage", form)
}

func (d *Dispatcher) slackGetUploadURL(ctx context.Context, token string, form url.Values) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/files.getUploadURLExternal", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := d.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		Ok        bool   `json:"ok"`
		Error     string `json:"error"`
		FileID    string `json:"file_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("slack getUploadURL decode: %w", err)
	}
	if !out.Ok || out.UploadURL == "" || out.FileID == "" {
		return "", "", fmt.Errorf("slack files.getUploadURLExternal error: %s", out.Error)
	}
	return out.UploadURL, out.FileID, nil
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

// flexInt accepts a JSON number or numeric string, so UI fields that send the
// SMTP port as a string (e.g. the port dropdown) unmarshal cleanly.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("invalid integer %q", s)
	}
	*f = flexInt(n)
	return nil
}

// stringOrSlice accepts a single string (split on commas) or a JSON array, so
// the "to" recipients field works whether it is sent as text or a list.
type stringOrSlice []string

func (s *stringOrSlice) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		out := []string{}
		for _, p := range strings.Split(single, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		*s = out
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

type emailConfig struct {
	SMTPHost string        `json:"smtpHost"`
	SMTPPort flexInt       `json:"smtpPort"`
	Username string        `json:"username"`
	Password string        `json:"password"`
	From     string        `json:"from"`
	To       stringOrSlice `json:"to"`
	UseTLS   bool          `json:"useTls"`
}

// buildEmailMsg assembles a full RFC822 message. A bare Subject-only message is
// frequently accepted then silently dropped or spam-foldered by MTAs, so we
// include Date, From, To, an encoded Subject, Message-ID and plain-text MIME
// headers.
func buildEmailMsg(cfg emailConfig, subject, message string) []byte {
	var b strings.Builder
	writeEmailHeaders(&b, cfg, subject)
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(message)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// buildEmailFileMsg assembles a multipart/mixed message: a plain-text summary
// part plus a base64 file attachment, so the report Excel/CSV rides along with
// the email.
func buildEmailFileMsg(cfg emailConfig, subject, body, filename string, data []byte) []byte {
	boundary := fmt.Sprintf("uptime-%d", time.Now().UnixNano())
	var b strings.Builder
	writeEmailHeaders(&b, cfg, subject)
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: application/octet-stream; name=\"" + filename + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end] + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// writeEmailHeaders emits the shared RFC822 headers used by every email variant.
func writeEmailHeaders(b *strings.Builder, cfg emailConfig, subject string) {
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	if cfg.From != "" {
		b.WriteString("From: " + cfg.From + "\r\n")
	}
	b.WriteString("To: " + strings.Join(cfg.To, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("Message-ID: <" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@uptime-monitor>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
}

func (d *Dispatcher) sendEmail(ctx context.Context, raw json.RawMessage, subject, message string) error {
	cfg, err := parseEmailConfig(raw)
	if err != nil {
		return err
	}
	if subject == "" {
		subject = "[Uptime Monitor Alert]"
	}
	return d.deliverEmail(ctx, cfg, buildEmailMsg(cfg, subject, message))
}

// sendEmailFile delivers the report as a multipart email with the file attached.
func (d *Dispatcher) sendEmailFile(ctx context.Context, raw json.RawMessage, subject, body, filename string, data []byte) error {
	cfg, err := parseEmailConfig(raw)
	if err != nil {
		return err
	}
	if subject == "" {
		subject = "[Uptime Monitor Alert]"
	}
	return d.deliverEmail(ctx, cfg, buildEmailFileMsg(cfg, subject, body, filename, data))
}

// parseEmailConfig decodes and normalises an email notification config.
func parseEmailConfig(raw json.RawMessage) (emailConfig, error) {
	var cfg emailConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.SMTPHost == "" || len(cfg.To) == 0 {
		return cfg, fmt.Errorf("email: missing smtpHost/to")
	}
	if cfg.SMTPPort == 0 {
		cfg.SMTPPort = 587
	}
	return cfg, nil
}

// deliverEmail connects to the configured SMTP server (choosing the security
// mode from the port) and transmits the prebuilt message bytes.
func (d *Dispatcher) deliverEmail(ctx context.Context, cfg emailConfig, msg []byte) error {
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, int(cfg.SMTPPort))

	// The selected port decides the security mode:
	//   465 -> implicit TLS, 587 -> STARTTLS (required), 25 -> plaintext only.
	// The legacy useTls flag still forces implicit TLS for older alert channels.
	port := int(cfg.SMTPPort)
	implicit := cfg.UseTLS || port == 465
	requireStartTLS := !implicit && port == 587
	plainOnly := !implicit && port == 25

	// Dial with a bounded timeout so an unreachable host (e.g. a private IP the
	// server cannot route to) fails fast instead of hanging the request.
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer conn.Close()
	// Overall read/write deadline for the whole SMTP exchange.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.SMTPHost}
	var client *smtp.Client
	if implicit {
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			return fmt.Errorf("smtp tls handshake %s: %w", addr, err)
		}
		client, err = smtp.NewClient(tlsConn, cfg.SMTPHost)
	} else {
		client, err = smtp.NewClient(conn, cfg.SMTPHost)
	}
	if err != nil {
		return err
	}
	defer client.Quit()

	// Upgrade the connection according to the port choice. Port 25 stays in
	// plaintext even when the server advertises STARTTLS.
	if !implicit && !plainOnly {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			if requireStartTLS {
				return fmt.Errorf("smtp: server does not support STARTTLS on port 587")
			}
		} else if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

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
	if _, err := w.Write(msg); err != nil {
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

// TestSend is used by the UI "Test notification" button. It renders through the
// notifier's resolved template so the test reflects the real formatting.
func (d *Dispatcher) TestSend(ctx context.Context, n *models.Notification) error {
	v := templateVars{
		Monitor:  n.Name,
		Status:   "UP",
		Message:  "This is a test notification from Uptime Monitor.",
		Target:   "-",
		URL:      "-",
		Time:     time.Now().UTC().Format(time.RFC3339),
		Duration: "-",
		Step:     "-",
	}
	subject, body := d.compose(ctx, n, kindUp, v)
	return d.sendTyped(ctx, n, models.StatusUp, subject, body)
}

// ---------- template rendering ----------

// alertKind labels the event that triggered a notification, selecting which
// body of a template to render.
type alertKind int

const (
	kindDown alertKind = iota
	kindUp
	kindDegraded
	kindEscalation
)

// channelForType maps a notifier type to its template channel.
func channelForType(t string) string {
	if t == "email" {
		return "email"
	}
	return "chat"
}

// templateVars holds the {placeholder} values substituted into a template.
type templateVars struct {
	Monitor  string
	Status   string
	Message  string
	Target   string
	URL      string
	Time     string
	Duration string
	Step     string
	IPPublic string
	IPLocal  string
}

// apply replaces the known {placeholders} in s with their values.
func (v templateVars) apply(s string) string {
	return strings.NewReplacer(
		"{monitor}", v.Monitor,
		"{status}", v.Status,
		"{message}", v.Message,
		"{reason}", v.Message,
		"{target}", v.Target,
		"{url}", v.URL,
		"{time}", v.Time,
		"{duration}", v.Duration,
		"{step}", v.Step,
		"{ipPublic}", v.IPPublic,
		"{ipLocal}", v.IPLocal,
		"{serverName}", v.Monitor,
	).Replace(s)
}

// alertHost extracts the hostname whose public IP should be shown, stripping
// any scheme (for URL monitors) and port.
func alertHost(m *models.Monitor) string {
	h := ""
	if strings.TrimSpace(m.URL) != "" {
		if u, err := url.Parse(m.URL); err == nil {
			h = u.Hostname()
		}
	}
	if h == "" {
		h = strings.TrimSpace(m.Host)
	}
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return h
}

// resolvePublicIP returns the IPv4 address a monitor's host resolves to. Bare
// IPs are returned as-is; domain hosts get a short best-effort lookup. Returns
// "-" when the address cannot be determined so templates read cleanly.
func resolvePublicIP(ctx context.Context, m *models.Monitor) string {
	h := alertHost(m)
	if h == "" {
		return "-"
	}
	if ip := net.ParseIP(h); ip != nil {
		return h
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := sysdns.LookupHost(c, h)
	if err != nil || len(addrs) == 0 {
		return "-"
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a
		}
	}
	return addrs[0]
}

// buildVars assembles the placeholder values from a monitor + event. downFor
// and step are only meaningful for recovery/escalation messages; when zero the
// corresponding placeholders fall back to "-" so templates read cleanly.
func buildVars(ctx context.Context, m *models.Monitor, status models.MonitorStatus, message string, downFor time.Duration, step int) templateVars {
	target := m.URL
	if target == "" {
		target = m.Host
	}
	v := templateVars{
		Monitor:  m.Name,
		Status:   strings.ToUpper(string(status)),
		Message:  message,
		Target:   target,
		URL:      m.URL,
		Time:     time.Now().UTC().Format(time.RFC3339),
		Duration: "-",
		Step:     "-",
		IPPublic: resolvePublicIP(ctx, m),
		IPLocal:  orDashLocal(m.LocalIp),
	}
	if downFor > 0 {
		v.Duration = downFor.Round(time.Second).String()
	}
	if step > 0 {
		v.Step = strconv.Itoa(step)
	}
	return v
}

// orDashLocal keeps an optional local-IP note readable when unset.
func orDashLocal(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// resolveTemplate picks the template for a notifier: its assigned template if
// set, otherwise the default for the notifier's channel. Returns nil when no
// template is available (callers then fall back to the legacy format).
func (d *Dispatcher) resolveTemplate(ctx context.Context, n *models.Notification) *models.AlertTemplate {
	ts, err := d.store.GetAlertTemplates(ctx)
	if err != nil {
		return nil
	}
	ch := channelForType(n.Type)
	var def *models.AlertTemplate
	for i := range ts {
		if n.TemplateID != "" && ts[i].ID == n.TemplateID {
			return &ts[i]
		}
		if ts[i].IsDefault && ts[i].Channel == ch {
			def = &ts[i]
		}
	}
	return def
}

// bodyFor selects the template body matching an event kind.
func bodyFor(t *models.AlertTemplate, kind alertKind) string {
	switch kind {
	case kindUp:
		return t.BodyUp
	case kindDegraded:
		return t.BodyDegraded
	case kindEscalation:
		return t.BodyEscalation
	default:
		return t.BodyDown
	}
}

// compose renders the subject+body for a notification, falling back to the
// historical hardcoded format when no template resolves.
func (d *Dispatcher) compose(ctx context.Context, n *models.Notification, kind alertKind, v templateVars) (subject, body string) {
	t := d.resolveTemplate(ctx, n)
	if t == nil {
		return "[Uptime Monitor Alert]", fmt.Sprintf("%s on %s: %s", v.Status, v.Monitor, v.Message)
	}
	body = v.apply(bodyFor(t, kind))
	subject = v.apply(t.Subject)
	if strings.TrimSpace(subject) == "" {
		subject = fmt.Sprintf("[Uptime Monitor] %s", v.Monitor)
	}
	return subject, body
}
