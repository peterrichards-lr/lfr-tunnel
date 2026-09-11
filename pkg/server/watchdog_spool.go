package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
)

// Forwarding what the gateway watchdog did, to a person (#1875).
//
// scripts/common/gateway-watchdog.sh detects a dead lfr-tunneld or nginx and restarts it. That is
// the right behaviour and it told nobody: the only record was journald on a box nobody is
// watching, so a gateway could go down, restart and come back with no trace -- while whoever was
// using the portal saw an outage they had no way to explain or to know was over. Same shape as
// #1824: the system did the right thing and said nothing.
//
// The delivery problem is that the gateway being down IS the event, so the notice cannot go
// through the gateway's own API at the time it happens. The watchdog therefore records to a spool
// file and this forwards from it, which keeps one delivery path and one set of SMTP credentials
// (#1732) instead of teaching a root shell script to send mail with a second copy of them.
//
// The cost is that the notice arrives after recovery rather than during the outage. For a
// self-healed restart that is the useful time anyway -- "it restarted itself at 03:14 and
// recovered" is a morning message. For an outage that does NOT recover, nothing here helps, and
// nothing here pretends to: that case needs a check that does not live on the box, which the
// issue records as the third option and this deliberately does not build.
//
// This reads and never writes the spool. The watchdog runs as root and lfr-tunneld does not, so a
// consume-and-delete design would need write permission across that boundary and would race with
// the watchdog appending to the file it is deleting. A high-water mark in the gateway's own
// settings needs neither, and the watchdog trims its own file.

const (
	// watchdogSpoolDefaultPath is where gateway-watchdog.sh writes unless LFT_WATCHDOG_SPOOL
	// overrides it. The two defaults must agree; tests/hooks/test-watchdog-spool.sh asserts it
	// rather than trusting two literals in two languages to be kept in step.
	watchdogSpoolDefaultPath = "/var/lib/lfr-tunnel/watchdog-events.jsonl"

	// settingWatchdogHighWater holds the timestamp of the newest event already forwarded.
	// Stored rather than derived because the alternative -- forwarding everything in the spool
	// every tick -- mails the owner the same restart once an hour forever.
	settingWatchdogHighWater = "watchdog_last_forwarded_ts"

	// alertKeyWatchdogRestart follows the alert_notify_* convention, so it is honoured by
	// adminAlertRecipient and mutable through the admin settings API like the rest.
	//
	// It defaults to ON (adminAlertRecipient only defaults alert_notify_tunnel_offline off),
	// which is the point of the issue: the failure being fixed is that nobody was told.
	alertKeyWatchdogRestart = "alert_notify_watchdog_restart"

	// watchdogMaxEventsPerAlert bounds one email. A box that restart-loops all night produces
	// hundreds of events and the owner needs to know that it did, not to read each one -- the
	// count above the list carries that, so the list can be truncated safely.
	watchdogMaxEventsPerAlert = 20
)

// watchdogEvent is one line of the spool. The field names are the contract with
// gateway-watchdog.sh's record_event.
type watchdogEvent struct {
	TS               string `json:"ts"`
	Event            string `json:"event"`
	Service          string `json:"service"`
	Outcome          string `json:"outcome"`
	RestartsLastHour int    `json:"restarts_last_hour"`
}

// watchdogSpoolPath is the configured spool, or the default the watchdog also defaults to.
func (s *Server) watchdogSpoolPath() string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.WatchdogSpoolPath) != "" {
		return strings.TrimSpace(s.cfg.WatchdogSpoolPath)
	}
	return watchdogSpoolDefaultPath
}

// readWatchdogEventsSince returns the spool's events strictly newer than since, oldest first.
//
// A missing spool is not an error: the overwhelmingly common case is a gateway whose watchdog has
// never had to do anything, and a gateway that has never needed healing must not log as though
// something were wrong with it.
//
// Malformed lines are skipped rather than failing the read. The spool is appended to by a shell
// script that can be killed mid-write, so a torn final line is expected, and one unparseable line
// must not suppress the restart recorded on the line above it.
func readWatchdogEventsSince(path, since string) ([]watchdogEvent, int, error) {
	f, err := os.Open(path) //nolint:gosec // operator-configured path, same trust as the DB path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck

	var events []watchdogEvent
	skipped := 0
	scanner := bufio.NewScanner(f)
	// The watchdog writes one compact JSON object per line; the default 64KB token limit is
	// ample, but a corrupt spool without newlines should not panic the scan.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev watchdogEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			skipped++
			continue
		}
		if ev.TS == "" || ev.Event != "restart" {
			skipped++
			continue
		}
		// Timestamps are fixed-width ISO-8601 UTC, so a string compare is a time compare --
		// and one that cannot fail to parse, which matters for a value that arrives from a
		// file rather than from code.
		if since != "" && ev.TS <= since {
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		// Return what was parsed before the error. A half-read spool still names restarts
		// that happened, and losing them to a read error is the failure this exists to stop.
		return events, skipped, err
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	return events, skipped, nil
}

// forwardWatchdogEvents mails any new watchdog restarts to the admin and advances the high-water
// mark. Safe to call when nothing has happened, which is almost always.
func (s *Server) forwardWatchdogEvents() {
	if s.db == nil {
		return
	}

	path := s.watchdogSpoolPath()
	since, err := s.db.GetAdminSetting(settingWatchdogHighWater)
	if err != nil {
		// Unknown mark. Do not treat that as "forward everything": a spool holding a night of
		// restarts would produce one alert per stale event on a gateway that is fine now.
		slog.Warn(fmt.Sprintf("[Watchdog] Could not read %s, skipping this pass: %v", settingWatchdogHighWater, err))
		return
	}

	events, skipped, err := readWatchdogEventsSince(path, since)
	if err != nil {
		slog.Error(fmt.Sprintf("[Watchdog] Failed to read the watchdog spool at %s: %v", path, err))
		// Deliberately falls through: events holds whatever parsed before the error, and a
		// restart that did happen is worth reporting even from a spool that then went bad.
	}
	if skipped > 0 {
		slog.Warn(fmt.Sprintf("[Watchdog] Skipped %d unparseable line(s) in %s", skipped, path))
	}
	if len(events) == 0 {
		return
	}

	newest := events[len(events)-1].TS

	// Advance the mark BEFORE sending. The send is async and its failure is already recorded
	// and audited by the funnel (#1732), so a send that fails is visible; a mark that fails to
	// advance is not, and would re-mail every one of these on the next tick, every tick.
	if err := s.db.SetAdminSetting(settingWatchdogHighWater, newest); err != nil {
		slog.Error(fmt.Sprintf("[Watchdog] Could not record the watchdog high-water mark: %v -- not alerting, to avoid mailing these again every tick", err))
		return
	}

	s.sendAdminAlert(alertKeyWatchdogRestart, watchdogAlertSubject(events), watchdogAlertBody(events, path))
}

// watchdogAlertSubject leads with the count and whether anything is still broken, because that is
// what decides whether the owner opens it now or after coffee.
func watchdogAlertSubject(events []watchdogEvent) string {
	failed := 0
	for _, ev := range events {
		if ev.Outcome != "recovered" {
			failed++
		}
	}
	switch {
	case failed > 0:
		return fmt.Sprintf("LFR Tunnel Alert: gateway service failed to recover (%d restart(s), %d did not recover)", len(events), failed)
	case len(events) == 1:
		return "LFR Tunnel Alert: gateway service restarted itself and recovered"
	default:
		return fmt.Sprintf("LFR Tunnel Alert: gateway restarted services %d times", len(events))
	}
}

// watchdogAlertBody is built here rather than from a template because the peak -- the highest
// restarts_last_hour seen -- has to be computed, and the issue's point is that one restart and
// three restarts in an hour are different events. A template would carry the sentence and lose
// the arithmetic.
func watchdogAlertBody(events []watchdogEvent, path string) string {
	var b strings.Builder
	peak := 0
	failed := 0
	for _, ev := range events {
		if ev.RestartsLastHour > peak {
			peak = ev.RestartsLastHour
		}
		if ev.Outcome != "recovered" {
			failed++
		}
	}

	b.WriteString("The gateway watchdog restarted one or more services on this gateway.\n\n")
	if failed > 0 {
		b.WriteString(fmt.Sprintf("%d of %d restart(s) did NOT recover. This gateway may still be degraded.\n\n", failed, len(events)))
	} else {
		b.WriteString("All of them recovered. No action may be needed -- this is a record, not a page.\n\n")
	}
	if peak >= 3 {
		b.WriteString(fmt.Sprintf("A service restarted %d times within an hour. Repeated restarts are a different\nproblem from a single one: something is putting it back into the state the\nwatchdog is healing, and healing it is not fixing that.\n\n", peak))
	}

	b.WriteString("Events:\n")
	shown := events
	if len(shown) > watchdogMaxEventsPerAlert {
		shown = shown[len(shown)-watchdogMaxEventsPerAlert:]
	}
	for _, ev := range shown {
		b.WriteString(fmt.Sprintf("  %s  %-16s %s\n", ev.TS, ev.Service, ev.Outcome))
	}
	if len(shown) < len(events) {
		b.WriteString(fmt.Sprintf("  ... and %d earlier event(s), omitted. The count above is the whole set.\n", len(events)-len(shown)))
	}

	b.WriteString(fmt.Sprintf("\nFull history on the gateway: %s\n", path))
	b.WriteString("Service logs: journalctl -u lfr-tunneld -u nginx -u gateway-watchdog\n")
	return b.String()
}
