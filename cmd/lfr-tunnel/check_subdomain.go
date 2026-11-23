package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// `lfr-tunnel check-subdomain <name>` (#2562).
//
// The documented way to check a name was a curl with the token pasted on the command line, after
// the same guides told users to keep it in ~/.lfr-tunnel/token. This asks the gateway with the
// token the client already resolves -- the same ladder a tunnel uses -- for every domain the
// gateway issues tunnels on.
//
// Exit status, so a script can branch on it:
//
//	0  every domain answered, and the name is available on at least one
//	1  every domain answered, and the name is available on none
//	2  anything else: a usage error, no gateway or token configured, the token refused, the
//	   gateway unreachable, or any one domain that could not be checked
//
// 2 wins over 0 on purpose. A domain that could not be checked is unknown, and printing
// "available" for it -- or exiting as if every answer were in -- would report unknown as absent.

const (
	checkSubdomainCommand = "check-subdomain"

	checkSubdomainAvailable = 0
	checkSubdomainTaken     = 1
	checkSubdomainError     = 2
)

// lineWriter keeps the first write error instead of dropping it, so a caller can print a run of
// lines and check once. errcheck runs with check-blank here, so `_, _ =` would not do.
type lineWriter struct {
	w   io.Writer
	err error
}

func (l *lineWriter) printf(format string, a ...any) {
	if l.err == nil {
		_, l.err = fmt.Fprintf(l.w, format, a...)
	}
}

// domainResult is one row of output.
type domainResult struct {
	label       string
	status      string
	available   bool
	unknown     bool
	suggestions string
}

// runCheckSubdomain runs the subcommand. args are the words after `check-subdomain`; cfg is the
// configuration main() already resolved, which is where the token and gateway come from.
func runCheckSubdomain(args []string, cfg *config.ClientConfig, stdout, stderr io.Writer) int {
	errOut := &lineWriter{w: stderr}
	name, cfg, ok := parseCheckSubdomainArgs(args, cfg, errOut)
	if !ok {
		return checkSubdomainError
	}

	if cfg.ServerURL == "" {
		errOut.printf("[Error] No gateway is configured, so there is nothing to ask. Set server_url in the config file, or LFT_SERVER_URL.\n")
		return checkSubdomainError
	}
	if cfg.AuthToken == "" {
		errOut.printf("[Error] No access token was found, so the gateway was not asked.\n")
		errOut.printf("[Client] Sign in to get one:\n         👉 %s\n", loginCommand)
		return checkSubdomainError
	}

	domains, err := client.GatewayTunnelDomains(cfg.ServerURL)
	if err != nil {
		errOut.printf("[Error] %v\n", err)
		return checkSubdomainError
	}
	if len(domains) == 0 {
		// An older gateway does not list its domains. Ask once and let it choose.
		domains = []string{""}
	}

	var results []domainResult
	legacy := false
	for _, domain := range domains {
		result, fatal, isLegacy := checkOneDomain(cfg, name, domain, errOut)
		if fatal {
			return checkSubdomainError
		}
		results = append(results, result)
		if isLegacy {
			// The gateway ignored the domain it was asked about. Asking about the others would
			// only repeat its answer under different labels.
			legacy = true
			break
		}
	}
	return renderCheckSubdomain(results, legacy, &lineWriter{w: stdout}, errOut)
}

// parseCheckSubdomainArgs reads the subcommand's own flags and the name. Flags may come before or
// after the name: the flag package stops at the first non-flag, so it parses once, takes the
// name, and parses what follows it.
func parseCheckSubdomainArgs(args []string, cfg *config.ClientConfig, errOut *lineWriter) (string, *config.ClientConfig, bool) {
	fs := flag.NewFlagSet(checkSubdomainCommand, flag.ContinueOnError)
	fs.SetOutput(errOut.w)
	configPath := fs.String("config", "", "Path to a client config file, instead of the default")
	tokenFlag := fs.String("token", "", "Access token to use, instead of the one the client resolves")
	fs.Usage = func() {
		errOut.printf("Usage: lfr-tunnel %s [-config <path>] [-token <token>] <subdomain>\n\n", checkSubdomainCommand)
		errOut.printf("Checks <subdomain> on every domain the gateway issues tunnels on, with the token\n")
		errOut.printf("the client already uses. Exit 0: available on at least one; 1: available on none;\n")
		errOut.printf("2: an error, or a domain that could not be checked.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return "", nil, false
	}
	positional := fs.Args()
	if len(positional) > 0 {
		if err := fs.Parse(positional[1:]); err != nil {
			return "", nil, false
		}
		positional = append([]string{positional[0]}, fs.Args()...)
	}
	name := ""
	if len(positional) == 1 {
		name = strings.ToLower(strings.TrimSpace(positional[0]))
	}
	if name == "" {
		fs.Usage()
		return "", nil, false
	}

	if *configPath != "" {
		loaded, err := config.LoadClientConfig(*configPath)
		if err != nil {
			errOut.printf("[Error] Failed to load configuration: %v\n", err)
			return "", nil, false
		}
		cfg = loaded
	}
	if *tokenFlag != "" {
		withFlag := *cfg
		withFlag.AuthToken = *tokenFlag
		withFlag.TokenSource = "-token flag"
		cfg = &withFlag
	}
	return name, cfg, true
}

// checkOneDomain asks about one domain. fatal means the whole command must stop -- the advice has
// been printed -- because no other domain could answer differently: a refused token, an edge, or
// an unreachable gateway. legacy means the gateway ignored the domain.
func checkOneDomain(cfg *config.ClientConfig, name, domain string, errOut *lineWriter) (domainResult, bool, bool) {
	result := domainResult{label: name}
	if domain != "" {
		result.label = name + "." + domain
	}

	check, err := client.CheckSubdomain(cfg.ServerURL, cfg.AuthToken, name, domain)
	if err != nil {
		if reportFatalCheckError(cfg, err, errOut) {
			return result, true, false
		}
		result.unknown = true
		result.status = "unknown -- " + err.Error()
		return result, false, false
	}

	legacy := false
	switch {
	case domain != "" && check.Domain == "":
		// Older than #2562: it answered for a domain of its own choosing, without saying which.
		legacy = true
		result.label = name
	case domain != "" && !strings.EqualFold(check.Domain, domain):
		result.unknown = true
		result.status = fmt.Sprintf("unknown -- the gateway answered about %s instead", check.Domain)
		return result, false, false
	}

	if check.Available {
		result.available = true
		result.status = "available"
		return result, false, legacy
	}
	result.status = "unavailable"
	if check.Reason != "" {
		result.status += " -- " + check.Reason
	}
	if len(check.Suggestions) > 0 {
		where := ""
		if check.Domain != "" {
			where = " on " + check.Domain
		}
		result.suggestions = fmt.Sprintf("Suggestions%s: %s", where, strings.Join(check.Suggestions, ", "))
	}
	return result, false, legacy
}

// reportFatalCheckError prints the advice for an error no other domain could answer differently,
// and reports whether it was one.
func reportFatalCheckError(cfg *config.ClientConfig, err error, errOut *lineWriter) bool {
	if errors.Is(err, client.ErrTokenRefused) {
		// The same advice a refused registration prints: what the gateway's single 401 can
		// mean, where this token was read from, and what will actually replace it.
		for _, line := range unauthorizedFailure(cfg, err, portalURLForAdvice(cfg, "")).advice {
			errOut.printf("%s\n", strings.TrimRight(line, "\n"))
		}
		return true
	}
	var gwErr *client.GatewayError
	if errors.As(err, &gwErr) && gwErr.Status == http.StatusMisdirectedRequest {
		errOut.printf("[Error] %s is a regional edge and cannot check subdomains. Point the client at the control plane", cfg.ServerURL)
		if gwErr.ControlPlane != "" {
			errOut.printf(" (%s)", gwErr.ControlPlane)
		}
		errOut.printf(", for example with LFT_SERVER_URL.\n")
		return true
	}
	var unreachable *client.UnreachableError
	if errors.As(err, &unreachable) {
		errOut.printf("[Error] %v\n", err)
		return true
	}
	return false
}

// renderCheckSubdomain prints the rows and suggestions and decides the exit status.
func renderCheckSubdomain(results []domainResult, legacy bool, out, errOut *lineWriter) int {
	tw := tabwriter.NewWriter(out.w, 0, 0, 2, ' ', 0)
	table := &lineWriter{w: tw}
	anyAvailable, anyUnknown := false, false
	for _, r := range results {
		table.printf("%s\t%s\n", r.label, r.status)
		anyAvailable = anyAvailable || r.available
		anyUnknown = anyUnknown || r.unknown
	}
	if table.err == nil {
		table.err = tw.Flush()
	}
	for _, r := range results {
		if r.suggestions != "" {
			out.printf("%s\n", r.suggestions)
		}
	}
	if legacy {
		errOut.printf("[Note] This gateway did not say which domain it checked, so the answer above is for the domain it would give you. " +
			"A gateway that old also does not see tunnels on other regions or other users' reservations, so \"available\" from it is not final.\n")
	}
	if anyUnknown {
		errOut.printf("[Error] At least one domain could not be checked, so its answer is unknown.\n")
	}

	switch {
	case table.err != nil || out.err != nil:
		return checkSubdomainError
	case anyUnknown:
		return checkSubdomainError
	case anyAvailable:
		return checkSubdomainAvailable
	default:
		return checkSubdomainTaken
	}
}
