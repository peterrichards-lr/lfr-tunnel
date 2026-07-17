package main

import (
	"fmt"
	"os"

	"lfr-tunnel/pkg/ops"
)

func printUsage() {
	fmt.Println("lfr-tunnel-ops - Administrative CLI for Liferay Tunnel Operations")
	fmt.Println("\nUsage:")
	fmt.Println("  lfr-tunnel-ops <command> [arguments]")
	fmt.Println("\nCommands:")
	fmt.Println("  build       Build cross-platform client binaries")
	fmt.Println("  sign        Sign client binaries for macOS, Windows, and Linux")
	fmt.Println("  deploy      Deploy server changes to the VPS")
	fmt.Println("  maintenance Enable or disable maintenance mode on the VPS")
	fmt.Println("  diagnose    Run diagnostic checks on the gateway VPS")
	fmt.Println("  help        Print this help message")
	fmt.Println("\nUse 'lfr-tunnel-ops <command> -help' for more information about a command.")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "build":
		ops.BuildCommand(args)
	case "sign":
		ops.SignCommand(args)
	case "deploy":
		ops.DeployCommand(args)
	case "deploy-clients":
		ops.DeployClientsCommand(args)
	case "maintenance":
		ops.MaintenanceCommand(args)
	case "diagnose":
		ops.DiagnoseCommand(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %q\n", command)
		printUsage()
		os.Exit(1)
	}
}
