package agent

import (
	"context"
	"testing"

	"github.com/minicodemonkey/chief/internal/loop"
)

func TestOpenCodeProvider_Name(t *testing.T) {
	p := NewOpenCodeProvider("")
	if p.Name() != "OpenCode" {
		t.Errorf("Name() = %q, want OpenCode", p.Name())
	}
}

func TestOpenCodeProvider_CLIPath(t *testing.T) {
	p := NewOpenCodeProvider("")
	if p.CLIPath() != "opencode" {
		t.Errorf("CLIPath() empty arg = %q, want opencode", p.CLIPath())
	}
	p2 := NewOpenCodeProvider("/usr/local/bin/opencode")
	if p2.CLIPath() != "/usr/local/bin/opencode" {
		t.Errorf("CLIPath() custom = %q, want /usr/local/bin/opencode", p2.CLIPath())
	}
}

func TestOpenCodeProvider_LogFileName(t *testing.T) {
	p := NewOpenCodeProvider("")
	if p.LogFileName() != "opencode.log" {
		t.Errorf("LogFileName() = %q, want opencode.log", p.LogFileName())
	}
}

func TestOpenCodeProvider_LoopCommand(t *testing.T) {
	ctx := context.Background()
	p := NewOpenCodeProvider("/bin/opencode")
	cmd := p.LoopCommand(ctx, "hello world", "/work/dir")

	if cmd.Path != "/bin/opencode" {
		t.Errorf("LoopCommand Path = %q, want /bin/opencode", cmd.Path)
	}
	wantArgs := []string{"/bin/opencode", "run", "--format", "json", "hello world"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("LoopCommand Args len = %d, want %d: %v", len(cmd.Args), len(wantArgs), cmd.Args)
	}
	for i, w := range wantArgs {
		if cmd.Args[i] != w {
			t.Errorf("LoopCommand Args[%d] = %q, want %q", i, cmd.Args[i], w)
		}
	}
	if cmd.Dir != "/work/dir" {
		t.Errorf("LoopCommand Dir = %q, want /work/dir", cmd.Dir)
	}
}

func TestOpenCodeProvider_ConvertCommand(t *testing.T) {
	p := NewOpenCodeProvider("opencode")
	cmd, mode, outPath, err := p.ConvertCommand("/prd/dir", "convert prompt")
	if err != nil {
		t.Fatalf("ConvertCommand unexpected error: %v", err)
	}
	if mode != loop.OutputStdout {
		t.Errorf("ConvertCommand mode = %v, want OutputStdout", mode)
	}
	if outPath != "" {
		t.Errorf("ConvertCommand outPath = %q, want empty string", outPath)
	}
	if cmd.Path != "opencode" {
		t.Errorf("ConvertCommand Path = %q, want opencode", cmd.Path)
	}
	if cmd.Dir != "/prd/dir" {
		t.Errorf("ConvertCommand Dir = %q, want /prd/dir", cmd.Dir)
	}
}

func TestOpenCodeProvider_FixJSONCommand(t *testing.T) {
	p := NewOpenCodeProvider("opencode")
	cmd, mode, outPath, err := p.FixJSONCommand("fix prompt")
	if err != nil {
		t.Fatalf("FixJSONCommand unexpected error: %v", err)
	}
	if mode != loop.OutputStdout {
		t.Errorf("FixJSONCommand mode = %v, want OutputStdout", mode)
	}
	if outPath != "" {
		t.Errorf("FixJSONCommand outPath = %q, want empty string", outPath)
	}
}

func TestOpenCodeProvider_InteractiveCommand(t *testing.T) {
	p := NewOpenCodeProvider("opencode")
	cmd := p.InteractiveCommand("/work", "my prompt")
	if cmd.Dir != "/work" {
		t.Errorf("InteractiveCommand Dir = %q, want /work", cmd.Dir)
	}
}

func TestOpenCodeProvider_ParseLine(t *testing.T) {
	p := NewOpenCodeProvider("")
	e := p.ParseLine(`{"type":"step_start","timestamp":1234567890,"sessionID":"ses_test123"}`)
	if e == nil {
		t.Fatal("ParseLine(step_start) returned nil")
	}
	if e.Type != loop.EventIterationStart {
		t.Errorf("ParseLine(step_start) Type = %v, want EventIterationStart", e.Type)
	}
}
