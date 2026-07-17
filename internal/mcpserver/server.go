package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/denysvitali/codex-mcp/internal/codexcli"
)

type Builder struct {
	Runner runner
	Logger *logrus.Logger
	// Version is reported to MCP clients during initialization.
	Version string
	// DefaultModel is the model codex_exec falls back to when a request does
	// not specify one. Empty means the Codex CLI's own default model.
	DefaultModel string
	// DefaultReasoningEffort is the reasoning effort codex_exec falls back to
	// when a request does not specify one. Empty means the Codex CLI's own
	// default for the selected model.
	DefaultReasoningEffort string
}

type runner interface {
	Run(context.Context, codexcli.RunRequest) (codexcli.RunResult, error)
	ListModels(context.Context) ([]codexcli.Model, error)
}

type CodexExecInput struct {
	Prompt           string         `json:"prompt" jsonschema:"Instructions to send to Codex."`
	Cwd              string         `json:"cwd,omitempty" jsonschema:"Working directory for the Codex run. Relative paths resolve from the server root."`
	ThreadID         string         `json:"thread_id,omitempty" jsonschema:"Existing Codex thread ID to resume."`
	Model            string         `json:"model,omitempty" jsonschema:"Codex model to run. Call the codex_list_models tool to discover the available models; if the server restricts the selectable models, codex_list_models only reports those. If omitted, the server default model is used."`
	ReasoningEffort  string         `json:"reasoning_effort,omitempty" jsonschema:"Reasoning effort for the run, e.g. low, medium, high or xhigh. The codex_list_models tool reports which levels each model supports. If omitted, the server default is used."`
	Profile          string         `json:"profile,omitempty" jsonschema:"Optional Codex profile override."`
	OutputSchema     map[string]any `json:"output_schema,omitempty" jsonschema:"Optional JSON Schema object describing the desired final-message output shape."`
	Sandbox          string         `json:"sandbox,omitempty" jsonschema:"Sandbox mode used only when yolo is disabled in the server config. One of: read-only, workspace-write, danger-full-access."`
	TimeoutMS        int            `json:"timeout_ms,omitempty" jsonschema:"Optional per-run timeout in milliseconds. The run is cancelled if the deadline is reached."`
	SkipGitRepoCheck *bool          `json:"skip_git_repo_check,omitempty" jsonschema:"Override automatic git repository detection."`
}

type ListModelsInput struct{}

type ListModelsResult struct {
	Models []codexcli.Model `json:"models"`
	// DefaultModel is the model codex_exec uses when the request does not
	// specify one. Empty means the Codex CLI picks its own default.
	DefaultModel string `json:"default_model,omitempty"`
	// DefaultReasoningEffort is the reasoning effort codex_exec uses when the
	// request does not specify one. Empty means the Codex CLI picks the
	// model's own default.
	DefaultReasoningEffort string `json:"default_reasoning_effort,omitempty"`
}

func (b Builder) New() *mcp.Server {
	version := b.Version
	if version == "" {
		version = "dev"
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "codex-mcp",
		Title:   "Codex MCP",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Delegate coding tasks to the Codex CLI. Call codex_exec to run a task non-interactively, and codex_list_models to discover which Codex models are available for the model argument of codex_exec.",
	})

	srv.AddReceivingMiddleware(b.errorLoggingMiddleware())

	mcp.AddTool(srv, &mcp.Tool{
		Name: "codex_exec",
		Description: "Run Codex non-interactively and return the final assistant message plus execution metadata. " +
			"Call codex_list_models first to discover the models available for the model argument.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Codex",
			DestructiveHint: new(true),
			OpenWorldHint:   new(true),
		},
	}, b.handleCodexExec)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "codex_list_models",
		Description: "List the Codex models available on this server so you can choose one for the model argument of codex_exec.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "List Codex models",
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: new(false),
			OpenWorldHint:   new(false),
		},
	}, b.handleListModels)

	return srv
}

func (b Builder) handleCodexExec(ctx context.Context, _ *mcp.CallToolRequest, args CodexExecInput) (*mcp.CallToolResult, codexcli.RunResult, error) {
	if strings.TrimSpace(args.Prompt) == "" {
		return nil, codexcli.RunResult{}, fmt.Errorf("prompt is required")
	}
	if args.TimeoutMS < 0 {
		return nil, codexcli.RunResult{}, fmt.Errorf("timeout_ms must be non-negative: %d", args.TimeoutMS)
	}
	if args.Sandbox != "" && args.Sandbox != "read-only" && args.Sandbox != "workspace-write" && args.Sandbox != "danger-full-access" {
		return nil, codexcli.RunResult{}, fmt.Errorf("invalid sandbox value: %s", args.Sandbox)
	}
	for _, ch := range args.ReasoningEffort {
		if ch < 'a' || ch > 'z' {
			return nil, codexcli.RunResult{}, fmt.Errorf("invalid reasoning_effort value: %s", args.ReasoningEffort)
		}
	}

	result, err := b.Runner.Run(ctx, codexcli.RunRequest{
		Prompt:           args.Prompt,
		Cwd:              args.Cwd,
		ThreadID:         args.ThreadID,
		Model:            args.Model,
		ReasoningEffort:  args.ReasoningEffort,
		Profile:          args.Profile,
		OutputSchema:     args.OutputSchema,
		Sandbox:          args.Sandbox,
		TimeoutMS:        args.TimeoutMS,
		SkipGitRepoCheck: args.SkipGitRepoCheck,
	})
	if err != nil {
		return nil, codexcli.RunResult{}, err
	}

	if args.OutputSchema != nil {
		var parsed any
		if err := json.Unmarshal([]byte(strings.TrimSpace(result.FinalMessage)), &parsed); err == nil {
			result.StructuredOutput = &parsed
		}
	}

	return nil, result, nil
}

func (b Builder) handleListModels(ctx context.Context, _ *mcp.CallToolRequest, _ ListModelsInput) (*mcp.CallToolResult, ListModelsResult, error) {
	models, err := b.Runner.ListModels(ctx)
	if err != nil {
		return nil, ListModelsResult{}, err
	}
	return nil, ListModelsResult{
		Models:                 models,
		DefaultModel:           b.DefaultModel,
		DefaultReasoningEffort: b.DefaultReasoningEffort,
	}, nil
}

// errorLoggingMiddleware logs protocol-level request failures. Tool-level
// errors are already logged by the runner with richer context.
func (b Builder) errorLoggingMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				b.Logger.WithFields(logrus.Fields{
					"method": method,
					"error":  err.Error(),
				}).Error("mcp request failed")
			}
			return result, err
		}
	}
}
