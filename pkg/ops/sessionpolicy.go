package ops

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Reconciling the portal session policy onto a running gateway (#1681).
//
// deploy uploads the binary and static assets and never touches server-config.yaml, and
// provisioning uploads a config the operator built by hand. So a setting reached a box only on
// initial provision or by someone SSHing in -- which is how portal_session_duration went from
// 24h to 8h, and portal_session_max_lifetime arrived, with nothing anywhere able to notice if
// either later drifted. Exactly the gap reconcile-nginx was built to close for nginx (#997).
//
// SECRECY RULE, inherited from check-config and equally non-negotiable: server-config.yaml holds
// token hashes, SMTP credentials and webhook URLs. Only the two session keys are ever read,
// compared, reported or written. No other key's value is examined, and none is printed.

// The only two keys this may read or write. Named explicitly rather than derived, so widening
// the blast radius takes a deliberate edit rather than a config file gaining a field.
const (
	keySessionDuration    = "portal_session_duration"
	keySessionMaxLifetime = "portal_session_max_lifetime"
)

// SessionPolicy is a declared or observed pair of session settings. An empty string means "not
// set", which is distinct from a zero duration: unset means the deployment expresses no opinion
// and the value is left alone.
type SessionPolicy struct {
	Duration    string
	MaxLifetime string
}

// PolicyDrift is one setting whose live value differs from what the deployment declares.
type PolicyDrift struct {
	Key      string
	Declared string
	Live     string
}

func (d PolicyDrift) String() string {
	live := d.Live
	if live == "" {
		live = "(not set)"
	}
	return fmt.Sprintf("%s: declared %q, live %s", d.Key, d.Declared, live)
}

// ValidateSessionPolicy rejects a declaration the gateway could not honour, before anything is
// written to a live box.
//
// An unparseable duration would be silently ignored by the server -- yaml decodes it into a
// zero time.Duration and the gateway falls back to its default -- so a typo would look like the
// setting simply having no effect. Better to refuse it here than to push it and wonder.
func ValidateSessionPolicy(p SessionPolicy) error {
	for key, val := range map[string]string{
		keySessionDuration:    p.Duration,
		keySessionMaxLifetime: p.MaxLifetime,
	} {
		if val == "" {
			continue
		}
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("%s: %q is not a duration (want e.g. \"8h\", \"30m\"): %w", key, val, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s: %q must be positive", key, val)
		}
	}

	// A cap below the idle timeout is not wrong, but it makes the idle setting dead: every
	// session would end at the cap first. Almost certainly a mistake, and silently doing what
	// was asked would hide it.
	if p.Duration != "" && p.MaxLifetime != "" {
		idle, err1 := time.ParseDuration(p.Duration)
		max, err2 := time.ParseDuration(p.MaxLifetime)
		if err1 == nil && err2 == nil && max < idle {
			return fmt.Errorf(
				"portal_session_max_lifetime (%s) is shorter than portal_session_duration (%s), "+
					"which makes the idle timeout unreachable -- every session would end at the cap",
				p.MaxLifetime, p.Duration)
		}
	}
	return nil
}

// ReadSessionPolicy extracts the two session keys from a live config.
//
// Only these keys are read. The rest of the document is not inspected, which is what keeps this
// away from the credentials in the same file.
func ReadSessionPolicy(configYAML []byte) (SessionPolicy, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(configYAML, &doc); err != nil {
		return SessionPolicy{}, fmt.Errorf("parsing the live config: %w", err)
	}
	get := func(k string) string {
		if v, ok := doc[k]; ok && v != nil {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	return SessionPolicy{
		Duration:    get(keySessionDuration),
		MaxLifetime: get(keySessionMaxLifetime),
	}, nil
}

// DiffSessionPolicy reports the settings the deployment declares that the live config does not
// match. A declaration of "" is no declaration and is skipped, so a deployment that manages only
// one of the two is not told the other has drifted.
func DiffSessionPolicy(declared, live SessionPolicy) []PolicyDrift {
	var out []PolicyDrift
	pairs := []struct{ key, want, got string }{
		{keySessionDuration, declared.Duration, live.Duration},
		{keySessionMaxLifetime, declared.MaxLifetime, live.MaxLifetime},
	}
	for _, p := range pairs {
		if p.want == "" || p.want == p.got {
			continue
		}
		out = append(out, PolicyDrift{Key: p.key, Declared: p.want, Live: p.got})
	}
	return out
}

// ApplySessionPolicy returns configYAML with the declared session keys set, updating a key in
// place where it exists and appending it where it does not.
//
// Round-tripped through yaml.Node rather than a map, because a map loses every comment and the
// key order in the file. Rewriting an operator's config into an unrecognisable shape -- however
// semantically identical -- is not something a tool should do to a file it does not own.
func ApplySessionPolicy(configYAML []byte, declared SessionPolicy) ([]byte, error) {
	if declared.Duration == "" && declared.MaxLifetime == "" {
		return configYAML, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(configYAML, &doc); err != nil {
		return nil, fmt.Errorf("parsing the live config: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("the live config is not a YAML mapping")
	}
	root := doc.Content[0]

	set := func(key, val string) {
		if val == "" {
			return
		}
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				root.Content[i+1].SetString(val)
				// Quoted, so a duration is never mistaken for something else by a later reader.
				root.Content[i+1].Style = yaml.DoubleQuotedStyle
				return
			}
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: val, Style: yaml.DoubleQuotedStyle},
		)
	}
	set(keySessionDuration, declared.Duration)
	set(keySessionMaxLifetime, declared.MaxLifetime)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("re-encoding the config: %w", err)
	}
	return out, nil
}

// ReconcileServerConfigCommand pushes the declared session policy onto a running gateway.
//
// Shaped like reconcile-nginx, for the same reason it is: back up first, apply, verify, and
// restore the backup if verification fails. The verifier differs -- there is no `nginx -t` for
// this file, so the gateway itself is the test: a config it cannot parse is a gateway that does
// not come back healthy, and that is what triggers the rollback.
//
// Safe to re-run: when the live values already match, it reports so and writes nothing, which
// also means it doubles as a way to confirm a box is where it should be.
func ReconcileServerConfigCommand(args []string) {
	fs := flag.NewFlagSet("reconcile-server-config", flag.ExitOnError)
	remotePath := fs.String("remote-config", "/etc/lfr-tunneld/server-config.yaml",
		"path to the gateway config on the target")
	identityFile := fs.String("i", "", "path to SSH private key file")
	flagUser := fs.String("u", "", "SSH username on the target")
	flagHost := fs.String("s", "", "SSH host of the target")
	flagTarget := fs.String("target", "", "named target from a multi-target lfr-tunnel-ops.yaml")
	dryRun := fs.Bool("dry-run", false, "report what would change and write nothing")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops reconcile-server-config [-dry-run] [-remote-config path] [-i key] [-u user] [-s host] [-target name]")
		fmt.Println("\nApplies the session: block from lfr-tunnel-ops.yaml to a running gateway.")
		fmt.Println("\ndeploy uploads the binary and never touches server-config.yaml, so a setting")
		fmt.Println("reached a box only on initial provision or by hand -- and nothing could tell you")
		fmt.Println("afterwards whether it had drifted (#1681).")
		fmt.Println("\nBacks the file up first, restarts the gateway, and restores the backup if it")
		fmt.Println("does not come back healthy. Only the two session keys are read or written; every")
		fmt.Println("other key, including the credentials in the same file, is left untouched and")
		fmt.Println("unexamined.")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	target, err := ResolveDeployTarget(*flagUser, *flagHost, *identityFile, *flagTarget)
	CheckFatal(err, "Failed to resolve target")
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	declared := SessionPolicy{
		Duration:    target.SessionDuration,
		MaxLifetime: target.SessionMaxLifetime,
	}
	if declared.Duration == "" && declared.MaxLifetime == "" {
		fmt.Println("No session: block declared in lfr-tunnel-ops.yaml -- nothing to reconcile.")
		fmt.Println("Declare portal_session_duration and/or portal_session_max_lifetime under session:")
		fmt.Println("to have this manage them. See lfr-tunnel-ops.yaml.example.")
		return
	}
	// Validated before the file is read, let alone written: a typo should cost nothing.
	CheckFatal(ValidateSessionPolicy(declared), "The declared session policy is not usable")

	fmt.Printf("=== Reconciling %s:%s ===\n", sshTarget, *remotePath)

	configYAML, err := RunCommandCaptureOutput("ssh", "-i", target.IdentityFile, sshTarget,
		"sudo cat "+*remotePath)
	CheckFatal(err, "Failed to read the remote config")

	live, err := ReadSessionPolicy([]byte(configYAML))
	CheckFatal(err, "Failed to parse the remote config")

	drift := DiffSessionPolicy(declared, live)
	if len(drift) == 0 {
		fmt.Println("Already matches the declared policy. Nothing to do.")
		return
	}
	for _, d := range drift {
		fmt.Printf("  %s\n", d)
	}
	if *dryRun {
		fmt.Println("\n-dry-run: nothing was written.")
		return
	}

	updated, err := ApplySessionPolicy([]byte(configYAML), declared)
	CheckFatal(err, "Failed to apply the policy")

	applySessionPolicyRemotely(target, sshTarget, *remotePath, updated)
}

// applySessionPolicyRemotely writes the config, restarts, verifies, and rolls back on failure.
//
// The gateway is the validator. There is no offline check for this file -- an unparseable config
// is only discovered when lfr-tunneld tries to start with it -- so "did it come back healthy"
// is the test, and a box that does not is returned to its previous config and restarted again.
//
// The new file is staged in the operator's home directory and moved into place with sudo, rather
// than piped straight to a privileged path: a truncated transfer then leaves a bad file in $HOME
// rather than a half-written one where the gateway reads it.
func applySessionPolicyRemotely(target DeployTarget, sshTarget, remotePath string, updated []byte) {
	tmp, err := os.CreateTemp("", "server-config-*.yaml")
	CheckFatal(err, "Failed to create a local temp file")
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(updated)
	CheckFatal(err, "Failed to write the local temp config")
	CheckFatal(tmp.Close(), "Failed to close the local temp config")

	staged := "/home/" + target.User + "/server-config.reconcile.yaml"
	err = RunCommand("scp", "-i", target.IdentityFile, tmp.Name(), sshTarget+":"+staged)
	CheckFatal(err, "Failed to copy the new config to the target")

	remoteScript := `
set -e
TARGET=` + remotePath + `
NEW=` + staged + `
STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="$TARGET.bak-$STAMP"

sudo cp "$TARGET" "$BACKUP"
echo "Backed up to $BACKUP"

# Ownership and mode are preserved from the file being replaced rather than assumed: this file
# is read by the gateway's own user and holds credentials, so a world-readable copy would be a
# regression check-config would (rightly) report.
sudo chown --reference="$TARGET" "$NEW"
sudo chmod --reference="$TARGET" "$NEW"
sudo mv "$NEW" "$TARGET"

sudo systemctl restart lfr-tunneld
sleep 5

# Healthy means the unit is active AND the gateway answers. A unit that is "active" while the
# process crash-loops would otherwise read as success.
HEALTHY=false
if sudo systemctl is-active --quiet lfr-tunneld; then
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		if curl -fsS --max-time 5 -o /dev/null http://127.0.0.1:8080/api/version 2>/dev/null; then
			HEALTHY=true
			break
		fi
		sleep 2
	done
fi

if [ "$HEALTHY" = true ]; then
	echo "RECONCILE_OK"
else
	echo "Gateway did not come back healthy -- restoring the previous config."
	sudo cp "$BACKUP" "$TARGET"
	sudo systemctl restart lfr-tunneld
	echo "RECONCILE_ROLLED_BACK"
fi
`
	out, err := RunCommandCaptureOutput("ssh", "-i", target.IdentityFile, sshTarget, remoteScript)
	fmt.Print(out)
	CheckFatal(err, "Failed to apply the config remotely")

	switch {
	case strings.Contains(out, "RECONCILE_OK"):
		fmt.Println("\nReconciled. The gateway is running the declared session policy.")
	case strings.Contains(out, "RECONCILE_ROLLED_BACK"):
		// Not a silent failure: the operator has to know the box is on its old config, or they
		// will believe a policy is in force that is not.
		CheckFatal(fmt.Errorf("the gateway did not come back healthy and was rolled back"),
			"Reconcile failed")
	default:
		CheckFatal(fmt.Errorf("the remote script reported neither success nor rollback"),
			"Reconcile outcome unknown -- check the target before assuming either")
	}
}
