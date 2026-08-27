package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

// configuredLogDir is the operator's chosen log directory, or "" for the default.
//
// Held at package level, and set once at startup, because LogDir has several readers as
// well as the writer -- ClientLogPath and ResolveClientLogPath back the Inspector's log
// viewer. Threading the value only as far as the writer would leave the Inspector reading
// an empty default directory and reporting no logs at all (#1223). The mutex is because
// the Inspector serves HTTP concurrently with the tunnel.
var (
	logDirMu         sync.RWMutex
	configuredLogDir string
)

// SetLogDir records where the persistent logs should live. Empty restores the default.
// Call it before opening any logger.
func SetLogDir(dir string) {
	logDirMu.Lock()
	defer logDirMu.Unlock()
	configuredLogDir = strings.TrimSpace(dir)
}

// ConfiguredLogDir returns the directory that was explicitly configured, or "" when the
// default is in use. Distinct from LogDir, which resolves and creates the effective path:
// this is for reporting what the operator actually set.
func ConfiguredLogDir() string {
	logDirMu.RLock()
	defer logDirMu.RUnlock()
	return configuredLogDir
}

// LogDir returns the directory holding the client's persistent logs, creating it if
// necessary. Defaults to ~/.lfr-tunnel/logs when nothing is configured.
func LogDir() (string, error) {
	dir, err := resolveLogDir(ConfiguredLogDir())
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		// Name the directory. A configured path that cannot be created is usually a typo,
		// and an error that does not say which path failed is not actionable.
		return "", fmt.Errorf("creating log directory %s: %w", dir, err)
	}
	return dir, nil
}

// resolveLogDir turns a configured value into an absolute path, without touching the disk
// so it stays testable. Empty yields the default location.
func resolveLogDir(configured string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if configured == "" {
		return filepath.Join(home, ".lfr-tunnel", "logs"), nil
	}
	return expandHome(configured, home), nil
}

// expandHome expands a leading ~ against home. Only a leading ~ is meaningful; a tilde
// elsewhere in a path is an ordinary character.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	return path
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

// SessionLogPath is one of the three logs the client writes: where it is, what it holds,
// and whether it exists yet.
type SessionLogPath struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Size   int64  `json:"size"`
}

// SessionLogPaths reports all three logs a subdomain writes.
//
// Built from the same helpers the writers use -- ResolveClientLogPath for the console log
// and LogDir for the other two -- rather than re-deriving the names here, so a change to
// where logs live moves the writer and this reporter together. #1129 added the traffic and
// error logs and nothing ever told the operator they existed; #1223 then added a Settings
// field naming the directory, which made that worse rather than better.
//
// Paths as well as contents: the Inspector serves all three logs (#1423). An earlier
// version of this comment argued for withholding the traffic log because it records
// request and response bodies. That reasoning did not survive review -- /api/state
// already returns the last 100 RequestRecords with ReqBody, RespBody, ReqHeaders,
// RespHeaders and the passcode, and /api/replay re-sends them, all through the same
// guardLocalOnly origin check. The traffic log is the same class of data over a longer
// window, not a new exposure, and the operator reading it can already see the live
// equivalent.
//
// The paths stay useful in their own right: they answer "where is this on disk" for
// someone who wants to grep, ship or rotate the file rather than read it in a browser.
func SessionLogPaths(subdomain string) []SessionLogPath {
	if subdomain == "" {
		return nil
	}
	stat := func(kind, path string) SessionLogPath {
		entry := SessionLogPath{Kind: kind, Path: path}
		if info, err := os.Stat(path); err == nil {
			entry.Exists = true
			entry.Size = info.Size()
		}
		return entry
	}

	paths := make([]SessionLogPath, 0, 3)
	// The console log is the odd one out: ResolveClientLogPath falls back to the
	// pre-move ~/.lfr-tunnel location, so on an upgraded machine it can sit in a
	// different directory from the other two. That is why each entry carries a full
	// path rather than a shared directory heading and three filenames.
	if console, err := ResolveClientLogPath(subdomain); err == nil {
		paths = append(paths, stat(LogKindConsole, console))
	}
	dir, err := LogDir()
	if err != nil {
		return paths
	}
	paths = append(paths,
		stat(LogKindTraffic, filepath.Join(dir, fmt.Sprintf("traffic-%s.log", subdomain))),
		stat(LogKindError, filepath.Join(dir, fmt.Sprintf("error-%s.log", subdomain))),
	)
	return paths
}

// The three logs a client session writes. Named because they are part of the Inspector's
// URL surface (/api/logs/<kind>) and its JSON, not just internal labels.
const (
	// LogKindConsole is the background-mode console log, served by /api/logs.
	LogKindConsole = "console"
	// LogKindTraffic is the per-request log, served by /api/logs/traffic.
	LogKindTraffic = "traffic"
	// LogKindError is the diagnostic event log, served by /api/logs/error.
	LogKindError = "error"
)

// DefaultLogTailBytes is how much of a log the Inspector returns by default.
//
// These files rotate at DefaultLogMaxBytes (8 MiB), and the Logs tab polls every few
// seconds, so serving whole files would ship megabytes per poll to render a view whose
// useful part is the recent end.
const DefaultLogTailBytes int64 = 256 << 10

// ReadSessionLog returns the tail of one of the client's persistent logs.
//
// kind is "console", "traffic" or "error". Reading the tail rather than the whole file
// keeps a poll cheap on an 8 MiB log; the returned bool reports whether the content was
// truncated, so a caller can say so rather than silently presenting a partial file as
// complete.
//
// Splitting on the first newline after the offset matters for traffic and error, which
// are JSON Lines: an arbitrary byte offset lands mid-object and the first line would fail
// to parse.
func ReadSessionLog(kind, subdomain string, maxBytes int64) ([]byte, bool, error) {
	var path string
	var err error
	switch kind {
	case LogKindConsole:
		path, err = ResolveClientLogPath(subdomain)
	case LogKindTraffic, LogKindError:
		var dir string
		if dir, err = LogDir(); err == nil {
			path = filepath.Join(dir, fmt.Sprintf("%s-%s.log", kind, subdomain))
		}
	default:
		return nil, false, fmt.Errorf("unknown log %q", kind)
	}
	if err != nil {
		return nil, false, err
	}

	f, err := os.Open(path) //nolint:gosec // path is built from LogDir and a validated kind
	if err != nil {
		return nil, false, err
	}
	defer f.Close() //nolint:errcheck

	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if maxBytes <= 0 || info.Size() <= maxBytes {
		data, rerr := io.ReadAll(f)
		return data, false, rerr
	}

	if _, err = f.Seek(info.Size()-maxBytes, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	// Drop the partial first line so JSON Lines stays parseable.
	if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
		data = data[idx+1:]
	}
	return data, true, nil
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

	if err := RotateFile(path, generations); err != nil {
		return nil, err
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

// RotateFile shifts an existing log into the next generation, so the caller can start a
// fresh file while keeping the previous run. Exported for callers that need a real
// *os.File rather than a RotatingFile -- notably the background-mode console log, whose
// handle is passed to a child process as a file descriptor.
//
// Rolls only when there is content worth keeping: a client that starts and stops
// repeatedly must not push real content out through empty generations.
func RotateFile(path string, generations int) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return nil //nolint:nilerr // nothing to rotate is not a failure
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return rotateGenerations(path, generations)
}

// rotateGenerations shifts path -> path.1 -> path.2 ... discarding the oldest.
func rotateGenerations(path string, generations int) error {
	if generations <= 0 {
		return os.Remove(path)
	}
	oldest := fmt.Sprintf("%s.%d", path, generations)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := generations - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		to := fmt.Sprintf("%s.%d", path, i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
		if err := rotateGenerations(r.path, r.generations); err != nil {
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
	if len(s) <= maxLoggedBodyBytes {
		return s
	}
	// Back up to a rune boundary. Bodies are arbitrary UTF-8, and cutting mid-character
	// leaves invalid bytes that json.Marshal silently rewrites to U+FFFD -- the same
	// defect class fixed for the TUI in #1130.
	cut := maxLoggedBodyBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
