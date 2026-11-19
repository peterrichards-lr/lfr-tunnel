package client

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// A long-running tunnel is told when its standing changes, not only at its next start (#2523).

func versionState(now time.Time, warningIn, deadlineIn time.Duration) *MinVersionState {
	return &MinVersionState{
		Required: true, Phase: ConsentPhaseGrace, MinVersion: "v1.53.0", ClientVersion: "v1.52.1",
		Deadline:  now.Add(deadlineIn).Format(time.RFC3339),
		WarningAt: now.Add(warningIn).Format(time.RFC3339),
	}
}

func TestNextMinVersionReminderWalksTheWindow(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	m := versionState(now, 9*24*time.Hour, 14*24*time.Hour)

	at, next, ok := NextMinVersionReminder(m, now)
	if !ok || next.Phase != ConsentPhaseWarning || !at.Equal(now.Add(9*24*time.Hour)) {
		t.Fatalf("from grace the next reminder should be the warning window at +9d, got %v %+v %v", at, next, ok)
	}
	if next.SecondsRemaining != int64((5 * 24 * time.Hour).Seconds()) {
		t.Errorf("at the warning the countdown should be the 5 days left, got %ds", next.SecondsRemaining)
	}

	at, next, ok = NextMinVersionReminder(next, at)
	if !ok || next.Phase != ConsentPhaseExpired || !at.Equal(now.Add(14*24*time.Hour)) {
		t.Fatalf("from the warning the next reminder should be the deadline at +14d, got %v %+v %v", at, next, ok)
	}

	if _, _, ok = NextMinVersionReminder(next, at); ok {
		t.Error("after the deadline there is nothing further to say")
	}
	for name, m := range map[string]*MinVersionState{
		"nil":          nil,
		"not required": {Required: false, Deadline: m.Deadline},
		"no deadline":  {Required: true, Phase: ConsentPhaseGrace},
	} {
		if _, _, ok := NextMinVersionReminder(m, now); ok {
			t.Errorf("%s: scheduled a reminder for nothing", name)
		}
	}
}

// A gateway that predates warning_at still gets a warning, at its default window.
func TestNextMinVersionReminderFallsBackWithoutWarningAt(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	m := versionState(now, 0, 14*24*time.Hour)
	m.WarningAt = ""
	at, next, ok := NextMinVersionReminder(m, now)
	if !ok || next.Phase != ConsentPhaseWarning || !at.Equal(now.Add(9*24*time.Hour)) {
		t.Fatalf("without warning_at the warning should fall 5 days before the deadline, got %v %+v %v", at, next, ok)
	}
}

// The schedule actually fires, once per transition, in order -- and a newer registration
// replaces the pending reminder rather than adding a second.
func TestMinVersionReminderFiresEachTransitionOnce(t *testing.T) {
	var mu sync.Mutex
	var said []string
	say := func(m *MinVersionState) {
		mu.Lock()
		said = append(said, m.Phase)
		mu.Unlock()
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), said...)
	}

	var r minVersionReminder
	// RFC3339 has one-second resolution, so the steps are whole seconds apart.
	now := time.Now().Truncate(time.Second)
	r.schedule(versionState(now, time.Second, 2*time.Second), now, say)

	deadline := time.Now().Add(5 * time.Second)
	for len(snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := strings.Join(snapshot(), ","); got != "warning,expired" {
		t.Fatalf("reminders said %q, want warning then expired, once each", got)
	}

	mu.Lock()
	said = nil
	mu.Unlock()
	now = time.Now().Truncate(time.Second)
	r.schedule(versionState(now, time.Second, 10*time.Second), now, say)
	r.schedule(&MinVersionState{}, now, say) // a later registration: nothing outstanding
	time.Sleep(1500 * time.Millisecond)
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("a replaced reminder still fired: %v", got)
	}
}

// The Inspector serves the state its banner is drawn from.
func TestInspectorStateCarriesTheMinVersionStanding(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	now := time.Now()
	engine.NoteMinVersion(versionState(now, time.Hour, 2*time.Hour))
	port := startInspectorForTest(t, engine, 56241)

	_, body := getInspector(t, port, "/api/state")
	var state struct {
		MinVersion *MinVersionState `json:"min_version"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decoding /api/state: %v", err)
	}
	if state.MinVersion == nil || state.MinVersion.MinVersion != "v1.53.0" || state.MinVersion.Phase != ConsentPhaseGrace {
		t.Fatalf("/api/state carries %+v; the banner would have nothing to show", state.MinVersion)
	}

	_, page := getInspector(t, port, "/")
	if !strings.Contains(string(page), `id="min-version-banner"`) {
		t.Error("the Inspector page has no banner element")
	}
}

// Every Inspector language carries all three banner strings, each naming the versions and the
// command. A missing one falls back to English silently, and a translation that dropped {3}
// would tell people their client is too old without saying how to fix it.
func TestInspectorBannerStringsInEveryLanguage(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	port := startInspectorForTest(t, engine, 56242)
	_, page := getInspector(t, port, "/")
	html := string(page)

	start := strings.Index(html, "const clientTranslations = {")
	if start < 0 {
		t.Fatal("no translation bundle in the Inspector page")
	}
	for _, lang := range []string{"en", "es", "de", "fr", "pt", "ja"} {
		i := strings.Index(html[start:], `"`+lang+`": {`)
		if i < 0 {
			t.Fatalf("no %s bundle", lang)
		}
		block := html[start+i:]
		if end := strings.Index(block, "\n    },"); end > 0 {
			block = block[:end]
		}
		for _, phase := range []string{"grace", "warning", "expired"} {
			key := `"client_min_version_` + phase + `": "`
			k := strings.Index(block, key)
			if k < 0 {
				t.Errorf("%s has no client_min_version_%s", lang, phase)
				continue
			}
			value := block[k+len(key):]
			value = value[:strings.Index(value, `",`)]
			for _, ph := range []string{"{0}", "{1}", "{3}"} {
				if !strings.Contains(value, ph) {
					t.Errorf("%s client_min_version_%s drops %s: %s", lang, phase, ph, value)
				}
			}
			if phase != "expired" && !strings.Contains(value, "{2}") {
				t.Errorf("%s client_min_version_%s does not give the date: %s", lang, phase, value)
			}
		}
	}
}
