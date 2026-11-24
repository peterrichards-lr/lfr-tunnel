package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"lfr-tunnel/pkg/client"
)

// backgroundRegistrationWait bounds how long `-background` waits for the tunnel it spawned to
// register before handing back (#2573). A var so tests can shorten it.
//
// Long enough for a normal start (discovery, region election, one registration round trip),
// short enough that a wrapper calling it synchronously is not left hanging on a slow network:
// the pending outcome below exits 0 and the tunnel keeps trying on its own.
var backgroundRegistrationWait = 20 * time.Second

// backgroundPollInterval is how often the wait looks for the tunnel's state file.
const backgroundPollInterval = 200 * time.Millisecond

// backgroundOutcome is what `-background` learned about the tunnel it started.
type backgroundOutcome int

const (
	// backgroundRegistered: the tunnel wrote its state file, which it does only after the
	// gateway accepted the registration.
	backgroundRegistered backgroundOutcome = iota
	// backgroundRefused: the tunnel exited with an error before registering -- the gateway
	// refused it (token, minimum version, subdomain, consent) or it could not start at all.
	backgroundRefused
	// backgroundPending: neither within the wait, or the tunnel exited cleanly before
	// registering (a restart into an upgrade hands over to a new process). Not a failure.
	backgroundPending
)

// backgroundStart describes the spawned tunnel for awaitRegistration. Every field is injected so
// the wait can be tested without starting a process: the client binary is never run locally.
type backgroundStart struct {
	pid       int
	exited    <-chan error
	findState func(pid int) *client.ClientState
	wait      time.Duration
	interval  time.Duration
}

type backgroundResult struct {
	outcome backgroundOutcome
	state   *client.ClientState
	exitErr error
}

// awaitRegistration waits until the tunnel has registered, has exited, or the wait runs out.
//
// Before #2573 `-background` returned as soon as the process started. Everything the gateway
// refuses happens after that, so a wrapper (LDM, the MCP server, a script) saw exit 0 and then
// nothing, and LDM reported a 10-second "connection timeout" for what was really a revoked token
// or a taken subdomain.
func (b backgroundStart) awaitRegistration() backgroundResult {
	deadline := time.NewTimer(b.wait)
	defer deadline.Stop()
	tick := time.NewTicker(b.interval)
	defer tick.Stop()

	for {
		if st := b.findState(b.pid); st != nil {
			return backgroundResult{outcome: backgroundRegistered, state: st}
		}
		select {
		case err := <-b.exited:
			// The tunnel may have registered and then exited between the check above and
			// now; the state file says which, so look once more before calling it a refusal.
			if st := b.findState(b.pid); st != nil {
				return backgroundResult{outcome: backgroundRegistered, state: st}
			}
			if err == nil {
				return backgroundResult{outcome: backgroundPending}
			}
			return backgroundResult{outcome: backgroundRefused, exitErr: err}
		case <-deadline.C:
			return backgroundResult{outcome: backgroundPending}
		case <-tick.C:
		}
	}
}

// refusalExcerptMaxLines caps how much of the log a refusal prints.
const refusalExcerptMaxLines = 40

// refusalExcerptTailLines is how much of the log's end is printed when no error line is found.
const refusalExcerptTailLines = 15

// refusalExcerpt picks the lines that explain why the tunnel stopped: from the last "[Error]" line
// to the end of the log, which is where every refusal prints the gateway's reason followed by its
// advice. A log with no such line yields its last few lines instead.
func refusalExcerpt(logText string) []string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(logText, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "[Error]") {
			start = i
			break
		}
	}
	if start < 0 {
		start = max(len(lines)-refusalExcerptTailLines, 0)
	}
	out := lines[start:]
	if len(out) > refusalExcerptMaxLines {
		out = out[:refusalExcerptMaxLines]
	}
	return out
}

// reportBackgroundRefusal writes why the tunnel did not start to w (stderr), quoting its log.
func reportBackgroundRefusal(w io.Writer, sub, logPath string, exitErr error) {
	var b strings.Builder
	fmt.Fprintf(&b, "[Error] The background tunnel for subdomain '%s' did not start (%v):\n", sub, exitErr)
	if data, err := os.ReadFile(logPath); err == nil {
		for _, l := range refusalExcerpt(string(data)) {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	fmt.Fprintf(&b, "[Client] Full log: %s\n", logPath)
	if _, err := io.WriteString(w, b.String()); err != nil {
		slog.Debug("could not print the refusal", "error", err)
	}
}

// removePIDFileIfOwned removes the subdomain's pidfile when it still names pid, so a refused start
// does not leave a pidfile behind for a process that no longer exists.
func removePIDFileIfOwned(sub string, pid int) {
	if cur, err := readPID(sub); err == nil && cur == pid {
		if path, perr := getPIDFilePath(sub); perr == nil {
			if rerr := os.Remove(path); rerr != nil {
				slog.Debug("could not remove the refused tunnel's pidfile", "error", rerr)
			}
		}
	}
}
