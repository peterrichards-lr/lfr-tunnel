package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Making the client's logs safe to send (#1885, part 2 of #1763).
//
// #1696's constraint, and the reason this exists as its own change: "redact before upload, and
// verify it". Upload turns a local-only leak into a transmitted one, so the tests here seed
// secrets into each log and assert they do not survive, rather than asserting that a redactor was
// called.
//
// BODIES ARE STRIPPED, NOT REDACTED. TrafficEntry.ReqBody and RespBody hold the developer's own
// application payloads, and no pattern-based pass can be trusted over arbitrary content -- it
// will miss things, and "we redacted it" is a stronger claim than anyone can back. The flag that
// records them says as much itself: "may contain tokens and customer data". Nothing diagnostic is
// lost for the case #1763 was filed for -- a routing problem is read from method, path, status,
// duration, port and region, all of which still travel.
//
// Everything else is redacted rather than dropped, because the remaining fields are the diagnosis.

// redactedPlaceholder is what replaces a secret. Deliberately visible: a reader of an uploaded
// log should be able to tell "there was a token here and it was removed" from "there was never a
// token here", because those two say different things about what they are looking at.
const redactedPlaceholder = "[REDACTED]"

// patPattern matches this product's personal access tokens, which carry the lfr_pat_ prefix
// (pkg/server/server_auth.go:140). Listed first because it is the one secret this repo issues and
// therefore the one it is responsible for.
var patPattern = regexp.MustCompile(`\blfr_pat_[A-Za-z0-9_\-]+`)

// bearerPattern catches an Authorization value that reached free text.
var bearerPattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._\-+/=]{8,}`)

// urlCredentialPattern catches credentials embedded in a URL (https://user:pass@host).
var urlCredentialPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/\s:@]+:[^/\s@]+@`)

// queryCredentialPattern catches a secret carried in a query string, which is how tokens most
// often end up in a path that otherwise looks harmless.
var queryCredentialPattern = regexp.MustCompile(`(?i)\b(token|api[_-]?key|apikey|secret|password|passwd|pwd|auth|access[_-]?token|refresh[_-]?token|signature|sig|session)=([^&\s"']+)`)

// secretFieldNames are the map keys in EventEntry.Fields whose VALUE is replaced outright,
// whatever it looks like. Matching on the name as well as the shape catches a secret that does
// not look like one -- a short passphrase, a numeric PIN.
var secretFieldNames = []string{
	"token", "password", "passwd", "pwd", "secret", "key", "auth",
	"authorization", "credential", "cookie", "session", "pat", "signature",
}

// redactText removes secret-shaped substrings from free text. Used for the console log, for
// string values in the error log, and for request paths.
func redactText(s string) string {
	if s == "" {
		return s
	}
	s = patPattern.ReplaceAllString(s, redactedPlaceholder)
	s = urlCredentialPattern.ReplaceAllString(s, "${1}"+redactedPlaceholder+"@")
	s = bearerPattern.ReplaceAllString(s, "${1} "+redactedPlaceholder)
	s = queryCredentialPattern.ReplaceAllString(s, "${1}="+redactedPlaceholder)
	return s
}

// looksLikeSecretName reports whether a field name names a secret.
func looksLikeSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretFieldNames {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// redactFields walks EventEntry.Fields, which is map[string]any and can hold anything the client
// chose to log.
func redactFields(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if looksLikeSecretName(k) {
			out[k] = redactedPlaceholder
			continue
		}
		switch tv := v.(type) {
		case string:
			out[k] = redactText(tv)
		case map[string]any:
			out[k] = redactFields(tv)
		case []any:
			arr := make([]any, 0, len(tv))
			for _, item := range tv {
				if s, ok := item.(string); ok {
					arr = append(arr, redactText(s))
					continue
				}
				arr = append(arr, item)
			}
			out[k] = arr
		default:
			// Numbers, bools and nulls cannot carry a token, and rewriting them would change
			// the shape of a log somebody is about to read.
			out[k] = v
		}
	}
	return out
}

// redactTrafficLine rewrites one line of the traffic log, or reports that it could not.
//
// Fails CLOSED. A line that does not parse as a TrafficEntry cannot be shown to contain no body,
// so it is dropped rather than passed through -- an unparseable line in a bodies-enabled traffic
// log is as likely to be an enormous payload that broke something as it is to be noise. The
// caller counts what it dropped and says so in the output, because a silently shorter log reads
// as a quieter system.
func redactTrafficLine(line []byte) ([]byte, bool) {
	var entry TrafficEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, false
	}
	// The decision of #1885: excluded, not redacted.
	entry.ReqBody = ""
	entry.RespBody = ""
	entry.Path = redactPath(entry.Path)

	out, err := json.Marshal(entry)
	if err != nil {
		return nil, false
	}
	return out, true
}

// redactPath keeps a path readable while removing credentials from its query string. The path
// itself is the diagnosis -- which route was slow, which returned 502 -- so it is not discarded.
func redactPath(p string) string {
	if p == "" {
		return p
	}
	// Parse where possible so that an encoded value is caught too; fall back to the text pass,
	// which is what handles a malformed path.
	if u, err := url.Parse(p); err == nil && u.RawQuery != "" {
		q := u.Query()
		changed := false
		for k := range q {
			if looksLikeSecretName(k) {
				q.Set(k, redactedPlaceholder)
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
			return redactText(u.String())
		}
	}
	return redactText(p)
}

// redactEventLine rewrites one line of the error log. Unlike the traffic log there is no body
// field, so an unparseable line is redacted as free text rather than dropped: the error log is
// where a diagnosis usually is, and losing a line costs more than it does in the traffic log.
func redactEventLine(line []byte) []byte {
	var entry EventEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return []byte(redactText(string(line)))
	}
	entry.Event = redactText(entry.Event)
	entry.Fields = redactFields(entry.Fields)
	out, err := json.Marshal(entry)
	if err != nil {
		return []byte(redactText(string(line)))
	}
	return out
}

// DefaultCollectionMaxBytes bounds one collection across all three logs.
//
// The unbounded figure is 3 files x 3 generations x 8 MiB, roughly 72 MiB per client, which is
// not a sensible thing to push at a gateway on an admin's click. Only the current generation is
// read (see collectOne), so the realistic input is 24 MiB; this caps the redacted output well
// under that and keeps the newest lines, which are the ones that describe the problem being
// reported.
const DefaultCollectionMaxBytes int64 = 4 << 20 // 4 MiB

// RedactedLog is one log file, cleaned and bounded, ready for part 3 to upload.
type RedactedLog struct {
	Kind string `json:"kind"`
	// Content is the redacted log, newest lines last.
	Content []byte `json:"-"`
	// Truncated records that older lines were dropped to fit the budget. Reported rather than
	// silent: a log that stops early without saying so reads as a system that went quiet.
	Truncated bool `json:"truncated"`
	// DroppedLines counts traffic lines that could not be parsed and were therefore dropped
	// rather than passed through unchecked.
	DroppedLines int `json:"dropped_lines"`
	// Bytes is the size of Content, so a caller can report the bundle without holding it.
	Bytes int `json:"bytes"`
}

// CollectRedactedLogs reads this client's three logs, redacts them, and returns them bounded to
// maxTotalBytes in total. It never reads a rotated generation: the current file is what describes
// the session being asked about, and the older ones multiply the size by three for history nobody
// asked for.
//
// A missing log is not an error -- a client that has never hit an error has no error log -- and
// is simply absent from the result.
func CollectRedactedLogs(dir, subdomain string, maxTotalBytes int64) ([]RedactedLog, error) {
	if maxTotalBytes <= 0 {
		maxTotalBytes = DefaultCollectionMaxBytes
	}
	if dir == "" {
		resolved, err := LogDir()
		if err != nil {
			return nil, fmt.Errorf("resolving the log directory: %w", err)
		}
		dir = resolved
	}

	// Ordered by how much each is worth when the budget runs out: the error log names the
	// failure, the console log carries the client's own story, and the traffic log is the
	// largest and the most replaceable.
	wanted := []struct {
		kind string
		path string
	}{
		{LogKindError, filepath.Join(dir, fmt.Sprintf("error-%s.log", subdomain))},
		{LogKindConsole, filepath.Join(dir, fmt.Sprintf("client-%s.log", subdomain))},
		{LogKindTraffic, filepath.Join(dir, fmt.Sprintf("traffic-%s.log", subdomain))},
	}

	var out []RedactedLog
	remaining := maxTotalBytes
	for _, w := range wanted {
		if remaining <= 0 {
			break
		}
		log, err := collectOne(w.kind, w.path, remaining)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if len(log.Content) == 0 && log.DroppedLines == 0 {
			continue
		}
		remaining -= int64(len(log.Content))
		out = append(out, log)
	}
	return out, nil
}

// collectOne reads and redacts a single log, keeping the NEWEST lines within budget.
func collectOne(kind, path string, budget int64) (RedactedLog, error) {
	// No //nolint here on purpose: .golangci.yml already excludes gosec G304 globally and
	// lists (*os.File).Close in errcheck's exclude-functions, so a suppression would spend a
	// slot on the nolint ratchet for a warning that was never going to fire.
	f, err := os.Open(path)
	if err != nil {
		return RedactedLog{}, err
	}
	defer func() { _ = f.Close() }()

	result := RedactedLog{Kind: kind}

	// Redact line by line, keeping a sliding window of the newest lines that fit. Holding the
	// whole file would defeat the cap on a client whose traffic log is at its 8 MiB rotation
	// point.
	var kept [][]byte
	var keptBytes int64

	scanner := bufio.NewScanner(f)
	// A single traffic line with a body can be large; the default 64KB token limit would end
	// the scan early and silently.
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)

	for scanner.Scan() {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		line := make([]byte, len(raw))
		copy(line, raw)

		var redacted []byte
		switch kind {
		case LogKindTraffic:
			var ok bool
			redacted, ok = redactTrafficLine(line)
			if !ok {
				result.DroppedLines++
				continue
			}
		case LogKindError:
			redacted = redactEventLine(line)
		default:
			redacted = []byte(redactText(string(line)))
		}

		kept = append(kept, redacted)
		keptBytes += int64(len(redacted)) + 1

		for keptBytes > budget && len(kept) > 0 {
			keptBytes -= int64(len(kept[0])) + 1
			kept = kept[1:]
			result.Truncated = true
		}
	}
	if err := scanner.Err(); err != nil {
		// Return what was read. A partially readable log still names the failure, and losing
		// it to a read error is the outcome this feature exists to prevent.
		result.Truncated = true
	}

	var buf bytes.Buffer
	for _, l := range kept {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	if result.DroppedLines > 0 {
		// Said out loud, in the log itself, so whoever reads it knows it is not the whole
		// story. A silently shorter log reads as a quieter system.
		fmt.Fprintf(&buf, "%s %d line(s) were unparseable and were dropped rather than uploaded unchecked.\n",
			redactedPlaceholder, result.DroppedLines)
	}
	result.Content = buf.Bytes()
	result.Bytes = len(result.Content)
	return result, nil
}
