package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SignCommand handles the signing of macOS, Windows, and Linux binaries.
func SignCommand(args []string) {
	fmt.Println("=== Beginning Signing Process ===")

	binDir := "dist"

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
	gpgSecret := GetEnvOrDefault("LFT_GPG_SECRET", "")
	skipGPG := GetEnvOrDefault("LFT_SKIP_GPG", "")

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
	if (signP12 != "" && signP12 != "skip") || (signKey != "" && signCrt != "") {
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

		if signP12 != "" && signP12 != "skip" {
			if !fileExists(signP12) && strings.Contains(signP12, "-----BEGIN") {
				tmpP12, _ := os.CreateTemp("", "sign-*.p12")
				tmpP12.WriteString(signP12)
				tmpP12.Close()
				signP12 = tmpP12.Name()
				tempFiles = append(tempFiles, signP12)
			}
			args = append(args, "-pkcs12", signP12)
		} else {
			if !fileExists(signKey) && strings.Contains(signKey, "-----BEGIN") {
				tmpKey, _ := os.CreateTemp("", "key-*.pem")
				tmpKey.WriteString(signKey)
				tmpKey.Close()
				signKey = tmpKey.Name()
				tempFiles = append(tempFiles, signKey)
			}
			if !fileExists(signCrt) && strings.Contains(signCrt, "-----BEGIN") {
				tmpCrt, _ := os.CreateTemp("", "crt-*.pem")
				tmpCrt.WriteString(signCrt)
				tmpCrt.Close()
				signCrt = tmpCrt.Name()
				tempFiles = append(tempFiles, signCrt)
			}
			args = append(args, "-key", signKey, "-certs", signCrt)
		}

		if signPass != "" {
			args = append(args, "-pass", signPass)
		}

		args = append(args, "-n", "Liferay Tunnel", "-i", "https://github.com/peterrichards-lr/lfr-tunnel", "-in", in, "-out", out)

		err := RunCommand("osslsigncode", args...)
		CheckFatal(err, "Windows binary signing failed")

		err = os.Rename(out, in)
		CheckFatal(err, "Failed to replace windows binary")
		fmt.Println("Windows binary successfully signed!")
	} else {
		fmt.Println("Skipping Windows signing (no certificate provided/found or skipped).")
	}

	// 3. Linux GPG Signing
	if skipGPG != "true" && gpgKey != "skip" {
		fmt.Println("Generating Linux detached GPG signature...")
		target := filepath.Join(binDir, "lfr-tunnel-linux-amd64")
		sigPath := target + ".asc"
		os.Remove(sigPath)

		if gpgSecret != "" {
			if !fileExists(gpgSecret) && strings.Contains(gpgSecret, "-----BEGIN") {
				tmpSec, _ := os.CreateTemp("", "gpg-*.asc")
				tmpSec.WriteString(gpgSecret)
				tmpSec.Close()
				gpgSecret = tmpSec.Name()
				defer os.Remove(gpgSecret)
			}
			
			// Import the secret key into GPG
			importArgs := []string{"--batch", "--yes"}
			if gpgPass != "" {
				importArgs = append(importArgs, "--pinentry-mode", "loopback", "--passphrase", gpgPass)
			}
			importArgs = append(importArgs, "--import", gpgSecret)
			err := RunCommand("gpg", importArgs...)
			if err != nil {
				fmt.Printf("WARNING: Failed to import GPG secret key: %v\n", err)
			} else {
				fmt.Println("GPG secret key imported successfully.")
			}
		}

		var gpgArgs []string
		gpgArgs = append(gpgArgs, "--batch", "--yes")
		if gpgPass != "" {
			gpgArgs = append(gpgArgs, "--pinentry-mode", "loopback", "--passphrase", gpgPass)
		}
		if gpgKey != "" {
			gpgArgs = append(gpgArgs, "--local-user", gpgKey)
		}
		gpgArgs = append(gpgArgs, "--armor", "--detach-sign", target)

		err := RunCommand("gpg", gpgArgs...)
		if err != nil {
			fmt.Printf("WARNING: GPG signing failed: %v\n", err)
		} else {
			fmt.Println("Linux detached GPG signature successfully created!")
		}
	} else {
		fmt.Println("Skipping Linux GPG signing.")
	}

	// 4. Regenerate Checksums
	fmt.Println("Updating checksums.txt...")
	err := generateChecksums(binDir)
	CheckFatal(err, "Failed to generate checksums")

	fmt.Println("=== Client Signing Complete! ===")
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
		if e.IsDir() || e.Name() == "checksums.txt" || strings.HasSuffix(e.Name(), ".asc") || strings.HasSuffix(e.Name(), ".minisig") {
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
	err = os.WriteFile(checksumsPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	if err != nil {
		return err
	}
	fmt.Printf("Checksums updated in %s\n", checksumsPath)

	// Run Minisign helper if exists
	minisignHelper := filepath.Join("scripts", "minisign_helper.go")
	if fileExists(minisignHelper) {
		fmt.Println("Generating Minisign signature for checksums.txt...")
		err = RunCommand("go", "run", minisignHelper, checksumsPath, checksumsPath+".minisig")
		if err != nil {
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
