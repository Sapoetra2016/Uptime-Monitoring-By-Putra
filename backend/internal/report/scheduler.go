package report

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/store"
)

// ErrNoChannels is returned by SendNow when the report has no notification
// channels selected yet.
var ErrNoChannels = errors.New("no notification channels selected for reports")

// Sender delivers a rendered report through the configured notification
// channels. Implemented by *notifier.Dispatcher; kept as an interface here so
// this package does not depend on the notifier internals.
type Sender interface {
	SendToChannels(ctx context.Context, ids []string, subject, body string)
	SendFileToChannels(ctx context.Context, ids []string, subject, body, filename string, data []byte)
	SendToNotifications(ctx context.Context, ns []*models.Notification, subject, body string)
	SendFileToNotifications(ctx context.Context, ns []*models.Notification, subject, body, filename string, data []byte)
	SendNotificationsSync(ctx context.Context, ns []*models.Notification, subject, body string) error
	SendFileToNotificationsSync(ctx context.Context, ns []*models.Notification, subject, body, filename string, data []byte) error
	SendSlackFile(ctx context.Context, token, channel, comment, filename string, data []byte) error
	SendSlackText(ctx context.Context, token, channel, message string) error
}

// Scheduler wakes up every 30s and, when the configured send time for the
// current period has just passed, builds the report, writes a CSV file to the
// separate report directory, and (when channels are selected) dispatches the
// summary. LastSentAt is persisted in the settings so restarts cannot cause a
// duplicate send for the same period.
type Scheduler struct {
	store  *store.Store
	sender Sender
	dir    string // report output directory, kept outside the app tree
}

func NewScheduler(st *store.Store, sender Sender, dir string) *Scheduler {
	return &Scheduler{store: st, sender: sender, dir: dir}
}

// Start blocks until ctx is cancelled. Intended to run in a goroutine.
func (sc *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sc.tick(ctx, time.Now())
		}
	}
}

// tick evaluates whether a periodic report is due at time now.
func (sc *Scheduler) tick(ctx context.Context, now time.Time) {
	set, err := sc.store.GetReportSettings(ctx)
	if err != nil {
		log.Printf("[report] settings error: %v", err)
		return
	}
	if !set.Enabled || !hasDestination(set) {
		return
	}
	sched, since := dueAt(set, now)
	if sched.IsZero() {
		return
	}
	// at-most-once per period: LastSentAt is stamped with the scheduled time.
	if !set.LastSentAt.Before(sched) {
		return
	}
	res, err := build(ctx, sc.store, since, now, set.SubjectTemplate, set.BodyTemplate)
	if err != nil {
		log.Printf("[report] build failed: %v", err)
		return
	}
	if path, werr := sc.writeCSV(set.Frequency, sched, res.CSV); werr != nil {
		log.Printf("[report] write file failed: %v", werr)
	} else {
		log.Printf("[report] saved %s", path)
	}
	sc.dispatch(ctx, set, res, sched)
	set.LastSentAt = sched
	if err := sc.store.SaveReportSettings(ctx, set); err != nil {
		log.Printf("[report] persist lastSentAt failed: %v", err)
	}
	log.Printf("[report] periodic report sent (%s window, %d channels)", set.Frequency, len(set.Channels))
}

// SendNow builds and dispatches a report immediately (used by the "send test
// now" button). It writes the report file in the configured format and sends
// synchronously so delivery failures (or a missing destination) are returned to
// the caller. It does not touch LastSentAt, so the periodic schedule is
// unaffected.
func (sc *Scheduler) SendNow(ctx context.Context) (*Result, error) {
	set, err := sc.store.GetReportSettings(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	res, err := build(ctx, sc.store, now.Add(-24*time.Hour), now, set.SubjectTemplate, set.BodyTemplate)
	if err != nil {
		return nil, err
	}
	// Save the report file in the chosen format (xlsx or csv) regardless of
	// delivery, so the file always exists in the report directory.
	name, data := attachFile(set, now, res)
	if path, werr := sc.writeFile(name, data); werr != nil {
		log.Printf("[report] write file failed: %v", werr)
	} else {
		log.Printf("[report] saved %s", path)
	}
	if derr := sc.dispatchSync(ctx, set, res, now, name, data); derr != nil {
		return res, derr
	}
	return res, nil
}

// dispatchSync delivers the report to the report's own configured channels
// (Settings → Laporan) and returns an error when there is no destination or any
// send fails. Report delivery is fully independent of the alert notification
// channels. Unlike the fire-and-forget dispatch used by the periodic scheduler,
// this blocks so the manual "send now" caller learns the real outcome.
func (sc *Scheduler) dispatchSync(ctx context.Context, set models.ReportSettings, res *Result, at time.Time, name string, data []byte) error {
	if len(set.Channels) == 0 {
		return ErrNoChannels
	}
	ns := reportNotifications(set.Channels)
	if set.SendFile && data != nil {
		return sc.sender.SendFileToNotificationsSync(ctx, ns, res.Subject, res.Body, name, data)
	}
	return sc.sender.SendNotificationsSync(ctx, ns, res.Subject, res.Body)
}

// dispatch delivers the report to the report's own configured channels
// (Settings → Laporan). Report delivery is fully independent of the alert
// notification channels. File delivery is used when enabled and the attachment
// rendered; other channels get the plain summary.
func (sc *Scheduler) dispatch(ctx context.Context, set models.ReportSettings, res *Result, at time.Time) {
	if len(set.Channels) == 0 {
		return
	}
	var name string
	var data []byte
	if set.SendFile {
		name, data = attachFile(set, at, res)
	}

	ns := reportNotifications(set.Channels)
	if data != nil {
		if _, err := sc.writeFile(name, data); err != nil {
			log.Printf("[report] write attachment failed: %v", err)
		}
		sc.sender.SendFileToNotifications(ctx, ns, res.Subject, res.Body, name, data)
	} else {
		sc.sender.SendToNotifications(ctx, ns, res.Subject, res.Body)
	}
}

// hasDestination reports whether the settings have any place to send to.
func hasDestination(set models.ReportSettings) bool {
	return len(set.Channels) > 0
}

// reportNotifications converts report channels into transient Notification
// values so the dispatcher's per-type senders can be reused without storing
// them as alert channels.
func reportNotifications(cs []models.ReportChannel) []*models.Notification {
	out := make([]*models.Notification, 0, len(cs))
	for _, c := range cs {
		out = append(out, &models.Notification{
			ID:     c.ID,
			Name:   c.Name,
			Type:   c.Type,
			Config: c.Config,
		})
	}
	return out
}

// attachFile picks the report file to deliver based on the configured format,
// returning its period-labelled name and bytes. Falls back to CSV when the
// Excel workbook could not be rendered.
func attachFile(set models.ReportSettings, at time.Time, res *Result) (string, []byte) {
	base := baseName(set.Frequency, at)
	if set.FileFormat == "csv" {
		return base + ".csv", []byte(res.CSV)
	}
	if len(res.XLSX) > 0 {
		return base + ".xlsx", res.XLSX
	}
	return base + ".csv", []byte(res.CSV)
}

// writeCSV stores the rendered table under the report directory using a
// period-specific file name (e.g. uptime-weekly-2026-W45.csv).
func (sc *Scheduler) writeCSV(frequency string, at time.Time, content string) (string, error) {
	return sc.writeFile(fileName(frequency, at), []byte(content))
}

// writeFile persists arbitrary report bytes under the report directory using
// the given (period-labelled) file name.
func (sc *Scheduler) writeFile(name string, data []byte) (string, error) {
	dir := sc.dir
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// baseName returns the extension-free, period-labelled report name.
func baseName(frequency string, at time.Time) string {
	switch frequency {
	case "weekly":
		y, w := at.ISOWeek()
		return fmt.Sprintf("uptime-weekly-%04d-W%02d", y, w)
	case "monthly":
		return fmt.Sprintf("uptime-monthly-%s", at.Format("2006-01"))
	default:
		return fmt.Sprintf("uptime-daily-%s", at.Format("2006-01-02"))
	}
}

// fileName maps a frequency + instant to a stable period-labelled CSV name.
func fileName(frequency string, at time.Time) string {
	return baseName(frequency, at) + ".csv"
}

// windowFor returns the reporting window length for the frequency.
func windowFor(frequency string) time.Duration {
	switch frequency {
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 31 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// dueAt computes the scheduled send instant for the period containing now and
// the window start. It reports due=false when the scheduled instant is not
// within the ticker's catch-up horizon (last 60s) of now.
func dueAt(set models.ReportSettings, now time.Time) (time.Time, time.Time) {
	hour, min := parseSendTime(set.SendTime)
	freq := set.Frequency
	if freq == "" {
		freq = "daily"
	}

	var sched time.Time
	switch freq {
	case "weekly":
		day := parseWeekday(set.WeeklyDay)
		// walk back from today to the target weekday, then stamp the time
		d := now
		for i := 0; i < 7; i++ {
			if d.Weekday() == day {
				break
			}
			d = d.AddDate(0, 0, -1)
		}
		sched = time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, now.Location())
	case "monthly":
		dom := set.MonthlyDay
		if dom < 1 {
			dom = 1
		}
		if dom > 28 {
			dom = 28 // keeps every month representable
		}
		sched = time.Date(now.Year(), now.Month(), dom, hour, min, 0, 0, now.Location())
		if sched.After(now) {
			// target day later this month not reached yet: check previous month
			prev := sched.AddDate(0, -1, 0)
			if withinHorizon(prev, now) {
				return prev, prev.Add(-windowFor(freq))
			}
			return time.Time{}, time.Time{}
		}
	default: // daily
		sched = time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	}
	if !withinHorizon(sched, now) {
		return time.Time{}, time.Time{}
	}
	return sched, sched.Add(-windowFor(freq))
}

// withinHorizon reports whether sched already passed and is fresher than the
// 60s ticker horizon, so exactly one tick claims the period.
func withinHorizon(sched, now time.Time) bool {
	d := now.Sub(sched)
	return d >= 0 && d <= 60*time.Second
}

// parseSendTime reads "HH:MM"; anything invalid falls back to 09:00.
func parseSendTime(s string) (int, int) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 9, 0
	}
	return t.Hour(), t.Minute()
}

// parseWeekday maps a weekday name (English or 3-letter prefix) to time.Weekday.
func parseWeekday(s string) time.Weekday {
	names := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	for i, n := range names {
		if strings.EqualFold(s, n) {
			return time.Weekday(i)
		}
	}
	return time.Monday
}
