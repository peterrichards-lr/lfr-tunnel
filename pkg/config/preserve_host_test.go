package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preserve_host is three-state (#2560): an absent key must stay distinguishable from an explicit
// false, or `preserve_host: false` cannot switch off Liferay detection.
func TestLoadClientConfig_PreserveHostIsThreeState(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{name: "absent", yaml: "subdomain: demo\n", want: "unset"},
		{name: "empty value", yaml: "preserve_host:\n", want: "unset"},
		{name: "explicit false", yaml: "preserve_host: false\n", want: "false"},
		{name: "explicit true", yaml: "preserve_host: true\n", want: "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadClientConfig(path)
			if err != nil {
				t.Fatalf("LoadClientConfig: %v", err)
			}
			if got := describeTriState(cfg.PreserveHost); got != tc.want {
				t.Errorf("preserve_host from %q = %s, want %s", tc.yaml, got, tc.want)
			}
		})
	}
}

// A save must not turn "unset" into an explicit false. Every tray and Inspector save wrote
// `preserve_host: false` before #2560, which would now switch detection off for anyone who
// saved any other setting.
func TestSaveClientConfig_DoesNotWriteAnUnsetPreserveHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := SaveClientConfig(path, &ClientConfig{Subdomain: "demo"}); err != nil {
		t.Fatalf("SaveClientConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "preserve_host") {
		t.Errorf("an unset preserve_host was written:\n%s", data)
	}

	off := false
	if err := SaveClientConfig(path, &ClientConfig{Subdomain: "demo", PreserveHost: &off}); err != nil {
		t.Fatalf("SaveClientConfig: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "preserve_host: false") {
		t.Errorf("an explicit false was dropped on save, so it would not survive a restart:\n%s", data)
	}
}

func TestFormPreserveHost(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name    string
		current *bool
		checked bool
		want    string
	}{
		{"checked from unset is explicit on", nil, true, "true"},
		{"unchecked from unset stays unset", nil, false, "unset"},
		{"unchecked from explicit on returns to unset", &on, false, "unset"},
		{"unchecked drops a stale false (it meant unset anyway)", &off, false, "unset"},
		{"checked overrides a hand-written false", &off, true, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeTriState(FormPreserveHost(tc.current, tc.checked)); got != tc.want {
				t.Errorf("FormPreserveHost(%s, %t) = %s, want %s",
					describeTriState(tc.current), tc.checked, got, tc.want)
			}
		})
	}
}

func describeTriState(v *bool) string {
	if v == nil {
		return "unset"
	}
	if *v {
		return "true"
	}
	return "false"
}
