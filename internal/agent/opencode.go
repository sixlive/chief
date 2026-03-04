package agent

import (
	"context"
	"os/exec"
	"strings"

	"github.com/minicodemonkey/chief/internal/loop"
)

type OpenCodeProvider struct {
	cliPath string
}

func NewOpenCodeProvider(cliPath string) *OpenCodeProvider {
	if cliPath == "" {
		cliPath = "opencode"
	}
	return &OpenCodeProvider{cliPath: cliPath}
}

func (p *OpenCodeProvider) Name() string { return "OpenCode" }

func (p *OpenCodeProvider) CLIPath() string { return p.cliPath }

func (p *OpenCodeProvider) LoopCommand(ctx context.Context, prompt, workDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, p.cliPath, "run", "--format", "json", prompt)
	cmd.Dir = workDir
	return cmd
}

func (p *OpenCodeProvider) InteractiveCommand(workDir, prompt string) *exec.Cmd {
	cmd := exec.Command(p.cliPath)
	cmd.Dir = workDir
	return cmd
}

func (p *OpenCodeProvider) ConvertCommand(workDir, prompt string) (*exec.Cmd, loop.OutputMode, string, error) {
	cmd := exec.Command(p.cliPath, "run", "--format", "json", "--", prompt)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	return cmd, loop.OutputStdout, "", nil
}

func (p *OpenCodeProvider) FixJSONCommand(prompt string) (*exec.Cmd, loop.OutputMode, string, error) {
	cmd := exec.Command(p.cliPath, "run", "--format", "json", "--", prompt)
	cmd.Stdin = strings.NewReader(prompt)
	return cmd, loop.OutputStdout, "", nil
}

func (p *OpenCodeProvider) ParseLine(line string) *loop.Event {
	return loop.ParseLineOpenCode(line)
}

func (p *OpenCodeProvider) LogFileName() string { return "opencode.log" }
