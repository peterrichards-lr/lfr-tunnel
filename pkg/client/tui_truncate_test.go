package client

import (
	"strings"
	"testing"
)

// TestTruncateRunesDoesNotSplitCharacters is the regression test for #1130. The previous
// byte-slicing truncation could cut a multi-byte character in half and emit a
// replacement glyph, and the client's own log lines carry multi-byte characters -- the
// portal hint uses an arrow, ops output uses check and clock marks.
func TestTruncateRunesDoesNotSplitCharacters(t *testing.T) {
	// Each of these is multi-byte in UTF-8, so a byte-wise cut lands mid-character.
	line := strings.Repeat("👉", 40)

	got := truncateRunes(line, 20)
	if strings.ContainsRune(got, '�') {
		t.Errorf("truncation split a character, producing a replacement glyph: %q", got)
	}
	if n := len([]rune(got)); n != 20 {
		t.Errorf("expected exactly 20 runes, got %d (%q)", n, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected an ellipsis on a truncated value, got %q", got)
	}
}

func TestTruncateRunesLeavesShortStringsAlone(t *testing.T) {
	for _, s := range []string{"", "short", "exactly-ten", "✅ done"} {
		if got := truncateRunes(s, 20); got != s {
			t.Errorf("expected %q to pass through unchanged, got %q", s, got)
		}
	}
}

func TestTruncateRunesBoundaries(t *testing.T) {
	// At exactly the limit, nothing is cut.
	if got := truncateRunes("abcde", 5); got != "abcde" {
		t.Errorf("a string at the limit must not be truncated, got %q", got)
	}
	// One past the limit, it is.
	if got := truncateRunes("abcdef", 5); got != "ab..." {
		t.Errorf("expected \"ab...\", got %q", got)
	}
	// Degenerate limits must not panic or produce a longer string than asked for.
	if got := truncateRunes("abcdef", 3); len([]rune(got)) != 3 {
		t.Errorf("expected 3 runes for a limit of 3, got %q", got)
	}
	if got := truncateRunes("abcdef", 0); got != "" {
		t.Errorf("expected an empty result for a zero limit, got %q", got)
	}
	if got := truncateRunes("abcdef", -1); got != "" {
		t.Errorf("expected an empty result for a negative limit, got %q", got)
	}
}

// TestInspectorPortAccessor covers the value the TUI now displays instead of a literal
// 4040, which was wrong whenever the requested port was taken or -inspector-port was used.
func TestInspectorPortAccessor(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	if got := engine.InspectorPort(); got != 0 {
		t.Errorf("expected 0 before the Inspector starts, got %d", got)
	}
	engine.SetInspectorPort(4042)
	if got := engine.InspectorPort(); got != 4042 {
		t.Errorf("expected the recorded port 4042, got %d", got)
	}
}
