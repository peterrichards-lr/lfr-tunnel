package server

import (
	"errors"
	"net/http"
	"testing"
)

func TestUnsubscribe_HtmlEscape(t *testing.T) {
	escaped := htmlEscape("<script>")
	if escaped != "&lt;script&gt;" {
		t.Errorf("expected &lt;script&gt;, got %s", escaped)
	}
}

func TestAPIErrors_MapErrorToStatusCode(t *testing.T) {
	err := errors.New("something went wrong")
	code := mapErrorToStatusCode(err)
	if code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", code)
	}
}

func TestSSO_GenerateRandomState(t *testing.T) {
	state := generateRandomState()
	if len(state) == 0 {
		t.Errorf("expected non-empty state")
	}
}
