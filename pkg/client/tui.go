package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
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

	// Title Banner
	fmt.Print("\033[H")   // Cursor to home
	fmt.Print("\033[36m") // Cyan
	fmt.Println("================================================================================")
	fmt.Print("  LIFERAY TUNNEL CLIENT                                            ")

	// Colored Status Label
	statusLabel := "\033[31mOFFLINE\033[36m"
	switch state {
	case "connected":
		statusLabel = "\033[32mCONNECTED\033[36m"
	case "connecting":
		statusLabel = "\033[33mCONNECTING\033[36m"
	}
	fmt.Printf("[%s]  \n", statusLabel)
	fmt.Println("================================================================================")
	fmt.Print("\033[0m") // Reset

	// Configuration Info
	sub := subdomainReq
	if subdomainAss != "" {
		sub = subdomainAss
	}
	fmt.Printf("  Subdomain:  \033[1;37m%s\033[0m\n", sub)
	fmt.Printf("  Server:     \033[90m%s\033[0m\n", strings.Join(publicURLs, ", "))
	fmt.Printf("  Local:      \033[90m127.0.0.1:%d (Primary)\033[0m\n", destPort)
	fmt.Printf("  Inspector:  \033[34mhttp://127.0.0.1:%d\033[0m\n", 4040)
	fmt.Println("--------------------------------------------------------------------------------")

	// Metrics Grid
	fmt.Printf("  Uptime:       %-12s | Active Conns:  %-12d\n", uptime, activeConns)
	fmt.Printf("  Total Reqs:   %-12d | RTT Latency:   %d ms (Avg: %s)\n", reqTotal, latency, rttAvg)
	fmt.Printf("  Bytes In:     %-12s | Bytes Out:     %s\n", formatBytes(bytesIn), formatBytes(bytesOut))
	fmt.Println("================================================================================")

	// Scrolling Request History
	fmt.Println("  RECENT HTTP REQUESTS (SCROLLING):")
	fmt.Print("\033[90m") // Dark Gray
	if len(history) == 0 {
		fmt.Println("  (No traffic captured yet. Make requests to your public domain to view.)")
		// Fill space to prevent jumpy screen sizes
		for i := 0; i < 7; i++ {
			fmt.Println()
		}
	} else {
		// Limit to last 8 requests
		limit := 8
		startIdx := len(history) - limit
		if startIdx < 0 {
			startIdx = 0
		}
		printed := 0
		for i := startIdx; i < len(history); i++ {
			rec := history[i]
			timeStr := rec.Time.Format("15:04:05")
			statusStr := colorStatus(rec.Status)

			// Format method and path
			methodStr := fmt.Sprintf("\033[1;36m%-6s\033[0m", rec.Method)
			pathLimit := 45
			pathStr := rec.Path
			if len(pathStr) > pathLimit {
				pathStr = pathStr[:pathLimit-3] + "..."
			}

			fmt.Printf("  [%s] %s %-45s -> %s (%dms)\n", timeStr, methodStr, pathStr, statusStr, rec.DurationMs)
			printed++
		}
		// Pad remaining space
		for i := printed; i < limit; i++ {
			fmt.Println()
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
			line := systemLogs[i]
			// Trim timestamp if it's already there to save width
			if len(line) > 78 {
				line = line[:75] + "..."
			}
			fmt.Printf("  * %s\n", line)
			printedLogs++
		}
	}
	for i := printedLogs; i < logLimit; i++ {
		fmt.Println()
	}
	fmt.Print("\033[0m") // Reset
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
