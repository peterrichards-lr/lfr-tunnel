package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"lfr-tunnel/pkg/config"
)

// `lfr-tunnel doctor` (#2561): one read-only pass over the setup problems that are otherwise
// answered in four different places, each reported PASS, FAIL or UNKNOWN.
//
// UNKNOWN is a result in its own right (AGENTS.md, "Unknown is not absent"): a check that could
// not run -- the gateway was unreachable, there was no Liferay to probe -- says so, and is never
// folded into PASS.

// DoctorStatus is one check's outcome.
type DoctorStatus string

const (
	DoctorPass    DoctorStatus = "PASS"
	DoctorFail    DoctorStatus = "FAIL"
	DoctorUnknown DoctorStatus = "UNKNOWN"
)

// DoctorCheck is one line of the report, with any explanation beneath it.
type DoctorCheck struct {
	Name   string
	Status DoctorStatus
	Detail []string
}

// DoctorInput is everything the checks need, resolved by the caller the same way a tunnel start
// would resolve it.
type DoctorInput struct {
	ServerURL   string
	Token       string
	TokenSource string

	// TargetHost is the local target the Liferay port is reached on.
	TargetHost string
	// LiferayPort is the local port treated as Liferay, or 0 when none was found.
	LiferayPort int
	// LiferaySource says how LiferayPort was found ("detected in the workspace", "given with -ports").
	LiferaySource string
	// LiferayUnknown explains why there is no LiferayPort, when finding one failed.
	LiferayUnknown string

	// PublicHost is the tunnel hostname to probe Liferay with, or "" when it is not known.
	PublicHost string
	// PublicHostNote qualifies PublicHost, e.g. that it was guessed from the gateway's domain.
	PublicHostNote string

	// PreserveHostOn is whether preserve_host is in effect for LiferayPort, and PreserveHostSummary
	// is the startup-summary text explaining it.
	PreserveHostOn      bool
	PreserveHostSummary string
}

// Seams for the checks that touch the machine or the network. Production never reassigns them.
var (
	doctorExecutable       = os.Executable
	doctorUserHomeDir      = os.UserHomeDir
	doctorServerInstallDir = ServerInstallDir
	doctorProbeTransport   http.RoundTripper
)

// Literals named so goconst does not attribute their package-wide occurrences to this file.
const (
	goosDarwin              = "darwin"
	goosWindows             = "windows"
	defaultDoctorTarget     = "127.0.0.1"
	schemeHTTPS             = "https"
	doctorCheckPreserveHost = "preserve_host"
)

// infosecExclusionsDoc is where the install path and the EDR exclusion are explained.
const infosecExclusionsDoc = "docs/infosec.md, section 3 (Standard Execution Paths & EDR Exclusions)"

// RunDoctorChecks runs every check, in report order.
func RunDoctorChecks(in DoctorInput) []DoctorCheck {
	return []DoctorCheck{
		doctorTokenCheck(in),
		doctorInstallPathCheck(in),
		doctorPreserveHostCheck(in),
		doctorHostAcceptanceCheck(in),
	}
}

func doctorTokenCheck(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "Token"}
	if strings.TrimSpace(in.Token) == "" {
		// Not unknown: every source was consulted and none supplied one. That is a fact.
		c.Status = DoctorFail
		c.Detail = []string{"No token was found in -token, LFT_CLIENT_TOKEN, the config file, ~/.lfr-tunnel/token " +
			"or the LDM credentials file. Run `lfr-tunnel login`."}
		return c
	}
	c.Detail = []string{"Read from " + in.TokenSource + "."}
	check, reason := verifyLoginToken(in.ServerURL, in.Token)
	switch check {
	case loginTokenVerified:
		c.Status = DoctorPass
		c.Detail = append(c.Detail, "Accepted by "+in.ServerURL+".")
	case loginTokenRefused:
		c.Status = DoctorFail
		c.Detail = append(c.Detail, in.ServerURL+" refused it. "+refusedTokenAdvice(in.TokenSource))
	default:
		c.Status = DoctorUnknown
		c.Detail = append(c.Detail, "Not checked: "+reason+".")
	}
	return c
}

func doctorInstallPathCheck(in DoctorInput) (c DoctorCheck) {
	c.Name = "Install path"
	defer func() {
		// Always said, whatever the outcome: the client can see a path, never an exclusion.
		c.Detail = append(c.Detail, "This checks the path only. Whether an EDR exclusion is applied to it "+
			"cannot be seen from here; see "+infosecExclusionsDoc+".")
	}()

	exe, err := doctorExecutable()
	if err != nil {
		c.Status = DoctorUnknown
		c.Detail = []string{fmt.Sprintf("Could not tell where this binary is running from: %v.", err)}
		return c
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, err := doctorUserHomeDir()
	if err != nil {
		c.Status = DoctorUnknown
		c.Detail = []string{fmt.Sprintf("Could not resolve your home directory: %v.", err)}
		return c
	}
	running := filepath.Dir(exe)
	c.Detail = []string{"Running from " + exe + "."}

	for _, name := range installDirEnvVars {
		if v := os.Getenv(name); v != "" {
			c.Detail = append(c.Detail, fmt.Sprintf("%s is set to %q: installs and upgrades follow it, and an "+
				"exclusion written for the standard path does not cover it.", name, v))
		}
	}

	expected := canonicalInstallDir(home)
	expectedFrom := "the standard install directory"
	serverDir, serverErr := doctorServerInstallDir(in.ServerURL)
	if serverErr == nil && serverDir != "" {
		expected = expandInstallDir(serverDir, home)
		expectedFrom = "the gateway's install_dir for this platform"
	}

	if serverErr != nil {
		// The gateway can name another directory and could not be asked, so where the binary
		// SHOULD be is not known -- in either direction.
		c.Status = DoctorUnknown
		where := "is not"
		if samePath(running, expected) {
			where = "is"
		}
		c.Detail = append(c.Detail, fmt.Sprintf("That %s the standard directory %s, but the gateway's install_dir "+
			"could not be read (%v), and it may name another.", where, expected, serverErr))
		return c
	}
	if samePath(running, expected) {
		c.Status = DoctorPass
		c.Detail = append(c.Detail, fmt.Sprintf("That is %s (%s).", expected, expectedFrom))
		return c
	}
	c.Status = DoctorFail
	c.Detail = append(c.Detail, fmt.Sprintf("Expected %s (%s). Reinstall with the installer from your gateway, "+
		"or run `lfr-tunnel --upgrade`, to put it there.", expected, expectedFrom))
	return c
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == goosWindows || runtime.GOOS == goosDarwin {
		// Case-insensitive file systems by default on both.
		return strings.EqualFold(a, b)
	}
	return a == b
}

func doctorPreserveHostCheck(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: doctorCheckPreserveHost}
	if in.LiferayPort == 0 {
		c.Status = DoctorUnknown
		c.Detail = []string{"No Liferay port to check. " + liferayNotFound(in)}
		return c
	}
	c.Detail = []string{"Startup would report: " + in.PreserveHostSummary + "."}
	if in.PreserveHostOn {
		c.Status = DoctorPass
		return c
	}
	c.Status = DoctorFail
	c.Detail = append(c.Detail, fmt.Sprintf("Liferay on %d is sent the local address as Host, so it generates "+
		"links for that address. If the reason above is preserve_host_auto: false, remove it from the config file; if it is "+
		"-preserve-host=false, drop the flag; otherwise pass -preserve-host.", in.LiferayPort))
	return c
}

func doctorHostAcceptanceCheck(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "Liferay accepts the tunnel hostname"}
	if in.LiferayPort == 0 {
		c.Status = DoctorUnknown
		c.Detail = []string{"No Liferay port to probe. " + liferayNotFound(in)}
		return c
	}
	if in.PublicHost == "" {
		c.Status = DoctorUnknown
		c.Detail = []string{"The tunnel hostname is not known here; pass -hostname <name>.<domain>."}
		return c
	}
	target := in.TargetHost
	if target == "" {
		target = defaultDoctorTarget
	}
	scheme := "http"
	if in.LiferayPort == 443 || in.LiferayPort == 8443 {
		scheme = schemeHTTPS
	}
	base := fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(target, strconv.Itoa(in.LiferayPort)))
	rt := doctorProbeTransport
	if rt == nil {
		rt = http.DefaultTransport
	}
	result := ProbeHostAcceptance(context.Background(), rt, base, "/", in.PublicHost,
		getHostHeaderValue(target, in.LiferayPort))

	c.Detail = []string{fmt.Sprintf("GET %s/ with Host: %s%s.", base, in.PublicHost, in.PublicHostNote)}
	switch result.Verdict {
	case HostAccepted:
		c.Status = DoctorPass
		c.Detail = append(c.Detail, fmt.Sprintf("Answered %d.", result.PublicStatus))
	case HostRejected:
		c.Status = DoctorFail
		c.Detail = append(c.Detail,
			fmt.Sprintf("Answered 500, but %d for the local address: Liferay has not accepted %s.", result.LocalStatus, in.PublicHost),
			LiferayHostHint(in.PublicHost)+".")
	default:
		c.Status = DoctorUnknown
		c.Detail = append(c.Detail, "Could not tell: "+result.Detail+".")
	}
	return c
}

func liferayNotFound(in DoctorInput) string {
	if in.LiferayUnknown != "" {
		return in.LiferayUnknown
	}
	return "None was detected; pass -ports <the Liferay port>."
}

// WriteDoctorReport prints the checks and returns the exit status: 1 if anything FAILED, 2 if
// nothing failed but something is UNKNOWN, 0 only when every check passed.
func WriteDoctorReport(w io.Writer, checks []DoctorCheck) int {
	failed, unknown := 0, 0
	for _, c := range checks {
		fmt.Fprintf(w, "[%-7s] %s\n", c.Status, c.Name)
		for _, d := range c.Detail {
			fmt.Fprintf(w, "          %s\n", d)
		}
		switch c.Status {
		case DoctorFail:
			failed++
		case DoctorUnknown:
			unknown++
		case DoctorPass:
		default:
			// A status this function does not know is not a pass.
			unknown++
		}
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d unknown.\n", len(checks)-failed-unknown, failed, unknown)
	switch {
	case failed > 0:
		return 1
	case unknown > 0:
		return 2
	default:
		return 0
	}
}

// refusedTokenAdvice says how to replace a refused token from where it was actually read. Advising
// `lfr-tunnel login` for a token from LFT_CLIENT_TOKEN, the config file or -token changed nothing:
// login saves ~/.lfr-tunnel/token, which those outrank, so doctor failed the same way after the
// user followed it (#2593). The rule is the one the tunnel's own refusal uses.
func refusedTokenAdvice(source string) string {
	if config.LoginReplacesToken(source) {
		return "Create a new token in the portal, or run `lfr-tunnel login`."
	}
	return "Create a new token in the portal and put it in " + source + " in place of this one, " +
		"or remove it from there so the token `lfr-tunnel login` saves is used. " +
		"Running login alone will not help: " + source + " outranks the file it saves."
}
