package main

import (
	"fmt"
	"os"

	"github.com/jedisct1/go-minisign"
)

// This helper signs a file with minisign, using a private key supplied entirely via
// environment variables -- it never contains or reads a hardcoded key. It is invoked
// locally (never in CI) as part of the "sign, then deploy" release flow (see
// pkg/ops/sign.go / pkg/ops/deploy.go and docs/server/setup_guide.md), after pulling the
// real key out of 1Password ("self-signed-minisign-key").
//
// MINISIGN_SECRET_KEY must contain the full minisign secret key file content (the
// "untrusted comment: ..." line plus the base64 payload). MINISIGN_KEY_PASSWORD is its
// passphrase; leave unset only if the key was deliberately generated unencrypted
// (`minisign -G -W`), which should never be true for a real signing key -- only for a
// throwaway test fixture.
func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: MINISIGN_SECRET_KEY=<key file content> [MINISIGN_KEY_PASSWORD=<passphrase>] go run minisign_helper.go <file_to_sign> <output_signature_file>")
		os.Exit(1)
	}

	secretKey := os.Getenv("MINISIGN_SECRET_KEY")
	if secretKey == "" {
		fmt.Println("Error: MINISIGN_SECRET_KEY is not set. Pull the real signing key from 1Password (self-signed-minisign-key) and export it before running this helper.")
		os.Exit(1)
	}

	content, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Printf("Failed to read file: %v\n", err)
		os.Exit(1)
	}

	sk, err := minisign.DecodePrivateKey(secretKey)
	if err != nil {
		fmt.Printf("Failed to decode private key: %v\n", err)
		os.Exit(1)
	}
	defer sk.Wipe()

	if sk.IsEncrypted() {
		password := os.Getenv("MINISIGN_KEY_PASSWORD")
		if password == "" {
			fmt.Println("Error: this secret key is password-protected but MINISIGN_KEY_PASSWORD is not set.")
			os.Exit(1)
		}
		if err := sk.Decrypt(password); err != nil {
			fmt.Printf("Failed to decrypt private key: %v\n", err)
			os.Exit(1)
		}
	}

	sig, err := sk.Sign(content, minisign.SignOptions{Hashed: true})
	if err != nil {
		fmt.Printf("Failed to sign: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(os.Args[2], sig.Encode(), 0644)
	if err != nil {
		fmt.Printf("Failed to write signature: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully signed %s -> %s\n", os.Args[1], os.Args[2])
}
