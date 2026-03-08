package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"

	"github.com/denysvitali/codex-mcp/internal/codexcli"
)

type Builder struct {
	Runner runner
	Logger *logrus.Logger
}

type runner interface {
	Run(context.Context, codexcli.RunRequest) (codexcli.RunResult, error)
}

type CodexExecInput struct {
	Prompt           string `json:"prompt" jsonschema:"required" jsonschema_description:"Instructions to send to Codex."`
	Cwd              string `json:"cwd,omitempty" jsonschema_description:"Working directory for the Codex run. Relative paths resolve from the server root."`
	ThreadID         string `json:"thread_id,omitempty" jsonschema_description:"Existing Codex thread ID to resume."`
	Model            string `json:"model,omitempty" jsonschema_description:"Optional Codex model override."`
	Profile          string `json:"profile,omitempty" jsonschema_description:"Optional Codex profile override."`
	Sandbox          string `json:"sandbox,omitempty" jsonschema_description:"Sandbox mode used only when yolo is disabled in the server config." jsonschema:"enum=read-only,enum=workspace-write,enum=danger-full-access"`
	TimeoutMS        int    `json:"timeout_ms,omitempty" jsonschema_description:"Optional per-run timeout in milliseconds. The run is cancelled if the deadline is reached."`
	SkipGitRepoCheck *bool  `json:"skip_git_repo_check,omitempty" jsonschema_description:"Override automatic git repository detection."`
}

func (b Builder) New() *server.MCPServer {
	hooks := &server.Hooks{}
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		b.Logger.WithFields(logrus.Fields{
			"method": string(method),
			"id":     id,
			"error":  err.Error(),
		}).Error("mcp request failed")
	})

	taskHooks := &server.TaskHooks{}
	taskHooks.AddOnTaskCreated(func(ctx context.Context, metrics server.TaskMetrics) {
		b.Logger.WithFields(logrus.Fields{
			"task_id": metrics.TaskID,
			"tool":    metrics.ToolName,
			"status":  metrics.Status,
		}).Info("task created")
	})
	taskHooks.AddOnTaskCompleted(func(ctx context.Context, metrics server.TaskMetrics) {
		b.Logger.WithFields(logrus.Fields{
			"task_id":     metrics.TaskID,
			"tool":        metrics.ToolName,
			"status":      metrics.Status,
			"duration_ms": metrics.Duration.Milliseconds(),
		}).Info("task completed")
	})
	taskHooks.AddOnTaskFailed(func(ctx context.Context, metrics server.TaskMetrics) {
		b.Logger.WithFields(logrus.Fields{
			"task_id":     metrics.TaskID,
			"tool":        metrics.ToolName,
			"status":      metrics.Status,
			"duration_ms": metrics.Duration.Milliseconds(),
			"error":       fmt.Sprint(metrics.Error),
		}).Warn("task failed")
	})
	taskHooks.AddOnTaskCancelled(func(ctx context.Context, metrics server.TaskMetrics) {
		b.Logger.WithFields(logrus.Fields{
			"task_id":     metrics.TaskID,
			"tool":        metrics.ToolName,
			"status":      metrics.Status,
			"duration_ms": metrics.Duration.Milliseconds(),
		}).Warn("task cancelled")
	})

	srv := server.NewMCPServer(
		"codex-mcp",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithTaskCapabilities(true, true, true),
		server.WithLogging(),
		server.WithRecovery(),
		server.WithHooks(hooks),
		server.WithTaskHooks(taskHooks),
	)

	tool := mcp.NewTool(
		"codex_exec",
		mcp.WithDescription("Run Codex non-interactively and return the final assistant message plus execution metadata."),
		mcp.WithTaskSupport(mcp.TaskSupportOptional),
		mcp.WithInputSchema[CodexExecInput](),
		mcp.WithOutputSchema[codexcli.RunResult](),
	)

	srv.AddTool(tool, mcp.NewStructuredToolHandler(func(ctx context.Context, req mcp.CallToolRequest, args CodexExecInput) (codexcli.RunResult, error) {
		return b.handleCodexExec(ctx, req, args)
	}))

	return srv
}

func (b Builder) handleCodexExec(ctx context.Context, req mcp.CallToolRequest, args CodexExecInput) (codexcli.RunResult, error) {
	if strings.TrimSpace(args.Prompt) == "" {
		return codexcli.RunResult{}, fmt.Errorf("prompt is required")
	}
	if args.TimeoutMS < 0 {
		return codexcli.RunResult{}, fmt.Errorf("timeout_ms must be non-negative: %d", args.TimeoutMS)
	}
	if args.Sandbox != "" && args.Sandbox != "read-only" && args.Sandbox != "workspace-write" && args.Sandbox != "danger-full-access" {
		return codexcli.RunResult{}, fmt.Errorf("invalid sandbox value: %s", args.Sandbox)
	}

	result, err := b.Runner.Run(ctx, codexcli.RunRequest{
		Prompt:           args.Prompt,
		Cwd:              args.Cwd,
		ThreadID:         args.ThreadID,
		Model:            args.Model,
		Profile:          args.Profile,
		Sandbox:          args.Sandbox,
		TimeoutMS:        args.TimeoutMS,
		SkipGitRepoCheck: args.SkipGitRepoCheck,
		Async:            req.Params.Task != nil,
	})
	if err != nil {
		var runErr *codexcli.RunError
		if ok := AsRunError(err, &runErr); ok && runErr.StderrTail != "" {
			return codexcli.RunResult{}, fmt.Errorf("%w", err)
		}
		return codexcli.RunResult{}, err
	}
	return result, nil
}

func AsRunError(err error, target **codexcli.RunError) bool {
	return errors.As(err, target)
}
