package main

import (
	"context"
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

	// Ask the port itself, as a tunnel start's watch does (#2571): the header identifies Liferay
	// for named ports and a native bundle, and withdraws the workspace-folder guess.
	probe := identifyLiferayForDoctor(cfg, *ports != "")
	if probe.identified > 0 {
		facts.liferayPort = probe.identified
		in.LiferaySource = fmt.Sprintf("identified by its Liferay-Portal header (%s)", probe.header)
		in.LiferayUnknown = ""
	} else if probe.withdrawn > 0 {
		facts.liferayPort = 0
		in.LiferayUnknown = fmt.Sprintf("Port %d answered without Liferay's Liferay-Portal header, so it is not treated as Liferay; pass -ports <the Liferay port>.", probe.withdrawn)
	}

	in.LiferayPort = facts.liferayPort
	in.TargetHost = cfg.TargetHost
	in.PreserveHostSummary, in.PreserveHostOn = doctorPreserveHost(cfg, *ports != "", probe)

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

// doctorLiferayProbe is what asking the candidate ports for Liferay's header found.
type doctorLiferayProbe struct {
	identified int    // the first port that answered with the header, or 0
	header     string // its value
	withdrawn  int    // a workspace-guessed port that answered without it, or 0
	verdict    client.LiferayIdentity
}

// identifyLiferayForDoctor asks once -- doctor reports the state now, it does not wait for a
// booting Liferay. A port named with -ports is asked too: doctor treats it as Liferay, and the
// answer says whether a tunnel start would.
func identifyLiferayForDoctor(cfg *config.ClientConfig, portsNamed bool) doctorLiferayProbe {
	var candidates []int
	switch {
	case portsNamed:
		candidates = []int{facts.liferayPort}
	case facts.liferayPort == 0 || facts.liferayWeak:
		candidates = facts.liferayCandidates
	}
	host := cfg.TargetHost
	if host == "" {
		host = "127.0.0.1"
	}
	var r doctorLiferayProbe
	for _, p := range candidates {
		verdict, header := probeLiferay(context.Background(), nil, host, p)
		if p == facts.liferayPort {
			r.verdict = verdict
		}
		if verdict == client.LiferayIdentified && r.identified == 0 {
			r.identified, r.header, r.verdict = p, header, verdict
		}
		if verdict == client.LiferayNotIdentified && facts.liferayWeak && p == facts.liferayPort && !portsNamed {
			r.withdrawn = p
		}
	}
	if r.identified > 0 {
		r.withdrawn = 0
	}
	return r
}

// probeLiferay is client.ProbeLiferay, swappable in tests.
var probeLiferay = client.ProbeLiferay

// doctorPreserveHost is what a tunnel start would do with preserve_host for the Liferay port.
// Since #2571 a tunnel start identifies a port by its Liferay-Portal header, named ports
// included, so the plan follows what the port answered rather than how it was chosen.
func doctorPreserveHost(cfg *config.ClientConfig, portsNamed bool, probe doctorLiferayProbe) (summary string, on bool) {
	detected := facts.liferayPort
	if portsNamed && probe.identified == 0 {
		detected = 0 // named, but not (yet) answering as Liferay: a tunnel start would not identify it now
	}
	inputs := preserveHostInputsFor(cfg)
	plan := planPreserveHost(inputs, detected)
	summary = plan.summary
	if probe.identified > 0 {
		summary = strings.Replace(summary, "(Liferay detected)", "(Liferay identified by its Liferay-Portal header)", 1)
	}
	if detected == 0 && facts.liferayPort > 0 && inputs.flag == "" && !inputs.fileOn && !inputs.autoDisable {
		switch probe.verdict {
		case client.LiferayNotIdentified:
			summary = fmt.Sprintf("off (port %d answered without Liferay's Liferay-Portal header; pass -preserve-host if it is Liferay with the header switched off)", facts.liferayPort)
		default:
			summary = fmt.Sprintf("off until Liferay answers on %d: a tunnel start watches for its Liferay-Portal header and turns it on then", facts.liferayPort)
		}
	}
	return summary, plan.all || containsInt(plan.ports, facts.liferayPort)
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
