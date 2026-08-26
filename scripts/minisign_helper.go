package main

import (
	"errors"
	"fmt"
	"os"

	"lfr-tunnel/pkg/minisign"
)

// A thin CLI wrapper over pkg/minisign. The signing logic moved into that package in #1402 so
// the LOCAL path (pkg/ops/sign.go) can call it in-process instead of through `go run`, which
// links and executes an unsigned binary out of the system temp dir on the EDR-protected
// workstation.
//
// This wrapper is kept because .github/workflows/release.yml still invokes it as
// `go run scripts/minisign_helper.go`, signing the checksums for the binaries attached to a
// GitHub release (#1265). That runs on an ephemeral Linux runner, which is unmonitored -- and
// .github/ is excluded from scripts/check-edr-safety.sh for exactly that reason -- so there is
// nothing to fix there and no reason to churn the release path.
//
// The two callers sign different checksum files: CI's binaries and the locally built,
// OS-codesigned ones are not the same bytes. Both verify against the same public key, so a
// client can check whichever route it downloaded from.
//
// MINISIGN_SECRET_KEY must contain the full minisign secret key file content (the
// "untrusted comment: ..." line plus the base64 payload). MINISIGN_KEY_PASSWORD is its
// passphrase; leave unset only if the key was deliberately generated unencrypted
// (`minisign -G -W`), which should never be true for a real signing key -- only for a
// throwaway test fixture.
func main() {
	if len(os.Args) < 3 {
		// Deliberately does not spell out the toolchain invocation. scripts/check-edr-safety.sh
		// now scans Go source, and a usage string advertising the very command the guard exists
		// to discourage is both a false positive for it and bad advice for a reader on macOS.
		fmt.Println("Usage: MINISIGN_SECRET_KEY=<key file content> [MINISIGN_KEY_PASSWORD=<passphrase>] minisign_helper <file_to_sign> <output_signature_file>")
		os.Exit(1)
	}

	if err := minisign.SignFileFromEnv(os.Args[1], os.Args[2]); err != nil {
		if errors.Is(err, minisign.ErrNoSecretKey) {
			fmt.Printf("Error: %v. Pull the real signing key from 1Password "+
				"(self-signed-minisign-key) and export it before running this helper.\n", err)
		} else {
			fmt.Printf("Error: %v\n", err)
		}
		os.Exit(1)
	}

	fmt.Printf("Successfully signed %s -> %s\n", os.Args[1], os.Args[2])
}
