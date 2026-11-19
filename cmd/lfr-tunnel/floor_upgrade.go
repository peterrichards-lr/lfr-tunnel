package main

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"lfr-tunnel/pkg/client"
)

// What a client below the gateway's minimum version does about it before registering (#2523).
//
// Telling someone to run `lfr-tunnel -upgrade` and then carrying on is the least helpful thing a
// client at an interactive terminal can do: the person is right there. So at a terminal it
// offers the upgrade; anywhere else -- a script, LDM, a service -- it only says so, because a
// prompt with nobody to answer it is a hang. Either way the upgrade is SelfUpgrade, the same
// verified path `-upgrade` takes, and a successful one restarts the client so this session runs
// the new version: a client past its deadline that carried on as the old one would be refused
// at the registration that follows.

type floorUpgradeAction int

const (
	floorNothing floorUpgradeAction = iota
	// floorAsk: below the floor at a terminal, with auto_upgrade off.
	floorAsk
	// floorRestartAfterAuto: below the floor with auto_upgrade on. The automatic upgrade runs
	// as it always has; below the floor it is followed by a restart.
	floorRestartAfterAuto
)

// floorUpgradeDecision is the whole rule, as a pure function so every branch is testable.
func floorUpgradeDecision(current string, info *client.ServerVersionInfo, autoUpgrade, interactive bool) floorUpgradeAction {
	if current == "" || current == "dev" || info == nil || info.MinVersion == "" {
		return floorNothing
	}
	if client.CompareVersions(current, info.MinVersion) >= 0 {
		return floorNothing
	}
	if autoUpgrade {
		return floorRestartAfterAuto
	}
	if interactive {
		return floorAsk
	}
	return floorNothing
}

// floorPromptTimeout bounds the question. Unanswered means no: an unattended terminal -- a
// tunnel started in a tmux pane nobody is looking at -- must not wait forever, and must not
// change the binary without a yes.
const floorPromptTimeout = 30 * time.Second

// askToUpgrade asks once and reports whether the answer was yes. Enter alone is yes, as the
// [Y/n] says; no answer within the timeout is no.
//
// After a timeout the reader goroutine is still parked on stdin and will take the next line
// typed. That is accepted: Go has no portable way to cancel a terminal read, and the cost is one
// line of input at an unattended terminal, against a client that would otherwise never start.
func askToUpgrade(out io.Writer, in io.Reader, current, floor string, timeout time.Duration) bool {
	// A question that could not be shown is not asked: no answer to it can be taken as yes.
	if _, err := fmt.Fprintf(out, "\nThis client (%s) is older than the minimum this gateway accepts (%s).\nUpgrade now? [Y/n] ", current, floor); err != nil {
		return false
	}
	answer := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			// stdin closed or unreadable before anything was typed: that is not an Enter.
			answer <- "n"
			return
		}
		answer <- strings.ToLower(strings.TrimSpace(line))
	}()
	select {
	case a := <-answer:
		return a == "" || a == "y" || a == "yes"
	case <-time.After(timeout):
		if _, err := fmt.Fprintln(out, "\nNo answer; continuing without upgrading."); err != nil {
			slog.Debug("could not write the prompt timeout notice", "error", err)
		}
		return false
	}
}

// upgradeAndRestart runs the verified upgrade and restarts into it. Any failure is reported and
// the client carries on as it is -- the same contract as the automatic upgrade.
func upgradeAndRestart(current, serverURL string) {
	if err := client.SelfUpgrade(current, serverURL); err != nil {
		slog.Warn(fmt.Sprintf("[Client] Upgrade failed, continuing on %s: %v", current, err))
		return
	}
	restartIntoUpgrade()
}

// restartIntoUpgrade restarts this client so the session runs the version just installed.
func restartIntoUpgrade() {
	slog.Info("[Client] Upgrade installed. Restarting on the new version...")
	if err := client.RestartSelf(); err != nil {
		slog.Warn(fmt.Sprintf("[Client] Could not restart automatically (%v). The upgrade is installed; start the client again to use it.", err))
	}
}
