// Command controlplane-gen writes the generated TypeScript wire contract.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

func main() {
	output := flag.String("out", "", "generated TypeScript output path")
	flag.Parse()
	if *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: controlplane-gen -out PATH")
		os.Exit(2)
	}
	if err := run(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("controlplane-gen: create output directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".protocol-*.ts")
	if err != nil {
		return fmt.Errorf("controlplane-gen: create temporary output: %w", err)
	}
	temporaryName := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryName) // Best-effort cleanup after generation failure.
		}
	}()
	if err := controlplane.GenerateTypeScript(temporary); err != nil {
		_ = temporary.Close() // Best-effort cleanup after generation failure.
		return fmt.Errorf("controlplane-gen: generate: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close() // Best-effort cleanup after fsync failure.
		return fmt.Errorf("controlplane-gen: sync: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("controlplane-gen: close: %w", err)
	}
	if err := os.Rename(temporaryName, output); err != nil {
		return fmt.Errorf("controlplane-gen: replace output: %w", err)
	}
	cleanup = false
	return nil
}
