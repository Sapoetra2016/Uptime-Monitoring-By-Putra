// Package report builds and schedules the periodic uptime summary that is
// delivered through the notification channels configured in Settings and
// written as a CSV file to a separate report directory.
package report

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"math"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uptime/monitoring/internal/models"
	"github.com/uptime/monitoring/internal/store"
	"github.com/uptime/monitoring/internal/sysdns"
	"github.com/xuri/excelize/v2"
)

// source is the subset of *store.Store the builder needs. Keeping it an
// interface makes the builder easy to test and prevents import cycles.
type source interface {
	ListMonitors(ctx context.Context) ([]models.Monitor, error)
	MonitorStats(ctx context.Context, monitorID string, since time.Time) (*store.Stats, error)
}

// Row is one service line in the report table.
type Row struct {
	Name     string
	Domain   string // target host/domain extracted from the monitor URL or host
	PublicIP string
	LocalIP  string
	Uptime   float64 // percent up over the window
	Down     float64 // percent down over the window
	SLA      float64 // configured SLO/SLA target percent
	Total    int
	DownCnt  int
}

// Result is a rendered report ready for dispatch and file output.
type Result struct {
	Subject string
	Body    string // human summary for chat/email
	CSV     string // full per-service table
	XLSX    []byte // Excel workbook of the same table (may be nil on error)
	Rows    []Row
}

// csvHeader is the fixed column order requested by the admin.
var csvHeader = []string{"Nama", "IP Public", "IP Lokal", "Uptime (%)", "Down (%)", "SLA (%)"}

// Build aggregates per-monitor availability over the window and renders both
// a plain-text summary and a CSV table (columns: name, public IP, local IP,
// uptime, down, SLA). subjTpl/bodyTpl, when non-empty, override the default
// message wording via {period}/{avg}/{services}/{top} placeholders.
func Build(ctx context.Context, st *store.Store, since, until time.Time, subjTpl, bodyTpl string) (*Result, error) {
	return build(ctx, st, since, until, subjTpl, bodyTpl)
}

func build(ctx context.Context, src source, since, until time.Time, subjTpl, bodyTpl string) (*Result, error) {
	monitors, err := src.ListMonitors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	enabled := make([]models.Monitor, 0, len(monitors))
	for _, m := range monitors {
		if m.Enabled {
			enabled = append(enabled, m)
		}
	}

	// Resolve public IPs concurrently (best-effort) before touching the DB so
	// slow lookups overlap instead of serialising the whole report.
	publicIPs := resolveIPs(ctx, enabled)

	rows := make([]Row, 0, len(enabled))
	var sumAvail float64
	var counted int
	for _, m := range enabled {
		st, err := src.MonitorStats(ctx, m.ID, since)
		if err != nil || st == nil {
			st = &store.Stats{}
		}
		r := Row{
			Name:     m.Name,
			Domain:   targetHost(m),
			PublicIP: publicIPs[m.ID],
			LocalIP:  m.LocalIp,
			SLA:      m.SloTargetPct,
			Total:    st.TotalChecks,
			DownCnt:  st.DownChecks,
		}
		if st.TotalChecks > 0 {
			r.Uptime = st.Availability * 100
			r.Down = float64(st.DownChecks) / float64(st.TotalChecks) * 100
			sumAvail += st.Availability
			counted++
		} else {
			r.Uptime = -1 // no data
		}
		rows = append(rows, r)
	}

	overall := 0.0
	if counted > 0 {
		overall = sumAvail / float64(counted) * 100
	}

	// worst availability first so problems surface at the top of the table
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aj := rows[i].Uptime, rows[j].Uptime
		if ai < 0 {
			return false
		}
		if aj < 0 {
			return true
		}
		return ai < aj
	})

	csvStr := renderCSV(rows)
	xlsx, xerr := renderXLSX(rows, since, until)
	if xerr != nil {
		log.Printf("[report] xlsx render failed: %v", xerr)
		xlsx = nil
	}
	body := renderBody(rows, overall, since, until, len(enabled))
	window := formatWindow(since, until)
	subject := fmt.Sprintf("[Uptime Report] %s — rata-rata %.2f%%", window, overall)

	// Custom templates from Settings override the built-in wording when set.
	if subjTpl != "" || bodyTpl != "" {
		vars := templateVars(rows, overall, window, len(enabled))
		if subjTpl != "" {
			subject = applyTemplate(subjTpl, vars)
		}
		if bodyTpl != "" {
			body = applyTemplate(bodyTpl, vars)
		}
	}

	return &Result{Subject: subject, Body: body, CSV: csvStr, XLSX: xlsx, Rows: rows}, nil
}

// Default message templates offered in Settings. SubjectTemplate and
// BodyTemplate in ReportSettings are empty until the admin customises them,
// in which case the built-in rendering above stays in effect.
const (
	DefaultSubjectTemplate = "UPTIME REPORT — {period}"
	DefaultBodyTemplate    = "UPTIME REPORT\n" +
		"Periode : {period}\n" +
		"Layanan : {services} | rata-rata uptime {avg}%\n\n" +
		"Top layanan bermasalah (uptime terendah):\n{top}\n\n" +
		"Tabel lengkap (nama, IP public, IP lokal, uptime, down, SLA) terlampir sebagai file.\n" +
		"Dikirim otomatis oleh Uptime Monitoring."
)

// applyTemplate replaces {var} placeholders (lowercase and uppercase forms)
// with their values.
func applyTemplate(tpl string, vars map[string]string) string {
	out := tpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
		out = strings.ReplaceAll(out, "{"+strings.ToUpper(k)+"}", v)
	}
	return out
}

// templateVars exposes the placeholders admins can use in report messages.
func templateVars(rows []Row, overall float64, window string, services int) map[string]string {
	var top strings.Builder
	shown := 0
	for _, r := range rows {
		if r.Uptime < 0 {
			continue
		}
		top.WriteString(fmt.Sprintf("- %s — uptime %.2f%%, down %.2f%%, SLA %.2f%%\n", r.Name, r.Uptime, r.Down, r.SLA))
		shown++
		if shown >= 10 {
			break
		}
	}
	if shown == 0 {
		top.WriteString("- belum ada data pada periode ini")
	}
	return map[string]string{
		"period":   window,
		"avg":      fmt.Sprintf("%.2f", overall),
		"services": fmt.Sprintf("%d", services),
		"top":      top.String(),
	}
}

// renderCSV encodes the table (header + rows) as CSV text.
func renderCSV(rows []Row) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(csvHeader)
	for _, r := range rows {
		rec := []string{
			r.Name,
			orDash(r.PublicIP),
			orDash(r.LocalIP),
			fmtPct(r.Uptime),
			fmtPct(r.Down),
			fmtPct(r.SLA),
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return b.String()
}

// renderXLSX builds the two-sheet report workbook that mirrors the admin's
// Excel template:
//
//	Sheet 1 "Uptime Monitoring" — per-server table with Up/Down minutes, a
//	Total Durasi column and an SLA (%) column, plus a totals/average row.
//	Sheet 2 "Analisa Uptime" — KPI block, a downtime-evaluation table and a
//	column chart comparing SLA per server.
//
// Derived numbers (Total, SLA %, KPIs, status) are computed in Go and written
// as plain values rather than Excel formulas: excelize stores formulas without
// a cached result, so viewers that do not recalculate on open (email preview,
// some mobile/WPS setups) showed those cells as blank. Computing them here
// keeps every cell populated and still consistent, since all values come from
// the same source rows. Up/Down are in minutes derived from the report window
// so SLA (Up/Total) matches the template. Returns nil bytes only on error
// (callers then fall back to the CSV attachment).
func renderXLSX(rows []Row, since, until time.Time) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	const dataSheet = "Uptime Monitoring"
	const analSheet = "Analisa Uptime"
	if err := f.SetSheetName("Sheet1", dataSheet); err != nil {
		return nil, err
	}
	if _, err := f.NewSheet(analSheet); err != nil {
		return nil, err
	}

	// Window duration in minutes drives the Up/Down minute columns.
	totalMinutes := int(math.Round(until.Sub(since).Minutes()))
	if totalMinutes <= 0 {
		totalMinutes = 1
	}

	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 16}})
	subtitleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 11}})
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"1F4E78"}},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	totalStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	pctStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 10}) // 0.00%
	pctBoldStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 10, Font: &excelize.Font{Bold: true}})
	kpiLabelStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})

	// ---------- Sheet 1: Uptime Monitoring ----------
	_ = f.SetCellStr(dataSheet, "A1", "PT EDI INDONESIA")
	_ = f.SetCellStyle(dataSheet, "A1", "A1", titleStyle)
	_ = f.SetCellStr(dataSheet, "A2", "Laporan & Monitoring Availability Server (Uptime Monitoring)")
	_ = f.SetCellStyle(dataSheet, "A2", "A2", subtitleStyle)

	headers := []string{"No", "Nama Server / Layanan", "Nama Domain", "IP Lokal", "IP Public", "Up (Menit)", "Down (Menit)", "Total Durasi (Menit)", "SLA (%)"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 4)
		_ = f.SetCellStr(dataSheet, cell, h)
	}
	_ = f.SetCellStyle(dataSheet, "A4", "I4", headerStyle)

	const firstDataRow = 5
	var sumUp, sumDown, sumTotal int
	var slaSum float64
	var slaCount int
	for i, r := range rows {
		rr := firstDataRow + i
		_ = f.SetCellValue(dataSheet, fmt.Sprintf("A%d", rr), i+1)
		xlsxSetStr(f, dataSheet, 2, rr, r.Name)
		xlsxSetStr(f, dataSheet, 3, rr, orDash(r.Domain))
		xlsxSetStr(f, dataSheet, 4, rr, orDash(r.LocalIP))
		xlsxSetStr(f, dataSheet, 5, rr, orDash(r.PublicIP))
		up, down := 0, 0
		if r.Uptime >= 0 {
			up = int(math.Round(float64(totalMinutes) * r.Uptime / 100))
			down = int(math.Round(float64(totalMinutes) * r.Down / 100))
		}
		total := up + down
		sla := 0.0
		if total > 0 {
			sla = float64(up) / float64(total)
		}
		_ = f.SetCellValue(dataSheet, fmt.Sprintf("F%d", rr), up)
		_ = f.SetCellValue(dataSheet, fmt.Sprintf("G%d", rr), down)
		// Total Durasi (H) and SLA % (I) are written as computed values so they
		// render in every viewer, including ones that do not recalculate.
		_ = f.SetCellValue(dataSheet, fmt.Sprintf("H%d", rr), total)
		_ = f.SetCellValue(dataSheet, fmt.Sprintf("I%d", rr), sla)
		_ = f.SetCellStyle(dataSheet, fmt.Sprintf("I%d", rr), fmt.Sprintf("I%d", rr), pctStyle)
		sumUp += up
		sumDown += down
		sumTotal += total
		if r.Uptime >= 0 {
			slaSum += sla
			slaCount++
		}
	}
	lastDataRow := firstDataRow + len(rows) - 1
	overallRow := lastDataRow + 1
	avgSLA := 0.0
	if slaCount > 0 {
		avgSLA = slaSum / float64(slaCount)
	}
	_ = f.SetCellStr(dataSheet, fmt.Sprintf("B%d", overallRow), "Rata-rata / Total Overall")
	_ = f.SetCellStyle(dataSheet, fmt.Sprintf("B%d", overallRow), fmt.Sprintf("B%d", overallRow), totalStyle)
	_ = f.SetCellValue(dataSheet, fmt.Sprintf("F%d", overallRow), sumUp)
	_ = f.SetCellValue(dataSheet, fmt.Sprintf("G%d", overallRow), sumDown)
	_ = f.SetCellValue(dataSheet, fmt.Sprintf("H%d", overallRow), sumTotal)
	_ = f.SetCellValue(dataSheet, fmt.Sprintf("I%d", overallRow), avgSLA)
	_ = f.SetCellStyle(dataSheet, fmt.Sprintf("I%d", overallRow), fmt.Sprintf("I%d", overallRow), pctBoldStyle)
	for col, w := range map[string]float64{"A": 6, "B": 34, "C": 30, "D": 16, "E": 16, "F": 14, "G": 14, "H": 20, "I": 12} {
		_ = f.SetColWidth(dataSheet, col, col, w)
	}

	// ---------- Sheet 2: Analisa Uptime (formula-driven) ----------
	_ = f.SetCellStr(analSheet, "A1", "PT EDI INDONESIA")
	_ = f.SetCellStyle(analSheet, "A1", "A1", titleStyle)
	_ = f.SetCellStr(analSheet, "A2", "Executive Summary & Analisa Performa Sistem")
	_ = f.SetCellStyle(analSheet, "A2", "A2", subtitleStyle)

	_ = f.SetCellStr(analSheet, "A4", "Metric Key Performance Indicator (KPI)")
	_ = f.SetCellStyle(analSheet, "A4", "B4", headerStyle)
	_ = f.MergeCell(analSheet, "A4", "B4")

	// KPI values are computed in Go and written directly so they always render
	// (same rationale as the SLA column on Sheet 1).
	_ = f.SetCellStr(analSheet, "A5", "Total Host / Server Di-monitoring")
	_ = f.SetCellValue(analSheet, "B5", len(rows))
	_ = f.SetCellStr(analSheet, "A6", "Rata-rata SLA Keseluruhan")
	_ = f.SetCellValue(analSheet, "B6", avgSLA)
	_ = f.SetCellStyle(analSheet, "B6", "B6", pctStyle)
	_ = f.SetCellStr(analSheet, "A7", "Total Downtime Keseluruhan (Menit)")
	_ = f.SetCellValue(analSheet, "B7", sumDown)
	_ = f.SetCellStr(analSheet, "A8", "Target SLA Minimum Company")
	_ = f.SetCellValue(analSheet, "B8", 0.999)
	_ = f.SetCellStyle(analSheet, "B8", "B8", pctStyle)
	_ = f.SetCellStr(analSheet, "A9", "Status Pencapaian Target")
	status := "DIBAWAH TARGET (WARNING)"
	if avgSLA >= 0.999 {
		status = "MEMENUHI SLA (GOOD)"
	}
	_ = f.SetCellStr(analSheet, "B9", status)
	for rr := 5; rr <= 9; rr++ {
		_ = f.SetCellStyle(analSheet, fmt.Sprintf("A%d", rr), fmt.Sprintf("A%d", rr), kpiLabelStyle)
	}

	_ = f.SetCellStr(analSheet, "A12", "Analisa & Evaluasi Downtime Server")
	_ = f.SetCellStyle(analSheet, "A12", "A12", subtitleStyle)
	evalCols := []string{"No", "Nama Server", "SLA Status", "Akar Masalah (Root Cause)", "Rencana Tindakan Korektif (Action Plan)"}
	for i, h := range evalCols {
		cell, _ := excelize.CoordinatesToCellName(i+1, 14)
		_ = f.SetCellStr(analSheet, cell, h)
	}
	_ = f.SetCellStyle(analSheet, "A14", "E14", headerStyle)

	// Rows are already sorted worst-first; list the offenders that actually had
	// downtime, referencing each server's SLA cell on Sheet 1. Root Cause and
	// Action Plan are left blank for the analyst to complete.
	evalRow := 15
	n := 0
	for _, r := range rows {
		if r.Uptime < 0 || r.DownCnt <= 0 {
			continue
		}
		up := int(math.Round(float64(totalMinutes) * r.Uptime / 100))
		down := int(math.Round(float64(totalMinutes) * r.Down / 100))
		sla := 0.0
		if up+down > 0 {
			sla = float64(up) / float64(up+down)
		}
		_ = f.SetCellValue(analSheet, fmt.Sprintf("A%d", evalRow), n+1)
		xlsxSetStr(f, analSheet, 2, evalRow, r.Name)
		_ = f.SetCellValue(analSheet, fmt.Sprintf("C%d", evalRow), sla)
		_ = f.SetCellStyle(analSheet, fmt.Sprintf("C%d", evalRow), fmt.Sprintf("C%d", evalRow), pctStyle)
		evalRow++
		n++
		if n >= 10 {
			break
		}
	}
	for col, w := range map[string]float64{"A": 34, "B": 30, "C": 14, "D": 40, "E": 45} {
		_ = f.SetColWidth(analSheet, col, col, w)
	}

	// Column chart comparing SLA per server, sourced from Sheet 1.
	if len(rows) > 0 {
		err := f.AddChart(analSheet, "G4", &excelize.Chart{
			Type: excelize.Col,
			Series: []excelize.ChartSeries{{
				Name:       fmt.Sprintf("'%s'!$I$4", dataSheet),
				Categories: fmt.Sprintf("'%s'!$B$%d:$B$%d", dataSheet, firstDataRow, lastDataRow),
				Values:     fmt.Sprintf("'%s'!$I$%d:$I$%d", dataSheet, firstDataRow, lastDataRow),
			}},
			Title: excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: "Grafik Perbandingan SLA Per Server (%)"}}},
		})
		if err != nil {
			log.Printf("[report] xlsx chart failed: %v", err)
		}
	}

	// No Excel formulas remain in the workbook (all derived cells are computed
	// values), so nothing depends on the viewer recalculating on open.
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// xlsxSetStr writes a string value to a sheet cell by column index and row.
func xlsxSetStr(f *excelize.File, sheet string, col, row int, v string) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	_ = f.SetCellStr(sheet, cell, v)
}

// renderBody produces a compact text summary suitable for chat/email. Only
// the worst offenders are listed; the full table lives in the CSV file.
func renderBody(rows []Row, overall float64, since, until time.Time, total int) string {
	var b strings.Builder
	b.WriteString("UPTIME REPORT\n")
	b.WriteString("Periode : " + formatWindow(since, until) + "\n")
	b.WriteString(fmt.Sprintf("Layanan : %d | rata-rata uptime %.2f%%\n", total, overall))
	b.WriteString("\nTop layanan bermasalah (uptime terendah):\n")
	shown := 0
	for _, r := range rows {
		if r.Uptime < 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s — uptime %.2f%%, down %.2f%%, SLA %.2f%%\n",
			r.Name, r.Uptime, r.Down, r.SLA))
		shown++
		if shown >= 10 {
			break
		}
	}
	if shown == 0 {
		b.WriteString("- belum ada data pada periode ini\n")
	}
	b.WriteString("\nTabel lengkap (nama, IP public, IP lokal, uptime, down, SLA) tersimpan sebagai file CSV di direktori report server.\n")
	b.WriteString("Dikirim otomatis oleh Uptime Monitoring.\n")
	return b.String()
}

// resolveIPs resolves one display IP per monitor in parallel. Failures yield
// an empty string (rendered as "-"); IP literals pass through unchanged.
func resolveIPs(ctx context.Context, monitors []models.Monitor) map[string]string {
	out := make(map[string]string, len(monitors))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 12) // bound concurrent lookups
	for _, m := range monitors {
		host := targetHost(m)
		if host == "" {
			continue
		}
		wg.Add(1)
		go func(id, h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			addrs, err := sysdns.LookupHost(c, h)
			if err != nil || len(addrs) == 0 {
				return
			}
			pick := pickIP(addrs)
			mu.Lock()
			out[id] = pick
			mu.Unlock()
		}(m.ID, host)
	}
	wg.Wait()
	return out
}

// pickIP prefers an IPv4 address, falling back to the first result.
func pickIP(addrs []string) string {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a
		}
	}
	if len(addrs) > 0 {
		return addrs[0]
	}
	return ""
}

// targetHost extracts the hostname whose public IP should be shown.
func targetHost(m models.Monitor) string {
	h := ""
	if strings.TrimSpace(m.URL) != "" {
		if u, err := url.Parse(m.URL); err == nil {
			h = u.Hostname()
		}
	}
	if h == "" {
		h = strings.TrimSpace(m.Host)
	}
	// strip any port like "10.1.5.1:8080"
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return h
}

func formatWindow(since, until time.Time) string {
	return fmt.Sprintf("%s s/d %s",
		since.Local().Format("02 Jan 2006 15:04"),
		until.Local().Format("02 Jan 2006 15:04"))
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func fmtPct(v float64) string {
	if v < 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", v)
}
