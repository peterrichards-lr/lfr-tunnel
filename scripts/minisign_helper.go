package main

import (
	"fmt"
	"os"

	"github.com/jedisct1/go-minisign"
)

const testSK = `untrusted comment: minisign encrypted secret key
RWQAAEIyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAOItWpGuGQbG4C9WXaxEYLgZ2xxuqfbuZmDgAhQ8Unot8t7SyxZ0nVh0gESesJ6Ay57fGFJ9T1ajVmanT7MFMCCDbPZ8uqDcSAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run minisign_helper.go <file_to_sign> <output_signature_file>")
		os.Exit(1)
	}

	content, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Printf("Failed to read file: %v\n", err)
		os.Exit(1)
	}

	sk, err := minisign.DecodePrivateKey(testSK)
	if err != nil {
		fmt.Printf("Failed to decode private key: %v\n", err)
		os.Exit(1)
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
