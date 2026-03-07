package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/minicodemonkey/chief/embed"
	"github.com/minicodemonkey/chief/internal/loop"
	"github.com/minicodemonkey/chief/internal/prd"
)

// waitFunc is the signature for prd.WaitWithPanel / prd.WaitWithSpinner.
type waitFunc func(cmd *exec.Cmd, title, message string, stderr *bytes.Buffer) error

// runAgentCommand runs a provider command, captures output (stdout or file), and
// displays a progress indicator via the supplied wait function.
func runAgentCommand(
	providerName string,
	cmd *exec.Cmd,
	mode loop.OutputMode,
	outPath string,
	wait waitFunc,
	title, activity string,
) (string, error) {
	var stdout, stderr bytes.Buffer
	if mode == loop.OutputStdout {
		cmd.Stdout = &stdout
	} else {
		cmd.Stdout = &bytes.Buffer{}
	}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		if outPath != "" {
			_ = os.Remove(outPath)
		}
		return "", fmt.Errorf("failed to start %s: %w", providerName, err)
	}
	if err := wait(cmd, title, activity, &stderr); err != nil {
		if outPath != "" {
			_ = os.Remove(outPath)
		}
		return "", err
	}
	if mode == loop.OutputFromFile && outPath != "" {
		defer os.Remove(outPath)
		data, err := os.ReadFile(outPath)
		if err != nil {
			return "", fmt.Errorf("failed to read output from %s: %w", outPath, err)
		}
		return string(data), nil
	}
	return stdout.String(), nil
}

// runConversionWithProvider runs the agent to convert prd.md to JSON.
func runConversionWithProvider(provider loop.Provider, absPRDDir string) (string, error) {
	prompt := embed.GetConvertPrompt(filepath.Join(absPRDDir, "prd.md"), "US")
	cmd, mode, outPath, err := provider.ConvertCommand(absPRDDir, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to prepare conversion command: %w", err)
	}
	return runAgentCommand(provider.Name(), cmd, mode, outPath, prd.WaitWithPanel, "Converting PRD", "Analyzing PRD...")
}

// runFixJSONWithProvider runs the agent to fix invalid JSON.
func runFixJSONWithProvider(provider loop.Provider, prompt string) (string, error) {
	cmd, mode, outPath, err := provider.FixJSONCommand(prompt)
	if err != nil {
		return "", fmt.Errorf("failed to prepare fix command: %w", err)
	}
	return runAgentCommand(provider.Name(), cmd, mode, outPath, prd.WaitWithSpinner, "Fixing JSON", "Fixing prd.json...")
}
