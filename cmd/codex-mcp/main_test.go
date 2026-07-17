package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/denysvitali/codex-mcp/internal/config"
)

func TestResolveConfigFlagsOverrideFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
codex_bin: /usr/bin/from-config
root: /tmp/from-config
allow_dirs:
  - /tmp/config-extra
default:
  yolo: false
  model: gpt-config
  reasoning_effort: low
  sandbox: read-only
max_concurrent_runs: 2
log_level: debug
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	defaults := config.Config{
		CodexBin:          "codex",
		DefaultYolo:       true,
		DefaultModel:      config.DefaultModel,
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: config.DefaultMaxConcurrentRuns,
		LogLevel:          config.DefaultLogLevel,
		ConfigPath:        configPath,
	}
	cliCfg := defaults

	cmd := &cobra.Command{Use: "serve"}
	cmd.Flags().StringVar(&cliCfg.CodexBin, "codex-bin", cliCfg.CodexBin, "")
	cmd.Flags().StringVar(&cliCfg.Root, "root", cliCfg.Root, "")
	cmd.Flags().StringSliceVar(&cliCfg.AllowDirs, "allow-dir", cliCfg.AllowDirs, "")
	cmd.Flags().StringVar(&cliCfg.ConfigPath, "config", cliCfg.ConfigPath, "")
	cmd.Flags().BoolVar(&cliCfg.DefaultYolo, "default-yolo", cliCfg.DefaultYolo, "")
	cmd.Flags().StringVar(&cliCfg.DefaultModel, "default-model", cliCfg.DefaultModel, "")
	cmd.Flags().StringVar(&cliCfg.DefaultReasoningEffort, "default-reasoning-effort", cliCfg.DefaultReasoningEffort, "")
	cmd.Flags().StringVar(&cliCfg.DefaultSandbox, "default-sandbox", cliCfg.DefaultSandbox, "")
	cmd.Flags().IntVar(&cliCfg.MaxConcurrentRuns, "max-concurrent-runs", cliCfg.MaxConcurrentRuns, "")
	cmd.Flags().StringVar(&cliCfg.LogLevel, "log-level", cliCfg.LogLevel, "")

	args := []string{
		"--config", configPath,
		"--codex-bin", "/usr/bin/from-cli",
		"--default-model", "gpt-cli",
		"--default-reasoning-effort", "high",
		"--default-yolo=true",
		"--allow-dir", "/tmp/cli-extra",
	}
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	cfg, err := resolveConfig(cmd, defaults, cliCfg)
	if err != nil {
		t.Fatalf("resolveConfig() error = %v", err)
	}

	if cfg.CodexBin != "/usr/bin/from-cli" {
		t.Fatalf("expected codex-bin from CLI, got %q", cfg.CodexBin)
	}
	if cfg.DefaultModel != "gpt-cli" {
		t.Fatalf("expected default-model from CLI, got %q", cfg.DefaultModel)
	}
	if cfg.DefaultReasoningEffort != "high" {
		t.Fatalf("expected default-reasoning-effort from CLI, got %q", cfg.DefaultReasoningEffort)
	}
	if !cfg.DefaultYolo {
		t.Fatalf("expected default-yolo from CLI to override config file")
	}
	if len(cfg.AllowDirs) != 1 || cfg.AllowDirs[0] != "/tmp/cli-extra" {
		t.Fatalf("expected allow-dir from CLI, got %+v", cfg.AllowDirs)
	}
	if cfg.Root != "/tmp/from-config" {
		t.Fatalf("expected root from config file, got %q", cfg.Root)
	}
	if cfg.DefaultSandbox != "read-only" || cfg.MaxConcurrentRuns != 2 || cfg.LogLevel != "debug" {
		t.Fatalf("expected remaining values from config file, got %+v", cfg)
	}
}
