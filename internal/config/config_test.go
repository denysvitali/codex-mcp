package config

import (
	"os"
	"reflect"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyFile(t *testing.T) {
	t.Parallel()

	cfg := Config{
		CodexBin:          "codex",
		DefaultYolo:       true,
		DefaultModel:      DefaultModel,
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 4,
		LogLevel:          "info",
	}

	no := false
	cfg.ApplyFile(FileConfig{
		CodexBin:  "/usr/local/bin/codex",
		Root:      "/tmp/root",
		AllowDirs: []string{"/tmp/extra"},
		Default: Defaults{
			Yolo:            &no,
			Model:           "gpt-5.5",
			ReasoningEffort: "high",
			Sandbox:         "read-only",
		},
		MaxConcurrentRuns: 2,
		LogLevel:          "debug",
	})

	if cfg.CodexBin != "/usr/local/bin/codex" || cfg.Root != "/tmp/root" {
		t.Fatalf("config file values not applied: %+v", cfg)
	}
	if cfg.DefaultYolo {
		t.Fatalf("expected default_yolo=false after file apply")
	}
	if cfg.DefaultModel != "gpt-5.5" || cfg.DefaultReasoningEffort != "high" || cfg.DefaultSandbox != "read-only" || cfg.MaxConcurrentRuns != 2 || cfg.LogLevel != "debug" {
		t.Fatalf("unexpected config after file apply: %+v", cfg)
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("default:\n  yolo: false\n  model: gpt-5.5\nroot: /tmp/work\nallow_dirs:\n  - /tmp/extra\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fileCfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if fileCfg.Default.Yolo == nil || *fileCfg.Default.Yolo {
		t.Fatalf("expected default.yolo=false, got %+v", fileCfg.Default.Yolo)
	}
	if fileCfg.Default.Model != "gpt-5.5" || fileCfg.Root != "/tmp/work" || len(fileCfg.AllowDirs) != 1 || fileCfg.AllowDirs[0] != "/tmp/extra" {
		t.Fatalf("unexpected file config: %+v", fileCfg)
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	valid := Config{
		CodexBin:          "/usr/bin/codex",
		Root:              "/tmp/root",
		MaxConcurrentRuns: 1,
	}

	tests := []struct {
		name    string
		mutate  func(Config) Config
		wantErr string
	}{
		{
			name: "valid",
			mutate: func(cfg Config) Config {
				return cfg
			},
		},
		{
			name: "missing codex bin",
			mutate: func(cfg Config) Config {
				cfg.CodexBin = ""
				return cfg
			},
			wantErr: "codex binary path is required",
		},
		{
			name: "missing root",
			mutate: func(cfg Config) Config {
				cfg.Root = ""
				return cfg
			},
			wantErr: "root path is required",
		},
		{
			name: "invalid concurrency",
			mutate: func(cfg Config) Config {
				cfg.MaxConcurrentRuns = 0
				return cfg
			},
			wantErr: "max concurrent runs must be positive: 0",
		},
		{
			name: "invalid sandbox",
			mutate: func(cfg Config) Config {
				cfg.DefaultSandbox = "sandboxed"
				return cfg
			},
			wantErr: "invalid default sandbox: sandboxed",
		},
		{
			name: "valid empty sandbox",
			mutate: func(cfg Config) Config {
				cfg.DefaultSandbox = ""
				return cfg
			},
		},
		{
			name: "valid read-only sandbox",
			mutate: func(cfg Config) Config {
				cfg.DefaultSandbox = "read-only"
				return cfg
			},
		},
		{
			name: "valid workspace-write sandbox",
			mutate: func(cfg Config) Config {
				cfg.DefaultSandbox = "workspace-write"
				return cfg
			},
		},
		{
			name: "valid danger-full-access sandbox",
			mutate: func(cfg Config) Config {
				cfg.DefaultSandbox = "danger-full-access"
				return cfg
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.mutate(valid).Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("expected %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	filePath := filepath.Join(base, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	home := filepath.Join(base, "home")
	homeProjects := filepath.Join(home, "projects")
	if err := os.MkdirAll(homeProjects, 0o755); err != nil {
		t.Fatalf("mkdir home projects: %v", err)
	}
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "empty path",
			input: "",
			want:  "",
		},
		{
			name:  "valid dir",
			input: target,
			want:  target,
		},
		{
			name:  "symlink resolves to dir",
			input: link,
			want:  target,
		},
		{
			name:  "tilde expansion",
			input: "~/projects",
			want:  homeProjects,
		},
		{
			name:    "missing path",
			input:   filepath.Join(base, "missing"),
			wantErr: "eval symlinks for path",
		},
		{
			name:    "not a directory",
			input:   filePath,
			wantErr: "path is not a directory",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePath(tt.input)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NormalizePath() error = %v", err)
				}
				if got != tt.want {
					t.Fatalf("expected %q, got %q", tt.want, got)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestNormalizePaths(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr string
	}{
		{
			name:  "empty input",
			input: nil,
		},
		{
			name:  "deduplicates normalized paths",
			input: []string{target, link, target},
			want:  []string{target},
		},
		{
			name:  "preserves unique normalized dirs",
			input: []string{target, other},
			want:  []string{target, other},
		},
		{
			name:    "returns first normalization error",
			input:   []string{target, filepath.Join(base, "missing")},
			wantErr: "eval symlinks for path",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizePaths(tt.input)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NormalizePaths() error = %v", err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("expected %v, got %v", tt.want, got)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
