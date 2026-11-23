package client

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// `doctor` (#2561). The property every case below holds: a check that could not run reports
// UNKNOWN, never PASS -- and the report's exit status does not treat UNKNOWN as success.

func TestDoctorToken_UnreachableGatewayIsUnknown(t *testing.T) {
	// The real checkLoginToken, against a port nothing listens on.
	c := doctorTokenCheck(DoctorInput{ServerURL: "http://127.0.0.1:1", Token: "abc", TokenSource: "token file (~/.lfr-tunnel/token)"})
	if c.Status != DoctorUnknown {
		t.Fatalf("unreachable gateway: status %s, want UNKNOWN; detail %v", c.Status, c.Detail)
	}
	if !strings.Contains(strings.Join(c.Detail, " "), "token file (~/.lfr-tunnel/token)") {
		t.Errorf("the token's source is not reported: %v", c.Detail)
	}
}

func TestDoctorToken_Outcomes(t *testing.T) {
	old := verifyLoginToken
	t.Cleanup(func() { verifyLoginToken = old })

	cases := []struct {
		name  string
		token string
		check loginTokenCheck
		want  DoctorStatus
	}{
		{"accepted", "abc", loginTokenVerified, DoctorPass},
		{"refused", "abc", loginTokenRefused, DoctorFail},
		{"gateway cannot say", "abc", loginTokenUnknown, DoctorUnknown},
		{"no token anywhere", "", loginTokenVerified, DoctorFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verifyLoginToken = func(string, string) (loginTokenCheck, string) { return tc.check, "stub reason" }
			c := doctorTokenCheck(DoctorInput{ServerURL: "https://gw.example", Token: tc.token, TokenSource: "x"})
			if c.Status != tc.want {
				t.Errorf("status %s, want %s (%v)", c.Status, tc.want, c.Detail)
			}
		})
	}
}

// stubInstall points the install-path check at a fake home and executable. Neither path exists,
// so EvalSymlinks leaves them as given (a real temp dir on macOS resolves /var to /private/var).
func stubInstall(t *testing.T, exe string, serverDir string, serverErr error) string {
	t.Helper()
	home := filepath.Join(string(filepath.Separator), "nonexistent-home-for-doctor-test")
	oldExe, oldHome, oldDir := doctorExecutable, doctorUserHomeDir, doctorServerInstallDir
	t.Cleanup(func() { doctorExecutable, doctorUserHomeDir, doctorServerInstallDir = oldExe, oldHome, oldDir })
	doctorExecutable = func() (string, error) { return strings.ReplaceAll(exe, "$HOME", home), nil }
	doctorUserHomeDir = func() (string, error) { return home, nil }
	doctorServerInstallDir = func(string) (string, error) { return serverDir, serverErr }
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	return home
}

func TestDoctorInstallPath(t *testing.T) {
	canonical := filepath.Join("$HOME", "liferay", "lfr-tunnel", canonicalExeName())
	cases := []struct {
		name      string
		exe       string
		serverDir string
		serverErr error
		want      DoctorStatus
	}{
		{"canonical, gateway has no opinion", canonical, "", nil, DoctorPass},
		{"canonical, gateway unreachable", canonical, "", errors.New("connection refused"), DoctorUnknown},
		{"elsewhere", filepath.Join("$HOME", "Downloads", canonicalExeName()), "", nil, DoctorFail},
		{"elsewhere, gateway unreachable", filepath.Join("$HOME", "Downloads", canonicalExeName()), "", errors.New("timeout"), DoctorUnknown},
		{"gateway moved it, binary not moved", canonical, "~/tools/lfr", nil, DoctorFail},
		{"gateway moved it, binary moved", filepath.Join("$HOME", "tools", "lfr", canonicalExeName()), "~/tools/lfr", nil, DoctorPass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubInstall(t, tc.exe, tc.serverDir, tc.serverErr)
			c := doctorInstallPathCheck(DoctorInput{ServerURL: "https://gw.example"})
			if c.Status != tc.want {
				t.Errorf("status %s, want %s\n%s", c.Status, tc.want, strings.Join(c.Detail, "\n"))
			}
			joined := strings.Join(c.Detail, " ")
			if !strings.Contains(joined, "docs/infosec.md") || !strings.Contains(joined, "cannot be seen from here") {
				t.Errorf("the check must point at infosec.md and not claim the exclusion: %v", c.Detail)
			}
		})
	}
}

func TestDoctorInstallPath_ReportsAnOverrideVariable(t *testing.T) {
	stubInstall(t, filepath.Join("$HOME", "liferay", "lfr-tunnel", canonicalExeName()), "", nil)
	t.Setenv("LFT_INSTALL_DIR", "/opt/elsewhere")
	c := doctorInstallPathCheck(DoctorInput{})
	if !strings.Contains(strings.Join(c.Detail, " "), `LFT_INSTALL_DIR is set to "/opt/elsewhere"`) {
		t.Errorf("a set install-dir override is not reported: %v", c.Detail)
	}
}

func TestDoctorLiferayChecks_UnknownWithoutATarget(t *testing.T) {
	in := DoctorInput{LiferayUnknown: "No Liferay was found."}
	for _, c := range []DoctorCheck{doctorPreserveHostCheck(in), doctorHostAcceptanceCheck(in)} {
		if c.Status != DoctorUnknown {
			t.Errorf("%s with no Liferay port: %s, want UNKNOWN", c.Name, c.Status)
		}
	}
	in = DoctorInput{LiferayPort: 8080}
	if c := doctorHostAcceptanceCheck(in); c.Status != DoctorUnknown {
		t.Errorf("host acceptance with no hostname: %s, want UNKNOWN", c.Status)
	}
}

func TestDoctorHostAcceptance(t *testing.T) {
	cases := []struct {
		name      string
		refuseAll bool
		refuse    []string
		want      DoctorStatus
		wantText  string
	}{
		{"refuses the tunnel hostname", false, []string{rejectedPublicHost}, DoctorFail, "virtual.hosts.valid.hosts"},
		{"serves it", false, nil, DoctorPass, "Answered 200"},
		{"fails for every host", true, nil, DoctorUnknown, "also got a 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newHostBackend(t, tc.refuseAll, tc.refuse...)
			c := doctorHostAcceptanceCheck(DoctorInput{TargetHost: "127.0.0.1", LiferayPort: b.port, PublicHost: rejectedPublicHost})
			if c.Status != tc.want {
				t.Errorf("status %s, want %s\n%s", c.Status, tc.want, strings.Join(c.Detail, "\n"))
			}
			if !strings.Contains(strings.Join(c.Detail, " "), tc.wantText) {
				t.Errorf("detail does not contain %q: %v", tc.wantText, c.Detail)
			}
		})
	}
}

func TestDoctorPreserveHost(t *testing.T) {
	if c := doctorPreserveHostCheck(DoctorInput{LiferayPort: 8080, PreserveHostOn: true, PreserveHostSummary: "on for 8080 (Liferay detected)"}); c.Status != DoctorPass {
		t.Errorf("on for the Liferay port: %s, want PASS", c.Status)
	}
	if c := doctorPreserveHostCheck(DoctorInput{LiferayPort: 8080, PreserveHostSummary: "off (-preserve-host=false)"}); c.Status != DoctorFail {
		t.Errorf("off for the Liferay port: %s, want FAIL", c.Status)
	}
}

func TestWriteDoctorReport_UnknownIsNotSuccess(t *testing.T) {
	cases := []struct {
		name     string
		statuses []DoctorStatus
		want     int
	}{
		{"all pass", []DoctorStatus{DoctorPass, DoctorPass}, 0},
		{"one unknown", []DoctorStatus{DoctorPass, DoctorUnknown}, 2},
		{"a fail outranks an unknown", []DoctorStatus{DoctorUnknown, DoctorFail}, 1},
		{"an unrecognised status is not a pass", []DoctorStatus{DoctorPass, DoctorStatus("SKIPPED")}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var checks []DoctorCheck
			for i, s := range tc.statuses {
				checks = append(checks, DoctorCheck{Name: string(rune('a' + i)), Status: s})
			}
			var out bytes.Buffer
			if got := WriteDoctorReport(&out, checks); got != tc.want {
				t.Errorf("exit %d, want %d\n%s", got, tc.want, out.String())
			}
		})
	}
	var out bytes.Buffer
	WriteDoctorReport(&out, []DoctorCheck{{Name: "Token", Status: DoctorUnknown, Detail: []string{"Not checked."}}})
	if !strings.Contains(out.String(), "[UNKNOWN] Token") || !strings.Contains(out.String(), "0 passed, 0 failed, 1 unknown") {
		t.Errorf("report:\n%s", out.String())
	}
}
