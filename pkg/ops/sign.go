package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lfr-tunnel/pkg/minisign"
)

// SignCommand handles the signing of macOS, Windows, and Linux binaries.
func SignCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops sign [-allow-stale] [-allow-no-default] [-dry-run]")
		fmt.Println("\nSigns dist/'s built binaries: macOS via codesign (LFT_MACOS_IDENTITY),")
		fmt.Println("Windows via osslsigncode (LFT_SIGN_KEY/LFT_SIGN_CRT or LFT_SIGN_P12, plus")
		fmt.Println("LFT_SIGN_PASS), and Linux via a detached GPG signature (LFT_GPG_KEY,")
		fmt.Println("LFT_GPG_SECRET, LFT_GPG_PASS). Regenerates dist/checksums.txt and")
		fmt.Println("minisign-signs it.")
		fmt.Println("\nRefuses to run if a step is neither configured nor explicitly skipped")
		fmt.Println("(#1906): set the relevant variable to \"skip\", or LFT_SKIP_GPG=true /")
		fmt.Println("LFT_SKIP_MINISIGN=true, to sign a subset deliberately.")
		fmt.Println("\nExit codes: 1 setup, 2 missing tool, 3 missing credential, 4 signing")
		fmt.Println("failed, 5 checksums failed.")
		fmt.Println("\nRefuses to run unless dist/'s build manifest matches pkg/config/version.go,")
		fmt.Println("so last release's binaries cannot be signed by accident (#1279).")
		fmt.Println("-allow-stale overrides that, deliberately.")
		fmt.Println("\nAlso refuses to sign clients with no default gateway, since deploy-clients")
		fmt.Println("would refuse to publish them. -allow-no-default overrides that.")
		return
	}

	// A real FlagSet rather than scanning args, so `sign -allow-stail` is rejected instead of
	// silently signing stale artefacts -- the failure mode being fixed here.
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	allowStale := fs.Bool("allow-stale", false, "sign dist/ even if it was not built from the current source")
	allowNoDefault := fs.Bool("allow-no-default", false, "sign clients that have no default gateway compiled in")
	// Answers "will this run work?" without spending the biometric prompt to find out, and
	// without touching dist/ -- so an already-published dist/ cannot be re-signed into
	// different bytes just to test the configuration (#1906).
	dryRun := fs.Bool("dry-run", false, "report what would be signed and exit; sign nothing")
	CheckFatal(fs.Parse(args), "Failed to parse arguments")

	// Before anything else, including the dry run: an op:// reference is not a configured
	// credential, and every check below would be reasoning about the literal string
	// "op://Employee/..." rather than a secret (#1978). Re-execs under `op run` and does not
	// return if it does.
	ResolveOpRefsOrReexec(append([]string{"sign"}, args...))

	fmt.Println("=== Beginning Signing Process ===")

	binDir := "dist"

	// Before anything is signed. A signature over the wrong bytes is worse than no signature:
	// it makes stale artefacts look verified all the way to the user (#1279).
	// Skipped for a dry run: staleness is about dist/, and a dry run does not read or write it.
	// Checking it anyway would make the configuration check fail for an unrelated reason.
	var manifest BuildManifest
	if !*dryRun {
		manifest = RequireCurrentDist(binDir, "sign", *allowStale)
	}

	// Signing is the expensive step -- it raises a biometric prompt and burns a person's
	// attention -- and it sits between build and publish, so without this the natural sequence
	// signed binaries deploy-clients would then refuse (#1723). Same condition, checked before
	// the cost rather than after.
	if !*dryRun {
		RequireDefaultGateway(manifest, "sign", *allowNoDefault)
	}

	macosIdentity := GetEnvOrDefault("LFT_MACOS_IDENTITY", "")
	signP12 := GetEnvOrDefault("LFT_SIGN_P12", "")
	signKey := GetEnvOrDefault("LFT_SIGN_KEY", "")
	signCrt := GetEnvOrDefault("LFT_SIGN_CRT", "")
	signPass := GetEnvOrDefault("LFT_SIGN_PASS", "")
	gpgKey := GetEnvOrDefault("LFT_GPG_KEY", "")
	gpgPass := GetEnvOrDefault("LFT_GPG_PASS", "")
	if gpgPass == "" {
		gpgPass = signPass
	}
	if gpgPass == "none" || gpgPass == "skip" {
		gpgPass = ""
	}
	gpgSecret := GetEnvOrDefault("LFT_GPG_SECRET", "")
	skipGPG := GetEnvOrDefault("LFT_SKIP_GPG", "")

	// Decide the whole run before signing anything (#1906).
	//
	// Previously each step made its own decision where it stood, printed "Skipping ..." and
	// carried on, so a release could be half-signed and still exit 0 -- and the operator found
	// out from a "Skipping" line in the middle of an otherwise successful-looking run, if at
	// all. Refusing up front means the biometric prompt is never spent on a run that was always
	// going to produce an incomplete dist/.
	plan, code, problems := planSigning(signEnv{
		MacOSIdentity: macosIdentity,
		SignP12:       signP12,
		SignKey:       signKey,
		SignCrt:       signCrt,
		GPGKey:        gpgKey,
		GPGSecret:     gpgSecret,
		SkipGPG:       skipGPG,
		SkipMinisign:  GetEnvOrDefault("LFT_SKIP_MINISIGN", ""),
		MinisignKey:   GetEnvOrDefault("MINISIGN_SECRET_KEY", ""),
	}, lookPathFn)
	if code != SignExitOK {
		fmt.Println()
		fmt.Println("=== Signing REFUSED ===")
		for _, p := range problems {
			fmt.Printf("  - %s\n", p)
		}
		fmt.Println("Nothing was signed and dist/ is unchanged.")
		os.Exit(code)
	}
	if *dryRun {
		fmt.Println()
		fmt.Println("=== Dry run: configuration is complete ===")
		fmt.Printf("  macOS:    %s\n", wouldSign(plan.MacOS))
		fmt.Printf("  Windows:  %s\n", wouldSign(plan.Windows))
		fmt.Printf("  Linux:    %s\n", wouldSign(plan.Linux))
		fmt.Printf("  minisign: %s\n", wouldSign(plan.Minisign))
		fmt.Println("Nothing was signed.")
		return
	}

	if skipped := describeSkips(plan); len(skipped) > 0 {
		// Up front, not where each step would have been reached.
		fmt.Printf("Deliberately NOT signing: %s (explicitly skipped).\n", strings.Join(skipped, ", "))
	}

	// Steps that were configured, attempted, and failed. A step nobody configured is skipped and
	// does not belong here -- that is deliberate behaviour. This exists so the two cannot be
	// confused: `sign` used to exit 0 after producing no signatures at all (#1596).
	var signingFailures []string

	// 1. macOS Signing
	if plan.MacOS {
		fmt.Println("Signing macOS binaries...")
		for _, arch := range []string{"arm64", "amd64"} {
			target := filepath.Join(binDir, fmt.Sprintf("lfr-tunnel-darwin-%s", arch))
			err := RunCommand("codesign", "--force", "--options", "runtime", "--sign", macosIdentity, target)
			if err != nil {
				// Recorded rather than fatal so the run reports every broken platform at
				// once. Aborting here meant fixing macOS only to discover Windows was also
				// misconfigured on the next attempt -- two biometric prompts to learn two
				// facts that were both true at the start (#1906).
				fmt.Printf("WARNING: macOS codesign failed for %s: %v\n", arch, err)
				signingFailures = append(signingFailures, "macOS signature for "+arch)
			}
		}
		if len(signingFailures) == 0 {
			fmt.Println("macOS binaries successfully signed!")
		}
	} else {
		fmt.Println("Skipping macOS codesigning (no identity provided or skipped).")
	}

	// 2. Windows Signing
	validP12 := signP12 != "" && signP12 != skipSentinel

	// An op:// reference that never resolved is neither a readable path nor PEM text. It used
	// to fall through to "Skipping Windows signing", which reads as a choice; now it is the
	// failure it always was -- the usual cause being a run that was not under `op run --`.
	keyUsable := fileExists(signKey) || strings.Contains(signKey, "-----BEGIN")
	crtUsable := fileExists(signCrt) || strings.Contains(signCrt, "-----BEGIN")
	if plan.Windows && !validP12 && (!keyUsable || !crtUsable) {
		fmt.Println()
		fmt.Println("=== Signing REFUSED ===")
		fmt.Println("  - Windows: LFT_SIGN_KEY/LFT_SIGN_CRT are set but are neither readable files")
		fmt.Println("    nor inline PEM. If they are op:// references, run under `op run --`.")
		os.Exit(SignExitCredentials)
	}

	if plan.Windows {
		fmt.Println("Signing Windows binary...")
		in := filepath.Join(binDir, "lfr-tunnel-windows-amd64.exe")
		out := filepath.Join(binDir, "lfr-tunnel-windows-amd64-signed.exe")

		var args []string
		args = append(args, "sign")

		var tempFiles []string
		defer func() {
			for _, f := range tempFiles {
				os.Remove(f)
			}
		}()

		if validP12 {
			if !fileExists(signP12) {
				tmpP12, err := os.CreateTemp("", "sign-*.p12")
				if err != nil {
					CheckFatal(err, "failed to create tmp p12 file")
				}
				if _, err := tmpP12.Write([]byte(signP12)); err != nil {
					CheckFatal(err, "failed to write tmp p12 file")
				}
				tmpP12.Close()
				signP12 = tmpP12.Name()
				tempFiles = append(tempFiles, signP12)
			}
			args = append(args, "-pkcs12", signP12)
		} else {
			if !fileExists(signKey) && strings.Contains(signKey, "-----BEGIN") {
				tmpKey, _ := os.CreateTemp("", "key-*.pem")
				if _, err := tmpKey.WriteString(signKey); err != nil {
					CheckFatal(err, "failed to write tmp key")
				}
				tmpKey.Close()
				signKey = tmpKey.Name()
				tempFiles = append(tempFiles, signKey)
			}
			if !fileExists(signCrt) && strings.Contains(signCrt, "-----BEGIN") {
				tmpCrt, _ := os.CreateTemp("", "crt-*.pem")
				if _, err := tmpCrt.WriteString(signCrt); err != nil {
					CheckFatal(err, "failed to write tmp crt")
				}
				tmpCrt.Close()
				signCrt = tmpCrt.Name()
				tempFiles = append(tempFiles, signCrt)
			}
			args = append(args, "-key", signKey, "-certs", signCrt)
		}

		if signPass != "" {
			// -readpass rather than -pass: the password goes in a 0600 file that osslsigncode
			// reads, so it never appears on argv where `ps` would show it (#1555).
			passFile, err := writeSecretFile(signPass, "signpass-*")
			CheckFatal(err, "failed to write the Windows signing password to a temp file")
			tempFiles = append(tempFiles, passFile)
			args = append(args, "-readpass", passFile)
		}

		args = append(args, "-n", "Liferay Tunnel", "-i", "https://github.com/peterrichards-lr/lfr-tunnel", "-in", in, "-out", out)

		if err := RunCommand("osslsigncode", args...); err != nil {
			fmt.Printf("WARNING: Windows binary signing failed: %v\n", err)
			signingFailures = append(signingFailures, "Windows signature")
		} else if err := os.Rename(out, in); err != nil {
			// The signed artefact exists but never replaced the unsigned one, so dist/ still
			// holds an unsigned exe. Counted as a signing failure for exactly that reason.
			fmt.Printf("WARNING: could not replace the Windows binary with its signed copy: %v\n", err)
			signingFailures = append(signingFailures, "Windows signature (signed copy not installed)")
		} else {
			fmt.Println("Windows binary successfully signed!")
		}
	} else {
		fmt.Println("Skipping Windows signing (no valid certificate file or PEM content provided/found).")
	}

	// 3. Linux GPG Signing
	if plan.Linux {
		// One passphrase file for the import and both detached signatures, rather than
		// --passphrase on each invocation, which would put it on argv three times (#1555).
		gpgPassFile := ""
		if gpgPass != "" {
			var err error
			gpgPassFile, err = writeSecretFile(gpgPass, "gpgpass-*")
			CheckFatal(err, "failed to write the GPG passphrase to a temp file")
			defer os.Remove(gpgPassFile)
		}
		if gpgSecret != "" {
			if !fileExists(gpgSecret) && strings.Contains(gpgSecret, "-----BEGIN") {
				tmpSec, _ := os.CreateTemp("", "gpg-*.asc")
				if _, err := tmpSec.WriteString(gpgSecret); err != nil {
					CheckFatal(err, "failed to write tmp gpg secret")
				}
				tmpSec.Close()
				gpgSecret = tmpSec.Name()
				defer os.Remove(gpgSecret)
			}

			// Decide whether there is anything to import before shelling out, so a redundant
			// import cannot look like a failure (#1596).
			switch {
			case gpgKey != "" && gpgKeyInKeyring(gpgKey):
				// Importing again would succeed as a no-op at best, and -- when LFT_GPG_SECRET
				// holds an unresolved op:// reference, which is the usual local setup -- fails
				// with "can't open op://..." while the signing that follows works perfectly.
				// That warning is indistinguishable from a real failure in the one workflow
				// where a misleading signal matters most.
				fmt.Printf("GPG signing key %s is already in the keyring; no import needed.\n", gpgKey)

			case !fileExists(gpgSecret):
				// Neither a readable file nor inline key material. Almost always an op://
				// reference that was never resolved because the command was not run under
				// `op run`. Said plainly rather than handed to gpg, which reports it as an
				// opaque "can't open".
				//
				// Not fatal on its own: the signing step below may still succeed from a key
				// already present under a different identifier. It does warn, because if it
				// cannot, the run produces no Linux signatures and still exits 0.
				fmt.Println("WARNING: LFT_GPG_SECRET is neither a readable file nor inline key material.")
				fmt.Println("         If it is an op:// reference, run this under `op run --` so it resolves.")
				fmt.Println("         Skipping the import; Linux signing will fail unless the key is already present.")

			default:
				importArgs := []string{"--batch", "--yes"}
				if gpgPassFile != "" {
					importArgs = append(importArgs, "--pinentry-mode", "loopback", "--passphrase-file", gpgPassFile)
				}
				importArgs = append(importArgs, "--import", gpgSecret)
				if err := RunCommand("gpg", importArgs...); err != nil {
					fmt.Printf("WARNING: Failed to import GPG secret key: %v\n", err)
				} else {
					fmt.Println("GPG secret key imported successfully.")
				}
			}
		}

		for _, arch := range []string{"amd64", "arm64"} {
			fmt.Printf("Generating Linux detached GPG signature for %s...\n", arch)
			target := filepath.Join(binDir, fmt.Sprintf("lfr-tunnel-linux-%s", arch))
			sigPath := target + ".asc"
			os.Remove(sigPath)

			var gpgArgs []string
			gpgArgs = append(gpgArgs, "--batch", "--yes")
			if gpgPassFile != "" {
				gpgArgs = append(gpgArgs, "--pinentry-mode", "loopback", "--passphrase-file", gpgPassFile)
			}
			if gpgKey != "" {
				gpgArgs = append(gpgArgs, "--local-user", gpgKey)
			}
			gpgArgs = append(gpgArgs, "--armor", "--detach-sign", target)

			err := RunCommand("gpg", gpgArgs...)
			if err != nil {
				fmt.Printf("WARNING: GPG signing failed for %s: %v\n", arch, err)
				// Recorded, not just printed. Linux signing was configured and attempted, so a
				// failure here means the release is missing signatures it was meant to have --
				// and the previous .asc was already removed above, so the artefacts are worse
				// off than before the run (#1596).
				signingFailures = append(signingFailures, fmt.Sprintf("Linux GPG signature for %s", arch))
			} else {
				fmt.Printf("Linux detached GPG signature for %s successfully created!\n", arch)
			}
		}
	} else {
		fmt.Println("Skipping Linux GPG signing.")
	}

	// 4. Refuse BEFORE touching checksums.txt (#1906).
	//
	// The order used to be the other way round, so a half-signed dist/ got a freshly
	// regenerated checksums.txt describing it -- internally consistent, and therefore
	// publishable by anything that only checks the hashes. Leaving the previous file in place
	// means deploy-clients' own checksum verification fails instead, which is the outcome we
	// want from a run that did not finish.
	if len(signingFailures) > 0 {
		fmt.Println()
		fmt.Println("=== Signing FAILED ===")
		for _, f := range signingFailures {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("dist/ is NOT ready to publish, and checksums.txt was left untouched.")
		os.Exit(SignExitSigning)
	}

	// 5. Regenerate Checksums
	fmt.Println("Updating checksums.txt...")
	if err := generateChecksums(binDir, plan.Minisign); err != nil {
		fmt.Printf("ERROR: Failed to generate checksums: %v\n", err)
		os.Exit(SignExitChecksums)
	}

	fmt.Println("=== Client Signing Complete! ===")
}

// writeSecretFile puts a credential in a 0600 file so it can be handed to a tool by path
// instead of on the command line.
//
// The point is argv, not the echo. While a process runs, its arguments are readable by any
// local user through the process table, so a password passed as `-pass <value>` is exposed
// whether or not anything prints it. Redacting the log fixes the visible half only (#1555).
func writeSecretFile(content, pattern string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// CreateTemp already makes the file 0600; being explicit means a change to that default
	// cannot quietly widen a credential file.
	if err := f.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// gpgKeyInKeyring reports whether gpg already holds a secret key matching the given identifier.
//
// Deliberately quiet: this is a probe, and routing it through RunCommand would print a gpg
// invocation and its stderr on every signing run, which is the sort of noise that trains people
// to stop reading the output (#1596).
func gpgKeyInKeyring(key string) bool {
	if key == "" || key == "skip" {
		return false
	}
	cmd := exec.Command("gpg", "--batch", "--list-secret-keys", key)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func fileExists(filename string) bool {
	if len(filename) > 255 {
		return false
	}
	info, err := os.Stat(filename)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func generateChecksums(dir string, wantMinisign bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var lines []string
	for _, e := range entries {
		// build-manifest.json is excluded deliberately (#1279): checksums.txt is consumed by
		// `lfr-tunnel --upgrade` and by install.sh to verify a DOWNLOADED BINARY, so it stays a
		// list of downloadable artefacts. The manifest is deploy-time provenance, not something
		// a client fetches and hashes.
		if e.IsDir() || e.Name() == "checksums.txt" || e.Name() == BuildManifestName ||
			strings.HasSuffix(e.Name(), ".asc") || strings.HasSuffix(e.Name(), ".minisig") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("%s  %s", hash, e.Name()))
	}

	checksumsPath := filepath.Join(dir, "checksums.txt")
	// 0644 deliberately (#1408): deploy-clients publishes this to the gateway's downloads
	// directory, where `lfr-tunnel --upgrade` and install.sh fetch it to verify a download.
	// It is a list of hashes, not a secret.
	err = os.WriteFile(checksumsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644) //nolint:gosec
	if err != nil {
		return err
	}
	fmt.Printf("Checksums updated in %s\n", checksumsPath)

	// In-process, not `go run scripts/minisign_helper.go` (#1402). That spawned the Go
	// toolchain, which links every executable it builds inside GOTMPDIR -- or the system temp
	// dir when unset -- and `go run` then executes it from there. `sign` is documented as being
	// run directly (`op run -- lfr-tunnel-ops sign`), never through make, so GOTMPDIR was
	// unset and this linked and ran an unsigned binary out of /var/folders on every signing
	// run: the exact pattern CLAUDE.md exists to prevent.
	if !wantMinisign {
		// Reached only when the operator asked for this explicitly; planSigning refuses an
		// absent key outright.
		fmt.Println("Skipping the Minisign signature (explicitly skipped). Upgrading clients " +
			"will be told the download cannot be verified.")
		return nil
	}

	fmt.Println("Generating Minisign signature for checksums.txt...")
	if err := minisign.SignFileFromEnv(checksumsPath, checksumsPath+".minisig"); err != nil {
		// Fatal now, where it used to warn (#1906). minisign is what `lfr-tunnel --upgrade`
		// checks, so a release missing it silently degrades every client upgrade -- and a
		// warning printed at the very end of a signing run is not read. ErrNoSecretKey is
		// still named separately because "no key" and "signing broke" want different actions.
		if errors.Is(err, minisign.ErrNoSecretKey) {
			return fmt.Errorf("minisign key configured but unusable: %w", err)
		}
		return fmt.Errorf("minisign signature generation failed: %w", err)
	}

	return nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
