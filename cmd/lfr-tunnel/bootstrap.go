package main

import "fmt"

// bootstrapEnvVars name a gateway to START from, exactly as -bootstrap does (#2586): the client
// fetches the region list there, picks the closest region and fails over. The long name first,
// then the short alias, like every other LFT_CLIENT_* setting.
//
// Why an environment variable at all: a container is configured through its environment (Docker
// Compose, LDM's sidecar), and the only variables that named a gateway -- LFT_CLIENT_SERVER,
// LFT_SERVER_URL, LFT_SERVER -- all PIN. The Docker image has no compiled-in default gateway, so
// every Docker client had to name one and so lost region election and failover: the route offered
// to people whose machines will not run the native client.
var bootstrapEnvVars = []string{"LFT_CLIENT_BOOTSTRAP", "LFT_BOOTSTRAP"}

// pinEnvVars are the variables that pin the client to one gateway.
var pinEnvVars = []string{"LFT_CLIENT_SERVER", "LFT_SERVER_URL", "LFT_SERVER"}

// resolveBootstrap returns the gateway to start from and what supplied it, or "" when nothing
// asked for one. The -bootstrap flag outranks the environment.
//
// A bootstrap and a pin together are refused, not resolved by precedence (as -bootstrap with -pin
// always was): they are opposite intents, and quietly honouring either one is how the original
// confusion started (#2061). Before #2586 a -bootstrap flag beside a pinning variable was silently
// pinned to the bootstrap URL.
func resolveBootstrap(flagBootstrap, flagPin string, getenv func(string) string) (url, source string, err error) {
	url, source = flagBootstrap, "-bootstrap"
	if url == "" {
		for _, name := range bootstrapEnvVars {
			if v := getenv(name); v != "" {
				url, source = v, name
				break
			}
		}
	}
	if url == "" {
		return "", "", nil
	}

	pin := ""
	if flagPin != "" {
		pin = "-pin"
	} else {
		for _, name := range pinEnvVars {
			if getenv(name) != "" {
				pin = name
				break
			}
		}
	}
	if pin != "" {
		return "", "", fmt.Errorf("%s and %s contradict each other: %s picks the closest region and fails over, "+
			"%s fixes this client to one gateway. Set whichever you meant, not both", source, pin, source, pin)
	}
	return url, source, nil
}
