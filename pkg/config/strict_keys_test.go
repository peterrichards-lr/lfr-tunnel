package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A server config with an unknown key must not start silently.
//
// The failure being removed: a mistyped key is skipped, the gateway starts, the feature the key
// was meant to switch on stays off, and nothing anywhere says why. Indistinguishable from a key
// that took effect -- the same shape as a check that cannot fail.
//
// The CONTROL cases are the important half here. Strict decoding is a change that can break
// production on restart, so "a real config still loads" has to be proven, not assumed.

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server-config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// FIRING. The defect.
func TestAnUnknownServerKeyIsRefusedAndNamed(t *testing.T) {
	path := writeCfg(t, "domains:\n  - example.com\ncountry_db_provder: \"dbip\"\n")

	_, err := LoadServerConfig(path)
	if err == nil {
		t.Fatal("a mistyped key was accepted; the gateway would start with it silently ignored")
	}
	// Naming the key is the whole value: an operator who cannot see WHICH key is wrong is no
	// better off than one who was told nothing.
	if !strings.Contains(err.Error(), "country_db_provder") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
	// And it must say how to proceed, including the rollback case.
	if !strings.Contains(err.Error(), allowUnknownKeysEnv) {
		t.Errorf("the error does not name the escape hatch for a rollback: %v", err)
	}
}

// CONTROL. Without this every case here would pass on a loader that rejects everything.
func TestAValidServerConfigStillLoads(t *testing.T) {
	path := writeCfg(t, "domains:\n  - example.com\ncountry_db_path: \"/etc/lfr-tunneld/geoip/country.mmdb\"\n")

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("CONTROL: a valid config was refused (%v); the cases above prove nothing", err)
	}
	if cfg.CountryDBPath != "/etc/lfr-tunneld/geoip/country.mmdb" {
		t.Errorf("the config parsed but did not apply: CountryDBPath=%q", cfg.CountryDBPath)
	}
}

// CONTROL, and the one that would have taken production down.
//
// KnownFields rejects unknown STRUCT fields. Map keys are DATA. Central's live config carries
// `client_platforms` (map[string]PlatformConfig) with keys like linux_amd64, and `role_settings`
// with `admin` -- none of which are fields. If strict decoding rejected those, every gateway
// would refuse to start on its existing config.
func TestMapKeysAreDataAndStillParse(t *testing.T) {
	path := writeCfg(t, `domains:
  - example.com
client_platforms:
  linux_amd64:
    url: "https://example.com/lfr-tunnel-linux-amd64"
  macos_arm64:
    url: "https://example.com/lfr-tunnel-darwin-arm64"
role_settings:
  admin:
    max_reservations: 5
`)

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("a real-shaped config with map keys was refused: %v\n\n"+
			"This is the shape every live gateway already has. Rejecting it would stop them "+
			"starting on their existing configuration.", err)
	}
	if _, ok := cfg.ClientPlatforms["linux_amd64"]; !ok {
		t.Error("client_platforms parsed but lost its entries")
	}
}

// BOUNDING. The rollback hatch: deliberate, named, and it still works.
func TestTheRollbackHatchAllowsAnUnknownKey(t *testing.T) {
	path := writeCfg(t, "domains:\n  - example.com\na_key_from_a_newer_gateway: true\n")

	if _, err := LoadServerConfig(path); err == nil {
		t.Fatal("PREMISE: the key was accepted without the hatch, so this case proves nothing")
	}

	t.Setenv(allowUnknownKeysEnv, "true")
	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("the rollback hatch did not allow an unknown key: %v", err)
	}
	if len(cfg.Domains) != 1 || cfg.Domains[0] != "example.com" {
		t.Error("the hatch allowed the key but the rest of the config was not applied")
	}
}

// The client must STAY lenient. Guarding the decision, not just the code: someone tidying up
// would otherwise make both loaders match and break every client carrying a retired key.
func TestTheClientLoaderStaysLenient(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server: \"https://example.com\"\nbypass_proxy: true\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := LoadClientConfig(p); err != nil {
		t.Fatalf("the client loader rejected a retired key (%v).\n\n"+
			"It must not. People have bypass_proxy and nav_placement in ~/.lfr-tunnel/config.yaml "+
			"from copying old examples, and a client that refuses to start cannot be fixed "+
			"remotely for a user in another timezone. Strict decoding is a SERVER-side decision.", err)
	}
}
