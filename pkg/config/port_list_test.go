package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// malformedPortLists is every shape #2477 reproduced, plus the range and empty-part edges.
// badPart is the piece the error must name: for a list where only one part is wrong, naming
// the whole list would not tell the user which part to fix.
var malformedPortLists = []struct {
	value   string
	badPart string
}{
	{"9999:80", "9999:80"},
	{"localhost:9999", "localhost:9999"},
	{"9999 8095", "9999 8095"},
	{"9999;8095", "9999;8095"},
	{"9999,x", "x"},
	{"0", "0"},
	{"70000", "70000"},
	{"65536", "65536"},
	{"-1", "-1"},
	{"+8080", "+8080"},
	{"9999,", ""},
	{",9999", ""},
	{"9999,,8080", ""},
	{" ", ""},
}

// TestParsePortListRefusesMalformedValues is #2477: every malformed form is an error that
// names the offending part, the source, and the accepted form -- never a shorter list that
// silently drops it. Before the fix each of these parsed to whatever parts strconv.Atoi
// happened to accept, which for an all-invalid list was nothing at all, and the caller then
// fell through to discovery and finally to 8080.
func TestParsePortListRefusesMalformedValues(t *testing.T) {
	for _, tc := range malformedPortLists {
		t.Run(tc.value, func(t *testing.T) {
			ports, err := ParsePortList("-ports", tc.value)
			if err == nil {
				t.Fatalf("ParsePortList(%q) = %v, nil; want an error -- a malformed value must "+
					"never become a shorter (or empty) list that falls back to another port",
					tc.value, ports)
			}
			msg := err.Error()
			if !strings.Contains(msg, `"`+tc.badPart+`"`) {
				t.Errorf("error %q does not name the bad part %q", msg, tc.badPart)
			}
			if !strings.Contains(msg, "-ports") {
				t.Errorf("error %q does not name where the value came from (-ports)", msg)
			}
			if !strings.Contains(msg, "1-65535") {
				t.Errorf("error %q does not state the accepted form", msg)
			}
			if ports != nil {
				t.Errorf("ParsePortList(%q) returned ports %v alongside its error", tc.value, ports)
			}
		})
	}
}

// TestParsePortListAcceptsValidValues pins every currently-valid input to its current result.
func TestParsePortListAcceptsValidValues(t *testing.T) {
	cases := []struct {
		value string
		want  []int
	}{
		{"9999", []int{9999}},
		{"9999,8080", []int{9999, 8080}},
		{" 9999 , 8080 ", []int{9999, 8080}},
		{"1", []int{1}},
		{"65535", []int{65535}},
		{"8080,3000,4001", []int{8080, 3000, 4001}},
	}
	for _, tc := range cases {
		got, err := ParsePortList("-ports", tc.value)
		if err != nil {
			t.Errorf("ParsePortList(%q) refused a valid list: %v", tc.value, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParsePortList(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestLoadClientConfigRefusesMalformedPortsEnv is the LFT_CLIENT_PORTS half of #2477. It used
// to be parsed with the same drop-what-does-not-parse loop and, when nothing survived, left
// cfg.Ports as the file had it -- so a typo in the environment silently published whatever
// the file or discovery chose.
//
// The error is carried on PortsEnvErr rather than failing the load (review of #2484): failing
// the load broke -stop and -status too, so a stale variable left a running tunnel unstoppable.
// The client reports it when a tunnel is about to start.
func TestLoadClientConfigRefusesMalformedPortsEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ports:\n  - 3000\n"), 0o600); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}

	for _, tc := range malformedPortLists {
		if strings.TrimSpace(tc.value) == "" {
			continue // an empty LFT_CLIENT_PORTS is unset, not malformed
		}
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("LFT_CLIENT_PORTS", tc.value)
			cfg, loadErr := LoadClientConfig(path)
			if loadErr != nil {
				t.Fatalf("a malformed LFT_CLIENT_PORTS failed the whole load (%v); -stop and -status must still work", loadErr)
			}
			err := cfg.PortsEnvErr
			if err == nil {
				t.Fatalf("LFT_CLIENT_PORTS=%q loaded with ports %v and no error; want one naming %q",
					tc.value, cfg.Ports, tc.badPart)
			}
			if !strings.Contains(err.Error(), `"`+tc.badPart+`"`) || !strings.Contains(err.Error(), "LFT_CLIENT_PORTS") {
				t.Errorf("error %q does not name LFT_CLIENT_PORTS and the bad part %q", err, tc.badPart)
			}
		})
	}

	t.Run("valid value still overrides the file", func(t *testing.T) {
		t.Setenv("LFT_CLIENT_PORTS", " 9999 , 8080 ")
		cfg, err := LoadClientConfig(path)
		if err != nil {
			t.Fatalf("a valid LFT_CLIENT_PORTS was refused: %v", err)
		}
		if !reflect.DeepEqual(cfg.Ports, []int{9999, 8080}) {
			t.Errorf("cfg.Ports = %v, want [9999 8080]", cfg.Ports)
		}
	})
}
