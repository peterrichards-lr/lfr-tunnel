package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// tuiLogWriter intercepts and buffers standard log outputs for display inside the TUI.
type tuiLogWriter struct {
	mu       sync.Mutex
	logs     []string
	original io.Writer
}

func (w *tuiLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	lines := strings.Split(strings.TrimSpace(string(p)), "\n")
	for _, l := range lines {
		if l != "" {
			w.logs = append(w.logs, l)
			if len(w.logs) > 6 { // Keep last 6 log lines
				w.logs = w.logs[1:]
			}
		}
	}
	return len(p), nil
}

func (w *tuiLogWriter) GetLogs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	copied := make([]string, len(w.logs))
	copy(copied, w.logs)
	return copied
}

// StartTUIDashboard launches the terminal dashboard loop.
// It returns a cleanup function that restores terminal settings.
func StartTUIDashboard(ctx context.Context, engine *InterceptorEngine, publicURLs []string) func() {
	// Redirect logger
	logWriter := &tuiLogWriter{
		original: os.Stderr,
	}
	log.SetOutput(logWriter)

	// Enter alternative screen buffer, clear, and hide cursor
	fmt.Print("\033[?1049h")
	fmt.Print("\033[?25l")
	fmt.Print("\033[2J")

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				render(engine, publicURLs, logWriter.GetLogs())
			}
		}
	}()

	// Cleanup closure
	return func() {
		log.SetOutput(os.Stderr)
		fmt.Print("\033[?25h")   // Show cursor
		fmt.Print("\033[?1049l") // Exit alternative screen buffer
		wg.Wait()
	}
}

func render(engine *InterceptorEngine, publicURLs []string, systemLogs []string) {
	engine.mu.RLock()
	state := engine.ConnState
	uptime := formatUptime(engine.UptimeStart)
	reqTotal := engine.RequestsTotal
	bytesIn := engine.BytesIn
	bytesOut := engine.BytesOut
	latency := engine.LatencyLast
	activeConns := engine.ActiveConnections
	history := make([]*RequestRecord, len(engine.History))
	copy(history, engine.History)
	subdomainReq := engine.SubdomainReq
	subdomainAss := engine.SubdomainAss
	destPort := engine.DestPort
	// Snapshot the endpoint here too. Failover rewrites region and server URL together,
	// and reading them unlocked further down raced with that write and could show the
	// new region label beside the old edge host.
	selectedRegion := engine.SelectedRegion
	engineServerURL := engine.ServerURL
	targetHost := engine.TargetHost
	inspectorPort := engine.inspectorPort
	engine.mu.RUnlock()

	// Calculate RTT average
	rttAvg := "N/A"
	engine.mu.RLock()
	if len(engine.LatencyHistory) > 0 {
		var sum int64
		for _, val := range engine.LatencyHistory {
			sum += val
		}
		rttAvg = fmt.Sprintf("%d ms", sum/int64(len(engine.LatencyHistory)))
	}
	engine.mu.RUnlock()

	// eol clears from the cursor to the end of the current line before the newline that
	// follows it. Every line below whose content can vary in length between renders needs
	// this -- moving the cursor to home (\033[H) and overwriting line-by-line, as this
	// whole function does on every redraw, only overwrites however many characters the new
	// content actually has; anything past that from a *longer* previous frame (a longer
	// status text, a longer log line, a bigger byte-count string, etc.) is left on screen
	// untouched, since a bare newline only moves the cursor and never erases anything.
	// Reported as garbled/duplicated-looking text mid-line (e.g. "200 OK (17ms)ied (3ms)"
	// -- the tail end of a longer previous line bleeding through a shorter new one) and as
	// "4.4 MBKB" on the byte counters.
	const eol = "\033[K"

	// Title Banner
	fmt.Print("\033[H")   // Cursor to home
	fmt.Print("\033[36m") // Cyan
	fmt.Printf("================================================================================%s\n", eol)
	fmt.Print("  LIFERAY TUNNEL CLIENT                                            ")

	// Colored Status Label
	statusLabel := "\033[31mOFFLINE\033[36m"
	switch state {
	case "connected":
		statusLabel = "\033[32mCONNECTED\033[36m"
	case "connecting":
		statusLabel = "\033[33mCONNECTING\033[36m"
	}
	fmt.Printf("[%s]  %s\n", statusLabel, eol)
	fmt.Printf("================================================================================%s\n", eol)
	fmt.Print("\033[0m") // Reset

	// Configuration Info
	sub := subdomainReq
	if subdomainAss != "" {
		sub = subdomainAss
	}
	fmt.Printf("  Subdomain:  \033[1;37m%s\033[0m%s\n", sub, eol)

	// Region / Edge Node
	if selectedRegion != "" {
		edgeHost := ""
		if u, err := url.Parse(engineServerURL); err == nil && u.Host != "" {
			edgeHost = u.Host
		}
		if edgeHost != "" {
			fmt.Printf("  Region:     \033[1;37m%s (%s)\033[0m%s\n", selectedRegion, edgeHost, eol)
		} else {
			fmt.Printf("  Region:     \033[1;37m%s\033[0m%s\n", selectedRegion, eol)
		}
	} else if engineServerURL != "" {
		if u, err := url.Parse(engineServerURL); err == nil && u.Host != "" {
			fmt.Printf("  Edge Node:  \033[1;37m%s\033[0m%s\n", u.Host, eol)
		}
	}

	fmt.Printf("  Server:     \033[90m%s\033[0m%s\n", strings.Join(publicURLs, ", "), eol)
	if targetHost == "" {
		targetHost = "127.0.0.1"
	}
	fmt.Printf("  Local:      \033[90m%s:%d (Primary)\033[0m%s\n", targetHost, destPort, eol)
	if inspectorPort > 0 {
		fmt.Printf("  Inspector:  \033[34mhttp://127.0.0.1:%d\033[0m%s\n", inspectorPort, eol)
	} else {
		fmt.Printf("  Inspector:  \033[90m(not running)\033[0m%s\n", eol)
	}
	fmt.Printf("--------------------------------------------------------------------------------%s\n", eol)

	// Metrics Grid
	fmt.Printf("  Uptime:       %-12s | Active Conns:  %-12d%s\n", uptime, activeConns, eol)
	fmt.Printf("  Total Reqs:   %-12d | RTT Latency:   %d ms (Avg: %s)%s\n", reqTotal, latency, rttAvg, eol)
	fmt.Printf("  Bytes In:     %-12s | Bytes Out:     %s%s\n", formatBytes(bytesIn), formatBytes(bytesOut), eol)
	fmt.Printf("================================================================================%s\n", eol)

	// Scrolling Request History
	fmt.Printf("  RECENT HTTP REQUESTS (SCROLLING):%s\n", eol)
	fmt.Print("\033[90m") // Dark Gray
	if len(history) == 0 {
		fmt.Printf("  (No traffic captured yet. Make requests to your public domain to view.)%s\n", eol)
		// Fill space to prevent jumpy screen sizes
		for i := 0; i < 7; i++ {
			fmt.Printf("%s\n", eol)
		}
	} else {
		// Display 8 most recent requests (newest at top)
		limit := 8
		if limit > len(history) {
			limit = len(history)
		}
		printed := 0
		for i := 0; i < limit; i++ {
			rec := history[i]
			timeStr := rec.Time.Format("15:04:05")
			statusStr := colorStatus(rec.Status)

			// Format method and path
			methodStr := fmt.Sprintf("\033[1;36m%-6s\033[0m", rec.Method)
			pathStr := truncateRunes(rec.Path, 45)

			fmt.Printf("  [%s] %s %-45s -> %s (%dms)%s\n", timeStr, methodStr, pathStr, statusStr, rec.DurationMs, eol)
			printed++
		}
		// Pad remaining space
		for i := printed; i < 8; i++ {
			fmt.Printf("%s\n", eol)
		}
	}
	fmt.Print("\033[0m") // Reset

	// System logs box
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("  SYSTEM LOGS & EVENTS:")
	fmt.Print("\033[90m") // Dark Gray
	logLimit := 5
	printedLogs := 0
	for i := len(systemLogs) - logLimit; i < len(systemLogs); i++ {
		if i >= 0 {
			line := truncateRunes(systemLogs[i], 78)
			fmt.Printf("  * %s%s\n", line, eol)
			printedLogs++
		}
	}
	for i := printedLogs; i < logLimit; i++ {
		fmt.Println(eol)
	}
	fmt.Print("\033[0m") // Reset
	fmt.Print(eol)       // Clear any leftover tail on whatever was the terminal's last-drawn line
}

// truncateRunes shortens s to at most maxLen characters, appending an ellipsis when it
// had to cut. It counts runes rather than bytes: slicing a UTF-8 string at a byte offset
// can land mid-character and emit a replacement glyph, and the client's own log lines
// carry multi-byte characters (the portal and status-page hints use an arrow, the ops
// output uses check and clock marks) -- precisely the lines long enough to be truncated.
// Counting runes also keeps the fmt padding verbs aligned, since they count runes too.
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatUptime(uptimeStart time.Time) string {
	if uptimeStart.IsZero() {
		return "00:00"
	}
	d := time.Since(uptimeStart).Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func colorStatus(status int) string {
	if status == 0 {
		return "\033[33mIn-Flight\033[0m"
	}
	color := "\033[37m" // white
	if status >= 200 && status < 300 {
		color = "\033[32m" // green
	} else if status >= 300 && status < 400 {
		color = "\033[33m" // yellow
	} else if status >= 400 {
		color = "\033[31m" // red
	}
	return fmt.Sprintf("%s%d %s\033[0m", color, status, http.StatusText(status))
}
