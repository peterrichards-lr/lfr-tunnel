package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// runDoctor is `lfr-tunnel doctor` (#2561): read-only checks of the token, the install path, and
// whether a Liferay target is set up to be tunnelled. The checks themselves live in
// pkg/client/doctor.go; this resolves their inputs the way a tunnel start would.
//
// args are the words after `doctor`. Go's flag package stops at the subcommand, so its flags are
// parsed here, on a set of their own.
func runDoctor(cfg *config.ClientConfig, sub string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(out)
	ports := fs.String("ports", "", "The Liferay port to check (the first, if several are given)")
	target := fs.String("target-host", "", "Host the Liferay port is reached on (default: as a tunnel start would resolve it)")
	subFlag := fs.String("subdomain", "", "Tunnel subdomain, used to work out the public hostname")
	hostname := fs.String("hostname", "", "Full public hostname to check Liferay accepts, e.g. dev-tomcat.lfr-demo.se")
	preserve := fs.Bool(flagPreserveHost, false, "As for a tunnel start: true or false beats Liferay detection")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(out, "[Error] unknown argument %q to doctor\n", fs.Arg(0))
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == flagPreserveHost {
			applyPreserveHostFlag(cfg, *preserve)
		}
	})
	if *target != "" {
		cfg.TargetHost = *target
	}
	if *subFlag != "" {
		sub = strings.ToLower(strings.TrimSpace(*subFlag))
	}

	in := client.DoctorInput{
		ServerURL:   cfg.ServerURL,
		Token:       cfg.AuthToken,
		TokenSource: tokenSourceOrNone(cfg.TokenSource),
	}

	if *ports != "" {
		list, err := config.ParsePortList("-ports", *ports)
		if err != nil {
			fmt.Fprintf(out, "[Error] %v\n", err)
			return 2
		}
		// Named by the user as the Liferay port: doctor is asked about Liferay, so this is a
		// statement, not a guess. A tunnel start with -ports does not assume it.
		facts.liferayPort = list[0]
		in.LiferaySource = "given with -ports"
	} else if _, err := resolvePortsAndMappings(cfg); err != nil {
		in.LiferayUnknown = fmt.Sprintf("Finding it failed: %v", err)
	} else if facts.liferayPort == 0 && len(cfg.Ports) > 0 {
		in.LiferayUnknown = "The configured ports are not identified as Liferay; pass -ports <the Liferay port>."
	}

	// What a tunnel start would do. Detection does not run for named ports, so a port named
	// with -ports is planned as undetected even though doctor treats it as Liferay.
	detected := facts.liferayPort
	if *ports != "" {
		detected = 0
	}
	plan := planPreserveHost(cfg.PreserveHost, preserveHostSource(cfg), detected)
	in.LiferayPort = facts.liferayPort
	in.TargetHost = cfg.TargetHost
	in.PreserveHostSummary = plan.summary
	in.PreserveHostOn = plan.all || containsInt(plan.ports, facts.liferayPort)
	if *ports != "" && cfg.PreserveHost == nil {
		in.PreserveHostSummary = "off (ports given explicitly, so no Liferay port is detected)"
	}

	switch {
	case *hostname != "":
		in.PublicHost = *hostname
	case cfg.CustomDomain != "":
		in.PublicHost = cfg.CustomDomain
	default:
		if domain := gatewayParentDomain(cfg.ServerURL); domain != "" && sub != "" {
			in.PublicHost = sub + "." + domain
			in.PublicHostNote = " (guessed from the subdomain and the gateway's domain; pass -hostname to check another)"
		}
	}

	return client.WriteDoctorReport(out, client.RunDoctorChecks(in))
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// gatewayParentDomain guesses the tunnel domain from the gateway's address: the gateway's own
// hostname with its first label removed (eu.lfr-demo.se -> lfr-demo.se), or as it is when it has
// only two. "" for an IP, localhost, or an address that does not parse -- there is nothing to
// guess from, and doctor then says the hostname is unknown rather than inventing one.
func gatewayParentDomain(serverURL string) string {
	if serverURL == "" {
		return ""
	}
	if !strings.Contains(serverURL, "://") {
		serverURL = "https://" + serverURL
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return ""
	}
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	return strings.Join(labels[1:], ".")
}
