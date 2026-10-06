package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// The client half of #2344: a successful registration says when the token expires, and the client
// warns inside the gateway's window. Every case goes red against the code before it, which had
// neither the field nor the warning.

var expiryNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func expiringIn(days float64, window int) *client.RegisterResponse {
	return &client.RegisterResponse{
		TokenExpiresAt:         expiryNow.Add(time.Duration(days * float64(24*time.Hour))).Format(time.RFC3339),
		TokenExpiryWarningDays: window,
		PortalURL:              "https://portal.example",
	}
}

func TestTokenExpiryWarningHonoursTheGatewaysWindow(t *testing.T) {
	cfg := &config.ClientConfig{TokenSource: "-token flag"}
	for _, tc := range []struct {
		name   string
		resp   *client.RegisterResponse
		warned bool
	}{
		{"3 days left, window 7", expiringIn(3, 7), true},
		{"10 days left, window 7", expiringIn(10, 7), false},
		{"10 days left, window 14: the gateway's window, not a constant", expiringIn(10, 14), true},
		{"3 days left, window 2", expiringIn(3, 2), false},
		{"3 days left, older gateway sends no window: default 7", expiringIn(3, 0), true},
		{"already expired: refusal explains it, not this", expiringIn(-1, 7), false},
		{"never expires", &client.RegisterResponse{}, false},
		{"older gateway sends nothing", &client.RegisterResponse{TokenExpiryWarningDays: 7}, false},
		{"unparseable expiry", &client.RegisterResponse{TokenExpiresAt: "soon", TokenExpiryWarningDays: 7}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := len(tokenExpiryWarning(cfg, tc.resp, expiryNow)) > 0
			if got != tc.warned {
				t.Errorf("warned=%v, want %v", got, tc.warned)
			}
		})
	}
}

// The remedy is chosen exactly as the 401 advice chooses it (#2342): login only where it actually
// replaces the token that is about to expire.
func TestTokenExpiryWarningNamesTheRemedyForTheSource(t *testing.T) {
	for _, tc := range []struct {
		source string
		login  bool
	}{
		{config.TokenSourceDefaultFile + " (/home/dev/.lfr-tunnel/token)", true},
		{config.TokenSourceLDMFile + " (/home/dev/.config/lfr/secrets)", true},
		{"LFT_TOKEN environment variable", false},
		{"token_file (/elsewhere)", false},
		{"-token flag", false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			text := strings.Join(tokenExpiryWarning(&config.ClientConfig{TokenSource: tc.source}, expiringIn(3, 7), expiryNow), "\n")
			if text == "" {
				t.Fatal("no warning at all, so the remedy cannot be checked")
			}
			if strings.Contains(text, loginCommand) != tc.login {
				t.Errorf("source %q: advised login=%v, want %v:\n%s", tc.source, !tc.login, tc.login, text)
			}
			if !strings.Contains(text, tc.source) {
				t.Errorf("the warning must say where the expiring token was read from:\n%s", text)
			}
		})
	}
}

// Once per expiry date per process: a client that fails over or reconnects must not repeat it on
// every registration, but a different date -- the token replaced, a new one expiring -- is news.
func TestTokenExpiryWarningIsPrintedOncePerExpiry(t *testing.T) {
	var buf bytes.Buffer
	prevLogger, prevWarned := slog.Default(), tokenExpiryWarned
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger); tokenExpiryWarned = prevWarned })
	tokenExpiryWarned = ""

	cfg := &config.ClientConfig{TokenSource: "-token flag"}
	count := func() int { return strings.Count(buf.String(), "Your access token expires") }

	first := expiringIn(3, 7)
	warnTokenExpiryOnce(cfg, first, expiryNow)
	warnTokenExpiryOnce(cfg, first, expiryNow)
	if got := count(); got != 1 {
		t.Errorf("the same expiry was warned about %d times, want once", got)
	}

	warnTokenExpiryOnce(cfg, expiringIn(5, 7), expiryNow)
	if got := count(); got != 2 {
		t.Errorf("a different expiry date must be warned about afresh; total warnings = %d, want 2", got)
	}
}

func TestExpiresInReadsNaturally(t *testing.T) {
	for d, want := range map[time.Duration]string{
		3*24*time.Hour + time.Hour: "in 3 days",
		36 * time.Hour:             "in 1 day",
		5 * time.Hour:              "in 5 hours",
		90 * time.Minute:           "in 1 hour",
		20 * time.Minute:           "within the hour",
	} {
		if got := expiresIn(d); got != want {
			t.Errorf("expiresIn(%v) = %q, want %q", d, got, want)
		}
	}
}
