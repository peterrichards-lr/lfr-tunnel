package config

// These describe *this software* -- where its source, documentation and images live. They
// are the same for everyone running lfr-tunnel, so they belong in the code.
const (
	DefaultRepositoryURL       = "https://github.com/peterrichards-lr/lfr-tunnel"
	DefaultDocumentationURL    = "https://github.com/peterrichards-lr/lfr-tunnel/tree/master/docs"
	DefaultSecureTokenGuideURL = "https://github.com/peterrichards-lr/lfr-tunnel/blob/master/docs/getting_started.md#option-c-restricted-secrets-file-advanced--secure"
	DefaultDockerHubURL        = "https://hub.docker.com/r/peterjrichards/lfr-tunnel"
	DefaultDockerBypassURL     = "https://github.com/peterrichards-lr/lfr-tunnel/blob/master/docs/liferay-se-guide.md#using-the-docker-wrapper-edr-bypass"
)

// These describe *one deployment* of it -- which gateway to talk to, and where that
// operator publishes their status page and portal. They are different for everyone, so they
// carry no value in source (#1188).
//
// They used to be constants holding one organisation's production hostnames, which meant
// any self-hosted deployment that hadn't overridden them pointed its users at somebody
// else's infrastructure. That is the same defect as #1124, where a hardcoded control-plane
// URL had self-hosted clients reporting their session tokens to a third party every five
// seconds. StatusPageURL was the most visible: it is printed to the operator whenever a
// gateway looks unreachable, so somebody debugging their own outage was sent to a status
// page describing an unrelated service.
//
// Vars rather than consts so a distributor can bake their own in at build time, exactly as
// Version already is:
//
//	go build -ldflags "-X lfr-tunnel/pkg/config.DefaultServerURL=https://tunnel.example.com"
//
// The Makefile and the release workflow pass these through from the environment, so the
// values live in the build configuration of whoever cuts the release rather than in this
// MIT-licensed source tree. Empty is a supported state everywhere they are read: the client
// asks to be pointed at a gateway rather than guessing one, and the status-page and portal
// hints are omitted rather than sending anyone somewhere wrong.
var (
	// DefaultServerURL is the gateway a client talks to when nothing else says otherwise.
	// Empty means the user must supply one via -server, LFT_SERVER_URL or their config.
	DefaultServerURL = ""

	// DefaultStatusPageURL is where this deployment publishes incident status. Empty means
	// no status-page hint is offered when a gateway looks unreachable.
	DefaultStatusPageURL = ""

	// DefaultPortalURL is this deployment's user portal, used for browser-based login when
	// it cannot be derived from the gateway URL. Empty means fall back to the gateway
	// itself, which serves the portal too.
	DefaultPortalURL = ""
)
