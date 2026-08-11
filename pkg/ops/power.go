package ops

import (
	"encoding/json"
	"fmt"
	"net"
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
func ensureInstanceRunning(host, region string) (restore func(), err error) {
	noop := func() {}
	if region == "" {
		return noop, nil
	}

	ip, err := resolveHostIPv4(host)
	if err != nil {
		return noop, fmt.Errorf("resolving %s to an IP for AWS lookup: %w", host, err)
	}

	instanceID, state, err := describeInstanceByIP(region, ip)
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
			if err := RunCommand("aws", "ec2", "stop-instances", "--region", region, "--instance-ids", instanceID); err != nil {
				fmt.Printf("WARNING: failed to stop instance %s back -- it's left running, stop it manually: %v\n", instanceID, err)
			}
		}
		return restore, nil
	default:
		return noop, fmt.Errorf("instance %s (%s) is in state %q, not running or stopped -- refusing to guess, deploy manually once it settles", instanceID, host, state)
	}
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

// describeInstanceByIP returns the first EC2 instance in region whose public IP matches
// ip, or ("", "", nil) if none is found (not an error -- the caller decides that's fatal).
func describeInstanceByIP(region, ip string) (instanceID, state string, err error) {
	out, err := RunCommandCaptureOutput("aws", "ec2", "describe-instances",
		"--region", region,
		"--filters", "Name=ip-address,Values="+ip,
		"--output", "json")
	if err != nil {
		return "", "", err
	}
	return parseInstanceDescribeOutput(out)
}

// parseInstanceDescribeOutput is split out from describeInstanceByIP so the JSON-parsing
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
			conn.Close()
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out after %s waiting for %s", timeout, addr)
}
