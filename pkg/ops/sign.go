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
		fmt.Println("Usage: lfr-tunnel-ops sign [-allow-stale]")
		fmt.Println("\nSigns dist/'s built binaries: macOS via codesign (LFT_MACOS_IDENTITY),")
		fmt.Println("Windows via osslsigncode (LFT_SIGN_KEY/LFT_SIGN_CRT or LFT_SIGN_P12, plus")
		fmt.Println("LFT_SIGN_PASS), and Linux via a detached GPG signature (LFT_GPG_KEY,")
		fmt.Println("LFT_GPG_SECRET, LFT_GPG_PASS). Any step is skipped if its env vars are")
		fmt.Println("unset. Regenerates dist/checksums.txt and minisign-signs it.")
		fmt.Println("\nRefuses to run unless dist/'s build manifest matches pkg/config/version.go,")
		fmt.Println("so last release's binaries cannot be signed by accident (#1279).")
		fmt.Println("-allow-stale overrides that, deliberately.")
		return
	}

	// A real FlagSet rather than scanning args, so `sign -allow-stail` is rejected instead of
	// silently signing stale artefacts -- the failure mode being fixed here.
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	allowStale := fs.Bool("allow-stale", false, "sign dist/ even if it was not built from the current source")
	CheckFatal(fs.Parse(args), "Failed to parse arguments")

	fmt.Println("=== Beginning Signing Process ===")

	binDir := "dist"

	// Before anything is signed. A signature over the wrong bytes is worse than no signature:
	// it makes stale artefacts look verified all the way to the user (#1279).
	RequireCurrentDist(binDir, "sign", *allowStale)

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

	// Steps that were configured, attempted, and failed. A step nobody configured is skipped and
	// does not belong here -- that is deliberate behaviour. This exists so the two cannot be
	// confused: `sign` used to exit 0 after producing no signatures at all (#1596).
	var signingFailures []string

	// 1. macOS Signing
	if macosIdentity != "" && macosIdentity != "skip" {
		fmt.Println("Signing macOS binaries...")
		for _, arch := range []string{"arm64", "amd64"} {
			target := filepath.Join(binDir, fmt.Sprintf("lfr-tunnel-darwin-%s", arch))
			err := RunCommand("codesign", "--force", "--options", "runtime", "--sign", macosIdentity, target)
			CheckFatal(err, "macOS codesign failed for "+arch)
		}
		fmt.Println("macOS binaries successfully signed!")
	} else {
		fmt.Println("Skipping macOS codesigning (no identity provided or skipped).")
	}

	// 2. Windows Signing
	validP12 := signP12 != "" && signP12 != "skip"
	validKeyCrt := signKey != "" && signKey != "skip" && signCrt != "" && signCrt != "skip" &&
		(fileExists(signKey) || strings.Contains(signKey, "-----BEGIN")) &&
		(fileExists(signCrt) || strings.Contains(signCrt, "-----BEGIN"))

	if validP12 || validKeyCrt {
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

		err := RunCommand("osslsigncode", args...)
		CheckFatal(err, "Windows binary signing failed")

		err = os.Rename(out, in)
		CheckFatal(err, "Failed to replace windows binary")
		fmt.Println("Windows binary successfully signed!")
	} else {
		fmt.Println("Skipping Windows signing (no valid certificate file or PEM content provided/found).")
	}

	// 3. Linux GPG Signing
	if skipGPG != "true" && gpgKey != "skip" {
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

	// 4. Regenerate Checksums
	fmt.Println("Updating checksums.txt...")
	err := generateChecksums(binDir)
	CheckFatal(err, "Failed to generate checksums")

	if len(signingFailures) > 0 {
		fmt.Println()
		fmt.Println("=== Signing FAILED ===")
		for _, f := range signingFailures {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println("dist/ is NOT ready to publish. Fix the above and re-run.")
		os.Exit(1)
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

func generateChecksums(dir string) error {
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
	fmt.Println("Generating Minisign signature for checksums.txt...")
	if err := minisign.SignFileFromEnv(checksumsPath, checksumsPath+".minisig"); err != nil {
		// Still a warning rather than fatal, as before. No key configured is a legitimate
		// state, and the platform signatures above are independent of this one -- but say
		// which of the two it was, since "no key" and "signing broke" want different actions.
		if errors.Is(err, minisign.ErrNoSecretKey) {
			fmt.Printf("Skipping Minisign signature: %v. Clients upgrading will be told the "+
				"download cannot be verified.\n", err)
		} else {
			fmt.Printf("WARNING: Minisign signature generation failed: %v\n", err)
		}
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
