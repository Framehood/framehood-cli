package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/config"
)

func TestConfigSetGet_OutputDir(t *testing.T) {
	cfg := config.Config{MCPBase: "https://mcp.framehood.ai", ConfigDir: t.TempDir()}
	target := filepath.Join(t.TempDir(), "out")

	// `config set output-dir <path>`
	setCmd := newConfigCmd(cfg)
	var setOut bytes.Buffer
	setCmd.SetOut(&setOut)
	setCmd.SetErr(&setOut)
	setCmd.SetArgs([]string{"set", "output-dir", target})
	if err := setCmd.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if !strings.Contains(setOut.String(), "output dir → ") || !strings.Contains(setOut.String(), target) {
		t.Errorf("set output = %q, want 'output dir → %s'", setOut.String(), target)
	}
	if got := cfg.OutputDir(); got != target {
		t.Errorf("after set, OutputDir = %q, want %q", got, target)
	}

	// `config get` prints the output dir, config dir, and MCP base.
	getCmd := newConfigCmd(cfg)
	var getOut bytes.Buffer
	getCmd.SetOut(&getOut)
	getCmd.SetErr(&getOut)
	getCmd.SetArgs([]string{"get"})
	if err := getCmd.Execute(); err != nil {
		t.Fatalf("config get: %v", err)
	}
	out := getOut.String()
	for _, want := range []string{target, cfg.ConfigDir, cfg.MCPBase} {
		if !strings.Contains(out, want) {
			t.Errorf("config get output missing %q:\n%s", want, out)
		}
	}
}

func TestConfigGet_UnsetShowsCwd(t *testing.T) {
	cfg := config.Config{MCPBase: "x", ConfigDir: t.TempDir()}
	cmd := newConfigCmd(cfg)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config get: %v", err)
	}
	if !strings.Contains(out.String(), "current working directory") {
		t.Errorf("unset output dir should show cwd hint:\n%s", out.String())
	}
}

func TestConfigSet_RejectsUnknownKey(t *testing.T) {
	cfg := config.Config{MCPBase: "x", ConfigDir: t.TempDir()}
	cmd := newConfigCmd(cfg)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"set", "nope", "value"})
	if err := cmd.Execute(); err == nil {
		t.Error("config set with an unknown key should error")
	}
}

func TestConfigSet_RejectsNonDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MCPBase: "x", ConfigDir: t.TempDir()}
	cmd := newConfigCmd(cfg)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"set", "output-dir", file})
	if err := cmd.Execute(); err == nil {
		t.Error("config set output-dir on a file (not a dir) should error")
	}
}
