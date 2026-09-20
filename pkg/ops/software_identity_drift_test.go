package ops

import (
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// docker_bypass_url was pinned in the live control plane's server-config.yaml, so #2082's
// correction to the binary default was silently discarded and the portal served a dead anchor
// at v1.48.40 -- while check-config reported "No drift found" throughout (#2096).

func TestTheDefectThatPromptedThis(t *testing.T) {
	live := []byte(`
docker_bypass_url: "https://github.com/peterrichards-lr/lfr-tunnel/blob/master/docs/liferay-se-guide.md#using-the-docker-wrapper-edr-bypass"
`)
	findings, err := CheckSoftwareIdentityDrift(live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("a config pinning the pre-#2082 anchor produced %d finding(s), want 1.\n"+
			"This is the exact state that served a dead link to every user of the portal", len(findings))
	}
	if !strings.Contains(findings[0], "docker_bypass_url") {
		t.Errorf("the finding does not name the key: %s", findings[0])
	}
	if !strings.Contains(findings[0], config.DefaultDockerBypassURL) {
		t.Error("the finding does not say what the default is, so the operator cannot tell " +
			"whether their override is deliberate or stale")
	}
}

// Pinning the CURRENT default is reported too, and this is the correction that matters.
//
// My first version stayed silent here, on the grounds that a redundant pin is harmless. It is
// harmless today and it is exactly how this defect was born: docker_bypass_url was pinned to
// the then-current default, the default was later corrected by #2082, and the pin silently won.
// A redundant pin is a stale pin that has not happened yet, so the line has to come out.
func TestPinningTheCurrentDefaultIsAlsoReported(t *testing.T) {
	live := []byte("docker_bypass_url: \"" + config.DefaultDockerBypassURL + "\"\n")

	findings, err := CheckSoftwareIdentityDrift(live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("pinning the current default produced %d finding(s), want 1", len(findings))
	}
	if !strings.Contains(findings[0], "already supplies") {
		t.Errorf("the finding reads like stale drift rather than a redundant pin: %s", findings[0])
	}
}

func TestAConfigThatSetsNoneOfThemIsSilent(t *testing.T) {
	live := []byte("domains:\n  - example.com\nbind_addr: \":443\"\n")

	findings, err := CheckSoftwareIdentityDrift(live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a config setting none of the identity keys produced findings: %v", findings)
	}
}

// An empty value is not an override -- it falls back to the default at load time, so reporting
// it would be reporting the absence of a setting.
func TestAnEmptyValueIsNotAnOverride(t *testing.T) {
	live := []byte("docker_image: \"\"\nrepository_url: \"   \"\n")

	findings, err := CheckSoftwareIdentityDrift(live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("blank values were reported as overrides: %v", findings)
	}
}

// Every key must actually be reachable, or the list silently shrinks after a rename.
func TestEveryIdentityKeyCanFire(t *testing.T) {
	defaults := config.DefaultServerConfig()

	for _, k := range softwareIdentityKeys {
		if got := k.DefaultVal(defaults); got == "" {
			t.Errorf("%s has an empty default, so nothing it is set to can ever differ from it "+
				"and the key is unreachable in this check", k.Key)
			continue
		}
		live := []byte(k.Key + ": \"something-else-entirely\"\n")
		findings, err := CheckSoftwareIdentityDrift(live)
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", k.Key, err)
		}
		if len(findings) != 1 {
			t.Errorf("%s pinned to a different value produced %d finding(s), want 1 -- the key "+
				"is listed but cannot fire", k.Key, len(findings))
		}
	}
}
