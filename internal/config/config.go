package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultMaxConcurrentRuns = 4
	DefaultLogLevel          = "info"
	DefaultModel             = "gpt-5.4"
)

type Config struct {
	CodexBin          string
	Root              string
	AllowDirs         []string
	DefaultYolo       bool
	DefaultModel      string
	DefaultSandbox    string
	MaxConcurrentRuns int
	LogLevel          string
	ConfigPath        string
}

func (c Config) Validate() error {
	if c.CodexBin == "" {
		return errors.New("codex binary path is required")
	}
	if c.Root == "" {
		return errors.New("root path is required")
	}
	if c.MaxConcurrentRuns <= 0 {
		return fmt.Errorf("max concurrent runs must be positive: %d", c.MaxConcurrentRuns)
	}

	switch c.DefaultSandbox {
	case "", "read-only", "workspace-write", "danger-full-access":
	default:
		return fmt.Errorf("invalid default sandbox: %s", c.DefaultSandbox)
	}

	return nil
}

type FileConfig struct {
	CodexBin          string   `yaml:"codex_bin"`
	Root              string   `yaml:"root"`
	AllowDirs         []string `yaml:"allow_dirs"`
	Default           Defaults `yaml:"default"`
	MaxConcurrentRuns int      `yaml:"max_concurrent_runs"`
	LogLevel          string   `yaml:"log_level"`
}

type Defaults struct {
	Yolo    *bool  `yaml:"yolo"`
	Model   string `yaml:"model"`
	Sandbox string `yaml:"sandbox"`
}

func DefaultConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(configDir, "codex-mcp", "config.yaml"), nil
}

func LoadFile(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}

	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) ApplyFile(fileCfg FileConfig) {
	if fileCfg.CodexBin != "" {
		c.CodexBin = fileCfg.CodexBin
	}
	if fileCfg.Root != "" {
		c.Root = fileCfg.Root
	}
	if len(fileCfg.AllowDirs) > 0 {
		c.AllowDirs = append([]string(nil), fileCfg.AllowDirs...)
	}
	if fileCfg.Default.Yolo != nil {
		c.DefaultYolo = *fileCfg.Default.Yolo
	}
	if fileCfg.Default.Model != "" {
		c.DefaultModel = fileCfg.Default.Model
	}
	if fileCfg.Default.Sandbox != "" {
		c.DefaultSandbox = fileCfg.Default.Sandbox
	}
	if fileCfg.MaxConcurrentRuns > 0 {
		c.MaxConcurrentRuns = fileCfg.MaxConcurrentRuns
	}
	if fileCfg.LogLevel != "" {
		c.LogLevel = fileCfg.LogLevel
	}
}

func NormalizePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	expanded := path
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		expanded = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("normalize path %q: %w", path, err)
	}

	clean := filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("eval symlinks for path %q: %w", clean, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat path %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", resolved)
	}

	return resolved, nil
}

func NormalizePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		normalized, err := NormalizePath(path)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}
