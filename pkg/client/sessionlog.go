package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultLogMaxBytes is the size a log file may reach before it is rotated.
	DefaultLogMaxBytes int64 = 8 << 20 // 8 MiB
	// DefaultLogGenerations is how many rotated generations are kept alongside the
	// live file. The point of keeping any is that the run worth reading is usually the
	// one that just ended.
	DefaultLogGenerations = 3
	// maxLoggedBodyBytes caps a body written to the traffic log, matching the cap the
	// in-memory inspector history already applies.
	maxLoggedBodyBytes = 10 * 1024
)

// LogDir returns the directory holding the client's persistent logs.
func LogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lfr-tunnel", "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// LegacyClientLogPath is where background-mode output was written before logs moved
// into their own directory. Readers still check it so a log from an older client
// remains visible.
func LegacyClientLogPath(subdomain string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", subdomain)), nil
}

// ClientLogPath returns the current location of the background-mode console log.
func ClientLogPath(subdomain string) (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("client-%s.log", subdomain)), nil
}

// ResolveClientLogPath returns the console log to read for a subdomain, preferring the
// current location but falling back to the pre-move path so a log written by an older
// client is still reachable. Returns the current path when neither exists, so callers
// can report where they looked.
func ResolveClientLogPath(subdomain string) (string, error) {
	current, err := ClientLogPath(subdomain)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(current); statErr == nil {
		return current, nil
	}
	if legacy, lerr := LegacyClientLogPath(subdomain); lerr == nil {
		if _, statErr := os.Stat(legacy); statErr == nil {
			return legacy, nil
		}
	}
	return current, nil
}

// RotatingFile is an append-only file that rotates once it exceeds maxBytes, keeping a
// bounded number of previous generations. It replaces truncate-on-start: a client that
// dies unexpectedly leaves its log behind instead of erasing it on the next run.
//
// Safe for concurrent writers.
type RotatingFile struct {
	mu          sync.Mutex
	path        string
	maxBytes    int64
	generations int
	file        *os.File
	size        int64
}

// OpenRotatingFile opens path for appending, rotating any existing content into a new
// generation first so each run starts on a fresh file while the previous run is kept.
func OpenRotatingFile(path string, maxBytes int64, generations int) (*RotatingFile, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultLogMaxBytes
	}
	if generations < 0 {
		generations = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	r := &RotatingFile{path: path, maxBytes: maxBytes, generations: generations}

	// Only roll if there is something worth keeping; otherwise a client that starts and
	// stops repeatedly would push real content out through empty generations.
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		if err := r.roll(); err != nil {
			return nil, err
		}
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close() //nolint:errcheck
		return err
	}
	r.file, r.size = f, info.Size()
	return nil
}

// roll shifts path -> path.1 -> path.2 ... discarding the oldest generation. The caller
// must hold the lock, or hold no references yet.
func (r *RotatingFile) roll() error {
	if r.generations == 0 {
		return os.Remove(r.path)
	}
	oldest := fmt.Sprintf("%s.%d", r.path, r.generations)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := r.generations - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", r.path, i)
		to := fmt.Sprintf("%s.%d", r.path, i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Write appends p, rotating first if it would take the file past its size cap.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}

	if r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.file.Close(); err != nil {
			return 0, err
		}
		if err := r.roll(); err != nil {
			return 0, err
		}
		if err := r.open(); err != nil {
			return 0, err
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// Close releases the underlying file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// TrafficEntry is one request/response pair as written to the traffic log.
type TrafficEntry struct {
	Time       time.Time `json:"ts"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"dur_ms"`
	TargetPort int       `json:"port"`
	Region     string    `json:"region,omitempty"`
	ReqBody    string    `json:"req_body,omitempty"`
	RespBody   string    `json:"resp_body,omitempty"`
}

// EventEntry is one diagnostic event as written to the error log.
type EventEntry struct {
	Time   time.Time      `json:"ts"`
	Level  string         `json:"level"`
	Event  string         `json:"event"`
	Fields map[string]any `json:"fields,omitempty"`
}

// SessionLogger writes the client's two persistent logs: every proxied request, and the
// diagnostic events needed to explain a tunnel that misbehaved. Both are JSON Lines so
// they can be filtered with jq and parsed back by tooling.
//
// A nil *SessionLogger is usable and discards everything, so callers never have to
// guard their logging calls.
type SessionLogger struct {
	traffic   *RotatingFile
	events    *RotatingFile
	logBodies bool
}

// NewSessionLogger opens the traffic and error logs for a subdomain. logBodies enables
// recording request/response payloads, which is off by default because bodies routinely
// carry OAuth tokens, session cookies and customer data and these files persist on disk.
func NewSessionLogger(dir, subdomain string, logBodies bool) (*SessionLogger, error) {
	traffic, err := OpenRotatingFile(
		filepath.Join(dir, fmt.Sprintf("traffic-%s.log", subdomain)),
		DefaultLogMaxBytes, DefaultLogGenerations)
	if err != nil {
		return nil, err
	}
	events, err := OpenRotatingFile(
		filepath.Join(dir, fmt.Sprintf("error-%s.log", subdomain)),
		DefaultLogMaxBytes, DefaultLogGenerations)
	if err != nil {
		_ = traffic.Close() //nolint:errcheck
		return nil, err
	}
	return &SessionLogger{traffic: traffic, events: events, logBodies: logBodies}, nil
}

func writeJSONLine(w *RotatingFile, v any) {
	if w == nil {
		return
	}
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	// Logging must never take down the tunnel, so write failures (full disk, removed
	// directory) are dropped rather than propagated.
	_, _ = w.Write(append(line, '\n')) //nolint:errcheck
}

func truncateBody(s string) string {
	if len(s) > maxLoggedBodyBytes {
		return s[:maxLoggedBodyBytes]
	}
	return s
}

// Traffic records one proxied request.
func (l *SessionLogger) Traffic(rec *RequestRecord, region string) {
	if l == nil || rec == nil {
		return
	}
	entry := TrafficEntry{
		Time:       rec.Time,
		Method:     rec.Method,
		Path:       rec.Path,
		Status:     rec.Status,
		DurationMs: rec.DurationMs,
		TargetPort: rec.TargetPort,
		Region:     region,
	}
	if l.logBodies {
		entry.ReqBody = truncateBody(rec.ReqBody)
		entry.RespBody = truncateBody(rec.RespBody)
	}
	writeJSONLine(l.traffic, entry)
}

// Event records a diagnostic event. level is conventionally "info", "warn" or "error".
func (l *SessionLogger) Event(level, event string, fields map[string]any) {
	if l == nil {
		return
	}
	writeJSONLine(l.events, EventEntry{
		Time:   time.Now().UTC(),
		Level:  level,
		Event:  event,
		Fields: fields,
	})
}

// Close releases both log files.
func (l *SessionLogger) Close() error {
	if l == nil {
		return nil
	}
	var firstErr error
	if err := l.traffic.Close(); err != nil {
		firstErr = err
	}
	if err := l.events.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
