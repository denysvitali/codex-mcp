package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/denysvitali/codex-mcp/internal/codexcli"
	"github.com/denysvitali/codex-mcp/internal/config"
	"github.com/denysvitali/codex-mcp/internal/mcpserver"
)

var version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	defaults := config.Config{
		CodexBin:          "codex",
		DefaultYolo:       true,
		DefaultModel:      config.DefaultModel,
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: config.DefaultMaxConcurrentRuns,
		LogLevel:          config.DefaultLogLevel,
	}
	cliCfg := defaults

	rootCmd := &cobra.Command{
		Use:   "codex-mcp",
		Short: "Expose Codex exec as an MCP stdio server",
	}

	defaultConfigPath, err := config.DefaultConfigPath()
	if err == nil {
		defaults.ConfigPath = defaultConfigPath
		cliCfg.ConfigPath = defaultConfigPath
	}

	rootCmd.PersistentFlags().StringVar(&cliCfg.CodexBin, "codex-bin", cliCfg.CodexBin, "Path to the codex binary")
	rootCmd.PersistentFlags().StringVar(&cliCfg.Root, "root", "", "Primary allowed workspace root")
	rootCmd.PersistentFlags().StringSliceVar(&cliCfg.AllowDirs, "allow-dir", nil, "Additional allowed workspace directories")
	rootCmd.PersistentFlags().StringSliceVar(&cliCfg.AllowModels, "allow-model", nil, "Restrict the models callers may use to these slugs; repeatable or comma-separated (empty: all catalog models)")
	rootCmd.PersistentFlags().StringVar(&cliCfg.ConfigPath, "config", cliCfg.ConfigPath, "Path to config YAML file")
	rootCmd.PersistentFlags().BoolVar(&cliCfg.DefaultYolo, "default-yolo", cliCfg.DefaultYolo, "Enable unrestricted Codex execution by default")
	rootCmd.PersistentFlags().StringVar(&cliCfg.DefaultModel, "default-model", cliCfg.DefaultModel, "Default Codex model to use when requests do not specify one (empty: use the Codex CLI's own default)")
	rootCmd.PersistentFlags().StringVar(&cliCfg.DefaultReasoningEffort, "default-reasoning-effort", cliCfg.DefaultReasoningEffort, "Default reasoning effort when requests do not specify one (e.g. low, medium, high; empty: model's own default)")
	rootCmd.PersistentFlags().StringVar(&cliCfg.DefaultSandbox, "default-sandbox", cliCfg.DefaultSandbox, "Default sandbox to use when yolo is disabled")
	rootCmd.PersistentFlags().IntVar(&cliCfg.MaxConcurrentRuns, "max-concurrent-runs", cliCfg.MaxConcurrentRuns, "Maximum concurrent Codex runs")
	rootCmd.PersistentFlags().StringVar(&cliCfg.LogLevel, "log-level", cliCfg.LogLevel, "Log level: panic, fatal, error, warn, info, debug, trace")

	serveCmd := newServeCommand(defaults, &cliCfg)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	})

	rootCmd.RunE = serveCmd.RunE
	return rootCmd
}

func newServeCommand(defaults config.Config, cliCfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the MCP server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveConfig(cmd, defaults, *cliCfg)
			if err != nil {
				return err
			}

			if cfg.Root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("determine working directory: %w", err)
				}
				cfg.Root = wd
			}

			root, err := config.NormalizePath(cfg.Root)
			if err != nil {
				return err
			}
			cfg.Root = root

			allowDirs, err := config.NormalizePaths(cfg.AllowDirs)
			if err != nil {
				return err
			}
			cfg.AllowDirs = allowDirs
			cfg.AllowModels = config.NormalizeModels(cfg.AllowModels)

			if _, err := exec.LookPath(cfg.CodexBin); err != nil {
				return fmt.Errorf("resolve codex binary %q: %w", cfg.CodexBin, err)
			}
			if err := cfg.Validate(); err != nil {
				return err
			}

			logger := logrus.New()
			logger.SetOutput(os.Stderr)
			logger.SetFormatter(&logrus.JSONFormatter{})
			level, err := logrus.ParseLevel(cfg.LogLevel)
			if err != nil {
				return fmt.Errorf("parse log level: %w", err)
			}
			logger.SetLevel(level)

			runner := codexcli.NewRunner(codexcli.RunnerConfig{
				CodexBin:               cfg.CodexBin,
				Root:                   cfg.Root,
				AllowDirs:              cfg.AllowDirs,
				AllowModels:            cfg.AllowModels,
				DefaultYolo:            cfg.DefaultYolo,
				DefaultModel:           cfg.DefaultModel,
				DefaultReasoningEffort: cfg.DefaultReasoningEffort,
				DefaultSandbox:         cfg.DefaultSandbox,
				MaxConcurrentRuns:      cfg.MaxConcurrentRuns,
			}, logger)

			srv := mcpserver.Builder{
				Runner:                 runner,
				Logger:                 logger,
				Version:                version,
				DefaultModel:           cfg.DefaultModel,
				DefaultReasoningEffort: cfg.DefaultReasoningEffort,
			}.New()

			logger.WithFields(logrus.Fields{
				"root":                     cfg.Root,
				"allow_dirs":               cfg.AllowDirs,
				"allow_models":             cfg.AllowModels,
				"default_yolo":             cfg.DefaultYolo,
				"default_model":            cfg.DefaultModel,
				"default_reasoning_effort": cfg.DefaultReasoningEffort,
				"default_sandbox":          cfg.DefaultSandbox,
				"max_concurrent_runs":      cfg.MaxConcurrentRuns,
				"config_path":              cfg.ConfigPath,
			}).Info("starting MCP stdio server")

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx, &mcp.StdioTransport{})
		},
	}
}

func resolveConfig(cmd *cobra.Command, defaults, cliCfg config.Config) (config.Config, error) {
	cfg := defaults

	if cliCfg.ConfigPath != "" {
		fileCfg, err := config.LoadFile(cliCfg.ConfigPath)
		if err == nil {
			cfg.ApplyFile(fileCfg)
		} else if !os.IsNotExist(err) {
			return config.Config{}, err
		}
	}

	cfg.ConfigPath = cliCfg.ConfigPath
	applyChangedFlags(cmd, &cfg, cliCfg)
	return cfg, nil
}

func applyChangedFlags(cmd *cobra.Command, cfg *config.Config, cliCfg config.Config) {
	if cmd.Flags().Changed("codex-bin") {
		cfg.CodexBin = cliCfg.CodexBin
	}
	if cmd.Flags().Changed("root") {
		cfg.Root = cliCfg.Root
	}
	if cmd.Flags().Changed("allow-dir") {
		cfg.AllowDirs = append([]string(nil), cliCfg.AllowDirs...)
	}
	if cmd.Flags().Changed("allow-model") {
		cfg.AllowModels = append([]string(nil), cliCfg.AllowModels...)
	}
	if cmd.Flags().Changed("config") {
		cfg.ConfigPath = cliCfg.ConfigPath
	}
	if cmd.Flags().Changed("default-yolo") {
		cfg.DefaultYolo = cliCfg.DefaultYolo
	}
	if cmd.Flags().Changed("default-model") {
		cfg.DefaultModel = cliCfg.DefaultModel
	}
	if cmd.Flags().Changed("default-reasoning-effort") {
		cfg.DefaultReasoningEffort = cliCfg.DefaultReasoningEffort
	}
	if cmd.Flags().Changed("default-sandbox") {
		cfg.DefaultSandbox = cliCfg.DefaultSandbox
	}
	if cmd.Flags().Changed("max-concurrent-runs") {
		cfg.MaxConcurrentRuns = cliCfg.MaxConcurrentRuns
	}
	if cmd.Flags().Changed("log-level") {
		cfg.LogLevel = cliCfg.LogLevel
	}
}
