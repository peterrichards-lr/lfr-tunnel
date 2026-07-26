package client

import (
	"errors"
	"reflect"
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
				Host:  "localhost",
				Ports: []int{8080},
				Type:  "Docker (LDM)",
			},
			expectedError: false,
		},
		{
			name:       "Native Liferay DXP container running",
			mockOutput: `liferay-dxp-custom||liferay/dxp:7.4||0.0.0.0:443->443/tcp, 0.0.0.0:8080->8080/tcp`,
			mockError:  nil,
			expectedResult: &AutoDiscoverResult{
				Host:  "localhost",
				Ports: []int{443, 8080},
				Type:  "Docker (Liferay)",
			},
			expectedError: false,
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
