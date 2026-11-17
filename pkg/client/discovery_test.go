package client

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type mockExecutor struct {
	output []byte
	err    error
}

func (m mockExecutor) Output(command string, args ...string) ([]byte, error) {
	return m.output, m.err
}

func TestDiscoverDocker(t *testing.T) {
	// Save the original executor and restore it after the test
	originalExecutor := cmdExecutor
	defer func() { cmdExecutor = originalExecutor }()

	tests := []struct {
		name           string
		mockOutput     string
		mockError      error
		expectedResult *AutoDiscoverResult
		expectedError  bool
	}{
		{
			name:           "Docker command fails",
			mockOutput:     "",
			mockError:      errors.New("executable file not found in $PATH"),
			expectedResult: nil,
			expectedError:  true,
		},
		{
			name: "No Liferay or LDM containers",
			mockOutput: `postgres_db||postgres:14||0.0.0.0:5432->5432/tcp
redis_cache||redis:alpine||0.0.0.0:6379->6379/tcp`,
			mockError:      nil,
			expectedResult: nil,
			expectedError:  false,
		},
		{
			name: "LDM container running with typical port",
			mockOutput: `ldm-project-xyz||peterrichards/ldm-runtime||0.0.0.0:8080->8080/tcp
postgres_db||postgres:14||0.0.0.0:5432->5432/tcp`,
			mockError: nil,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8080},
				Type:      "Docker (LDM)",
				Container: "ldm-project-xyz",
			},
			expectedError: false,
		},
		{
			name:       "Native Liferay DXP container running",
			mockOutput: `liferay-dxp-custom||liferay/dxp:7.4||0.0.0.0:443->443/tcp, 0.0.0.0:8080->8080/tcp`,
			mockError:  nil,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{443, 8080},
				Type:      "Docker (Liferay)",
				Container: "liferay-dxp-custom",
			},
			expectedError: false,
		},
		{
			// The shape of a real LDM machine (#2462): one project, plus LDM's global Traefik
			// proxy on 80/443 and its search container. Discovery used to return [8080 80 443].
			name: "LDM infrastructure is not a candidate",
			mockOutput: `acme-portal||liferay/dxp:2025.q1.0||0.0.0.0:8080->8080/tcp
liferay-proxy-global||traefik:v3.1||0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp
liferay-search-global||elasticsearch:8.17.0||0.0.0.0:9200->9200/tcp
postgres_db||postgres:14||0.0.0.0:5432->5432/tcp`,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8080},
				Type:      "Docker (Liferay)",
				Container: "acme-portal",
			},
		},
		{
			name:           "only LDM infrastructure is running",
			mockOutput:     `liferay-proxy-global||traefik:v3.1||0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp`,
			expectedResult: nil,
		},
		{
			name:       "a port Docker lists for IPv4 and IPv6 (both notations) is published once",
			mockOutput: `ldm-project-xyz||peterrichards/ldm-runtime||0.0.0.0:8080->8080/tcp, [::]:8080->8080/tcp, :::8080->8080/tcp`,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8080},
				Type:      "Docker (LDM)",
				Container: "ldm-project-xyz",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdExecutor = mockExecutor{
				output: []byte(tt.mockOutput),
				err:    tt.mockError,
			}

			result, err := discoverDocker()
			if (err != nil) != tt.expectedError {
				t.Errorf("discoverDocker() error = %v, expectedError %v", err, tt.expectedError)
				return
			}
			if !reflect.DeepEqual(result, tt.expectedResult) {
				t.Errorf("discoverDocker() result = %v, expected %v", result, tt.expectedResult)
			}
		})
	}
}

// Two projects on one machine (#2462): publishing both shared one the user never meant to, and
// choosing one would be a guess. Discovery refuses, names both, and does not fall back to probing
// 8080 -- which is very likely one of them.
func TestDiscoveryRefusesWhenSeveralContainersQualify(t *testing.T) {
	originalExecutor := cmdExecutor
	defer func() { cmdExecutor = originalExecutor }()
	cmdExecutor = mockExecutor{output: []byte(`acme-portal||liferay/dxp:2025.q1.0||0.0.0.0:8080->8080/tcp
other-project||liferay/dxp:2024.q4.0||0.0.0.0:18080->8080/tcp, 0.0.0.0:3000->3000/tcp
liferay-proxy-global||traefik:v3.1||0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp`)}

	result, err := AutoDiscoverTarget()
	var ambiguous *AmbiguousDiscoveryError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("AutoDiscoverTarget() = %+v, %v; want an AmbiguousDiscoveryError", result, err)
	}
	if result != nil {
		t.Errorf("a refusal must publish nothing, got %+v", result)
	}
	want := []DiscoveredContainer{{Name: "acme-portal", Ports: []int{8080}}, {Name: "other-project", Ports: []int{3000}}}
	if !reflect.DeepEqual(ambiguous.Candidates, want) {
		t.Errorf("candidates = %+v, want %+v", ambiguous.Candidates, want)
	}
	for _, s := range []string{"acme-portal", "other-project", "-ports"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("the refusal %q does not mention %q", err.Error(), s)
		}
	}
}

func TestAutoDiscoverTarget(t *testing.T) {
	// We leave the integration/fallback test as-is for local state.
	result, err := AutoDiscoverTarget()
	if err != nil {
		t.Logf("Expected no error, got %v", err)
	}

	if result != nil {
		t.Logf("Discovered something: %+v", result)
	} else {
		t.Log("Nothing discovered, as expected in CI")
	}
}
