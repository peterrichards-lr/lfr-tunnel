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
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops sign")
		fmt.Println("\nSigns dist/'s built binaries: macOS via codesign (LFT_MACOS_IDENTITY),")
		fmt.Println("Windows via osslsigncode (LFT_SIGN_KEY/LFT_SIGN_CRT or LFT_SIGN_P12, plus")
		fmt.Println("LFT_SIGN_PASS), and Linux via a detached GPG signature (LFT_GPG_KEY,")
		fmt.Println("LFT_GPG_SECRET, LFT_GPG_PASS). Any step is skipped if its env vars are")
		fmt.Println("unset. Regenerates dist/checksums.txt and minisign-signs it.")
		return
	}

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
	if gpgPass == "none" || gpgPass == "skip" {
		gpgPass = ""
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
			args = append(args, "-pass", signPass)
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

		for _, arch := range []string{"amd64", "arm64"} {
			fmt.Printf("Generating Linux detached GPG signature for %s...\n", arch)
			target := filepath.Join(binDir, fmt.Sprintf("lfr-tunnel-linux-%s", arch))
			sigPath := target + ".asc"
			os.Remove(sigPath)

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
				fmt.Printf("WARNING: GPG signing failed for %s: %v\n", arch, err)
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
