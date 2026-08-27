package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The committed example config is only useful if it is COMPLETE (#1452).
//
// An incomplete example is worse than no example: it looks authoritative, so a setting missing
// from it reads as a setting that does not exist. That is the shape of drift this repo keeps
// hitting -- central's config, the edge nginx config and the edge registry were all "documented"
// by something that had quietly stopped matching the running system.
//
// So completeness is enforced rather than intended. Add a field to ServerConfig without adding it
// to the example and this fails.

const exampleConfigPath = "../../resources/server/server-config.example.yaml"

// TestExampleConfigCoversEveryField is the guard.
func TestExampleConfigCoversEveryField(t *testing.T) {
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", exampleConfigPath, err)
	}

	var documented map[string]interface{}
	if err := yaml.Unmarshal(data, &documented); err != nil {
		t.Fatalf("the example config does not parse as YAML: %v", err)
	}

	var missing []string
	typ := reflect.TypeOf(ServerConfig{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		if _, ok := documented[name]; !ok {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		t.Errorf(
			"%d setting(s) exist in ServerConfig but not in %s:\n  %s\n\n"+
				"Add them with a placeholder value. An example that omits a setting is worse than no "+
				"example, because it reads as authoritative -- which is the drift #1452 is about.",
			len(missing), exampleConfigPath, strings.Join(missing, "\n  "))
	}
}

// TestExampleConfigHasNoUnknownKeys is the other direction: a key here that the struct does not
// accept is silently ignored by yaml.v3, so an operator could copy it and believe it applied.
func TestExampleConfigHasNoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", exampleConfigPath, err)
	}
	var documented map[string]interface{}
	if err := yaml.Unmarshal(data, &documented); err != nil {
		t.Fatalf("the example config does not parse as YAML: %v", err)
	}

	known := map[string]bool{}
	typ := reflect.TypeOf(ServerConfig{})
	for i := 0; i < typ.NumField(); i++ {
		if tag := typ.Field(i).Tag.Get("yaml"); tag != "" && tag != "-" {
			known[strings.Split(tag, ",")[0]] = true
		}
	}

	var unknown []string
	for k := range documented {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		t.Errorf("the example documents %d key(s) ServerConfig does not accept: %s\n"+
			"yaml.v3 ignores unknown keys silently, so an operator copying these would believe "+
			"they had applied a setting that does nothing", len(unknown), strings.Join(unknown, ", "))
	}
}

// TestExampleConfigCarriesNoSecrets — this file is committed to a public repository. Placeholder
// values only; anything that could be a credential must be empty.
func TestExampleConfigCarriesNoSecrets(t *testing.T) {
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", exampleConfigPath, err)
	}
	text := string(data)

	// A 64-char hex run is a SHA-256, which is what a real edge token_hash looks like.
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, word := range strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == '"' || r == ' ' || r == ':' || r == ','
		}) {
			if len(word) == 64 && isHex(word) {
				t.Errorf("line looks like it carries a real SHA-256 (a token_hash): %q", trimmed)
			}
		}
	}

	// The live deployment's own domains must not be baked in: this is a template, not a record
	// of one gateway.
	for _, leak := range []string{"lfr-demo.se", "lfr-demo.online", "liferay.com"} {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, leak) {
				t.Errorf("the example names a real deployment domain %q on a value line: %q", leak, line)
			}
		}
	}
}

func isHex(s string) bool {
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'f'
		isUpper := r >= 'A' && r <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}

// TestExampleConfigLoads checks the loader accepts it, not merely that it is valid YAML.
//
// A template the real loader rejects is worse than no template: an operator copies it, edits two
// values, and discovers at restart that it never could have started. LoadServerConfig applies
// defaults and validation that a bare yaml.Unmarshal does not.
func TestExampleConfigLoads(t *testing.T) {
	cfg, err := LoadServerConfig(exampleConfigPath)
	if err != nil {
		t.Fatalf("the committed example does not load: %v", err)
	}
	if cfg == nil {
		t.Fatal("loaded a nil config")
	}
	// Sanity that it loaded the file rather than falling back to defaults for everything.
	if len(cfg.Domains) == 0 {
		t.Error("expected the example's domains to be loaded")
	}
}
