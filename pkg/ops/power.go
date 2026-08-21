package ops

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// ensureInstanceRunning checks (via the AWS CLI) whether the EC2 instance behind host is
// currently stopped and, if so, starts it and waits until it's reachable over SSH before
// returning. The returned restore func puts the instance back into whatever power state
// it was in before this call -- callers should `defer restore()` immediately after a nil
// error, so a deploy that lands during a scheduled power-off window (e.g. edge-us/
// edge-apac's deliberately-wrong midnight-8am schedule, kept as a live test case for
// #885) doesn't leave the box running outside its schedule, whether the deploy itself
// succeeds or fails.
//
// A no-op restore (and nil error) is returned if region is empty -- this whole feature is
// opt-in via -aws-region/AWS_REGION/central.aws_region (#1050), so a deploy that never
// configures it behaves exactly as it did before this existed.
//
// Relies on the target's public IP being an Elastic IP (so it's still visible via
// `Name=ip-address` on a stopped instance) -- true for every box in this fleet as of
// #1050; a regular auto-assigned public IP is released on stop and this lookup would find
// nothing.
func ensureInstanceRunning(host, region, instanceTag string) (restore func(), err error) {
	noop := func() {}
	if region == "" {
		return noop, nil
	}

	ip, err := resolveHostIPv4(host)
	if err != nil {
		return noop, fmt.Errorf("resolving %s to an IP for AWS lookup: %w", host, err)
	}

	instanceID, state, err := describeInstanceForHost(region, ip, instanceTag)
	if err != nil {
		return noop, fmt.Errorf("looking up EC2 instance for %s (%s) in %s: %w", host, ip, region, err)
	}
	if instanceID == "" {
		return noop, fmt.Errorf("no EC2 instance found for %s (%s) in %s -- check the region is right", host, ip, region)
	}

	switch state {
	case "running":
		fmt.Printf("EC2 instance %s (%s) is already running.\n", instanceID, host)
		return noop, nil
	case "stopped":
		fmt.Printf("EC2 instance %s (%s) is stopped -- starting it for this deploy...\n", instanceID, host)
		if err := RunCommand("aws", "ec2", "start-instances", "--region", region, "--instance-ids", instanceID); err != nil {
			return noop, fmt.Errorf("starting instance %s: %w", instanceID, err)
		}
		if err := RunCommand("aws", "ec2", "wait", "instance-running", "--region", region, "--instance-ids", instanceID); err != nil {
			return noop, fmt.Errorf("waiting for instance %s to reach running: %w", instanceID, err)
		}
		if err := waitForSSH(host, 2*time.Minute); err != nil {
			return noop, fmt.Errorf("instance %s is running but SSH never came up: %w", instanceID, err)
		}
		restore = func() {
			fmt.Printf("Restoring EC2 instance %s (%s) to its previous (stopped) state...\n", instanceID, host)
			stopErr := RunCommand("aws", "ec2", "stop-instances", "--region", region, "--instance-ids", instanceID)

			// Don't take the stop command's word for it. Credentials on this account are
			// short-lived and their refresh is unreliable, so the window between starting
			// an instance and stopping it can outlive the credential -- which is exactly
			// how a node was left running overnight after a deploy to Tokyo (#1183).
			// Confirm the state actually reached.
			_, state, checkErr := describeInstanceForHost(region, ip)
			switch {
			case checkErr == nil && (state == "stopping" || state == "stopped"):
				fmt.Printf("EC2 instance %s (%s) is %s.\n", instanceID, host, state)
				return
			case stopErr == nil && checkErr != nil:
				// The stop was accepted but we cannot confirm it. Say so rather than
				// implying either outcome.
				powerRestoreFailed(instanceID, host, region,
					fmt.Sprintf("stop was accepted but its result could not be confirmed: %v", checkErr))
			case stopErr != nil:
				powerRestoreFailed(instanceID, host, region, stopErr.Error())
			default:
				powerRestoreFailed(instanceID, host, region,
					fmt.Sprintf("instance is still %q after the stop", state))
			}
		}
		return restore, nil
	default:
		return noop, fmt.Errorf("instance %s (%s) is in state %q, not running or stopped -- refusing to guess, deploy manually once it settles", instanceID, host, state)
	}
}

// PowerRestoreFailed reports whether a deploy left an instance running that it had
// started. Deploy checks this so an unattended run cannot exit 0 having stranded a node
// outside its schedule, quietly costing money (#1183).
var powerRestoreFailure string

// PowerRestoreFailure returns the description of a failed power restore, or "" if none.
func PowerRestoreFailure() string { return powerRestoreFailure }

// powerRestoreFailed records the failure and makes it loud. The previous behaviour was a
// single Printf at the tail of a long, noisy deploy, which affected nothing and was easy
// to miss entirely.
func powerRestoreFailed(instanceID, host, region, reason string) {
	powerRestoreFailure = fmt.Sprintf("instance %s (%s) may still be RUNNING: %s", instanceID, host, reason)
	rule := strings.Repeat("!", 78)
	fmt.Fprintf(os.Stderr, "\n%s\n", rule)
	fmt.Fprintf(os.Stderr, "WARNING: %s\n", powerRestoreFailure)
	fmt.Fprint(os.Stderr, "It was started for this deploy and must be stopped, or it runs outside its\n")
	fmt.Fprint(os.Stderr, "schedule and keeps costing money. Stop it with:\n\n")
	fmt.Fprintf(os.Stderr, "  aws ec2 stop-instances --region %s --instance-ids %s\n", region, instanceID)
	fmt.Fprintf(os.Stderr, "%s\n\n", rule)
}

// resolveHostIPv4 looks up host's first IPv4 address, since AWS's ip-address filter
// doesn't match on IPv6.
func resolveHostIPv4(host string) (string, error) {
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address found for %s", host)
}

// ec2DescribeInstancesOutput is the subset of `aws ec2 describe-instances --output json`
// this package actually needs.
type ec2DescribeInstancesOutput struct {
	Reservations []struct {
		Instances []struct {
			InstanceID string `json:"InstanceId"`
			State      struct {
				Name string `json:"Name"`
			} `json:"State"`
		} `json:"Instances"`
	} `json:"Reservations"`
}

// describeInstanceForHost returns the instance in region whose public address matches ip,
// or ("", "", nil) if none is found -- not an error, the caller decides whether that is
// fatal.
//
// instanceTag optionally narrows the search to resources carrying a given tag, written
// "Key=Value". Supplying one means a mis-set region or a stale DNS record fails as "not
// found" rather than matching an unrelated instance that happens to share an address.
//
// The tag is operator-supplied and empty by default. A tag value identifies one particular
// deployment, so hardcoding one here would put a single organisation's naming into
// MIT-licensed code that other people are meant to run against their own infrastructure.
func describeInstanceForHost(region, ip, instanceTag string) (instanceID, state string, err error) {
	args := []string{"ec2", "describe-instances", "--region", region, "--filters"}
	// One --filters flag taking several values; repeating the flag would drop all but the
	// last, which would silently widen the search rather than narrow it.
	if filter := tagFilter(instanceTag); filter != "" {
		args = append(args, filter)
	}
	args = append(args, "Name=ip-address,Values="+ip, "--output", "json")

	out, err := RunCommandCaptureOutput("aws", args...)
	if err != nil {
		return "", "", err
	}
	return parseInstanceDescribeOutput(out)
}

// tagFilter turns an operator-supplied "Key=Value" into the provider's filter syntax, or
// "" when nothing usable was given. A malformed value is ignored rather than fatal: it can
// only ever widen the search back to matching on address alone, which is what happens
// without a tag anyway.
func tagFilter(instanceTag string) string {
	key, value, found := strings.Cut(strings.TrimSpace(instanceTag), "=")
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	if !found || key == "" || value == "" {
		return ""
	}
	return "Name=tag:" + key + ",Values=" + value
}

// parseInstanceDescribeOutput is split out from describeInstanceForHost so the JSON-parsing
// logic can be unit tested against canned AWS CLI output without actually shelling out.
func parseInstanceDescribeOutput(rawJSON string) (instanceID, state string, err error) {
	var parsed ec2DescribeInstancesOutput
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return "", "", fmt.Errorf("parsing aws ec2 describe-instances output: %w", err)
	}
	for _, r := range parsed.Reservations {
		for _, i := range r.Instances {
			return i.InstanceID, i.State.Name, nil
		}
	}
	return "", "", nil
}

// waitForSSH polls host's SSH port (22) until it accepts a TCP connection or timeout
// elapses.
func waitForSSH(host string, timeout time.Duration) error {
	return waitForTCP(net.JoinHostPort(host, "22"), timeout, 5*time.Second)
}

// waitForTCP polls addr until it accepts a TCP connection, timeout elapses, or interval
// between attempts -- split out from waitForSSH so tests can use a short interval instead
// of the real 5s cadence.
func waitForTCP(addr string, timeout, interval time.Duration) error {
	dialTimeout := interval
	if dialTimeout > 5*time.Second {
		dialTimeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err == nil {
			_ = conn.Close() //nolint:errcheck
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out after %s waiting for %s", timeout, addr)
}
