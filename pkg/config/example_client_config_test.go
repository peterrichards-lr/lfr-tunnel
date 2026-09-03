package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The client-side counterpart of example_config_test.go, for the same reason (#1698).
//
// The client config file reached twenty-four settings with no example and no documentation at
// all, and one of them -- server_url -- is the only way to supply a gateway without also pinning
// the client to it. A reference that silently loses a setting is how that happens again, so
// completeness is enforced here rather than intended.

const clientExampleConfigPath = "../../resources/client/client-config.example.yaml"

// TestClientExampleConfigCoversEveryField fails if ClientConfig gains a setting the example does
// not mention.
func TestClientExampleConfigCoversEveryField(t *testing.T) {
	documented := parseClientExample(t)

	var missing []string
	typ := reflect.TypeOf(ClientConfig{})
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
			"%d setting(s) exist in ClientConfig but not in %s:\n  %s\n\n"+
				"Add them with a placeholder value, and describe them in "+
				"docs/client_configuration.md. An example that omits a setting is worse than no "+
				"example, because it reads as authoritative -- which is what #1698 is about.",
			len(missing), clientExampleConfigPath, strings.Join(missing, "\n  "))
	}
}

// TestClientExampleConfigHasNoUnknownKeys is the other direction: yaml.v3 ignores a key the
// struct does not accept, so a user copying it would believe they had set something.
func TestClientExampleConfigHasNoUnknownKeys(t *testing.T) {
	documented := parseClientExample(t)

	known := map[string]bool{}
	typ := reflect.TypeOf(ClientConfig{})
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
		t.Errorf("the example documents %d key(s) ClientConfig does not accept: %s\n"+
			"yaml.v3 ignores unknown keys silently, so a user copying these would believe "+
			"they had applied a setting that does nothing", len(unknown), strings.Join(unknown, ", "))
	}
}

// TestClientExampleConfigLoads asserts the example is not merely valid YAML but a valid config:
// it decodes into ClientConfig with the same loader a real client uses.
func TestClientExampleConfigLoads(t *testing.T) {
	cfg, err := LoadClientConfig(clientExampleConfigPath)
	if err != nil {
		t.Fatalf("the example config does not load: %v", err)
	}
	if cfg.ServerURL == "" {
		t.Error("the example should carry a placeholder server_url -- it is the setting #1698 exists for")
	}
}

// TestClientExampleConfigCarriesNoSecrets -- this file is committed to a public repository, and
// three of its keys are credentials. They must ship empty.
//
// Asserted against the file's own text rather than a loaded ClientConfig: LoadClientConfig also
// consults the environment and ~/.lfr-tunnel/token, so a developer with LFT_CLIENT_TOKEN
// exported would fail a check that read the loaded value.
func TestClientExampleConfigCarriesNoSecrets(t *testing.T) {
	documented := parseClientExample(t)
	for _, key := range []string{"auth_token", "passcode", "basic_auth"} {
		if val, _ := documented[key].(string); val != "" {
			t.Errorf("the example sets %s to %q -- credentials must ship empty", key, val)
		}
	}
}

func parseClientExample(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(clientExampleConfigPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", clientExampleConfigPath, err)
	}
	var documented map[string]interface{}
	if err := yaml.Unmarshal(data, &documented); err != nil {
		t.Fatalf("the example config does not parse as YAML: %v", err)
	}
	return documented
}
