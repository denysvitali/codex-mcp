package mcpserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/denysvitali/codex-mcp/internal/codexcli"
)

func TestHandleCodexExecValidation(t *testing.T) {
	t.Parallel()

	builder := Builder{
		Runner: stubRunner{},
		Logger: logrus.New(),
	}

	tests := []struct {
		name string
		args CodexExecInput
		want string
	}{
		{
			name: "blank prompt",
			args: CodexExecInput{},
			want: "prompt is required",
		},
		{
			name: "whitespace prompt",
			args: CodexExecInput{
				Prompt: " \n\t ",
			},
			want: "prompt is required",
		},
		{
			name: "negative timeout",
			args: CodexExecInput{
				Prompt:    "run",
				TimeoutMS: -1,
			},
			want: "timeout_ms must be non-negative: -1",
		},
		{
			name: "invalid sandbox",
			args: CodexExecInput{
				Prompt:  "run",
				Sandbox: "sandboxed",
			},
			want: "invalid sandbox value: sandboxed",
		},
		{
			name: "invalid reasoning effort",
			args: CodexExecInput{
				Prompt:          "run",
				ReasoningEffort: `high";model="x`,
			},
			want: `invalid reasoning_effort value: high";model="x`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := builder.handleCodexExec(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestHandleCodexExecForwardsRequest(t *testing.T) {
	t.Parallel()

	runner := &capturingRunner{
		result: codexcli.RunResult{FinalMessage: "ok"},
	}
	builder := Builder{
		Runner: runner,
		Logger: logrus.New(),
	}

	skip := true
	_, result, err := builder.handleCodexExec(context.Background(), nil, CodexExecInput{
		Prompt:           "ship it",
		Cwd:              "repo",
		ThreadID:         "thread-1",
		Model:            "gpt-test",
		ReasoningEffort:  "high",
		Profile:          "fast",
		OutputSchema:     map[string]any{"type": "object"},
		AddDirs:          []string{"extra"},
		Ephemeral:        true,
		Sandbox:          "read-only",
		TimeoutMS:        123,
		SkipGitRepoCheck: &skip,
	})
	if err != nil {
		t.Fatalf("handleCodexExec() error = %v", err)
	}
	if result.FinalMessage != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}

	req := runner.req
	if req.Prompt != "ship it" || req.Cwd != "repo" || req.ThreadID != "thread-1" || req.Model != "gpt-test" || req.ReasoningEffort != "high" || req.Profile != "fast" || req.Sandbox != "read-only" || req.TimeoutMS != 123 {
		t.Fatalf("unexpected request forwarded to runner: %+v", req)
	}
	if !reflect.DeepEqual(req.AddDirs, []string{"extra"}) {
		t.Fatalf("expected add_dirs to be forwarded: %+v", req.AddDirs)
	}
	if !req.Ephemeral {
		t.Fatalf("expected ephemeral to be forwarded: %+v", req.Ephemeral)
	}
	if req.SkipGitRepoCheck == nil || !*req.SkipGitRepoCheck {
		t.Fatalf("expected skip_git_repo_check to be forwarded: %+v", req)
	}
	if !reflect.DeepEqual(req.OutputSchema, map[string]any{"type": "object"}) {
		t.Fatalf("unexpected output schema: %+v", req.OutputSchema)
	}
}

func TestHandleCodexExecReturnsStructuredOutput(t *testing.T) {
	t.Parallel()

	builder := Builder{
		Runner: &capturingRunner{},
		Logger: logrus.New(),
	}
	runner := builder.Runner.(*capturingRunner)
	runner.result = codexcli.RunResult{FinalMessage: `{"status":"done"}`}

	_, result, err := builder.handleCodexExec(context.Background(), nil, CodexExecInput{
		Prompt:       "run",
		OutputSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("handleCodexExec() error = %v", err)
	}
	if result.StructuredOutput == nil {
		t.Fatal("expected structured_output when final message is valid JSON")
	}
	if !reflect.DeepEqual(*result.StructuredOutput, map[string]any{"status": "done"}) {
		t.Fatalf("expected structured_output from JSON final message, got %+v", result.StructuredOutput)
	}

	runner.result = codexcli.RunResult{FinalMessage: "not json"}

	_, result, err = builder.handleCodexExec(context.Background(), nil, CodexExecInput{
		Prompt:       "run",
		OutputSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("handleCodexExec() error = %v", err)
	}
	if result.StructuredOutput != nil {
		t.Fatalf("expected no structured_output when final message is not valid JSON, got %+v", result.StructuredOutput)
	}
}

func TestHandleCodexExecAcceptsValidSandboxValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sandbox string
	}{
		{name: "empty", sandbox: ""},
		{name: "read only", sandbox: "read-only"},
		{name: "workspace write", sandbox: "workspace-write"},
		{name: "danger full access", sandbox: "danger-full-access"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := &capturingRunner{
				result: codexcli.RunResult{FinalMessage: "ok"},
			}
			builder := Builder{
				Runner: runner,
				Logger: logrus.New(),
			}

			_, _, err := builder.handleCodexExec(context.Background(), nil, CodexExecInput{
				Prompt:  "run",
				Sandbox: tt.sandbox,
			})
			if err != nil {
				t.Fatalf("handleCodexExec() error = %v", err)
			}
			if runner.req.Sandbox != tt.sandbox {
				t.Fatalf("expected sandbox %q, got %q", tt.sandbox, runner.req.Sandbox)
			}
		})
	}
}

func TestHandleCodexExecReturnsRunError(t *testing.T) {
	t.Parallel()

	inner := &codexcli.RunError{
		Err:        errors.New("boom"),
		ExitCode:   17,
		StderrTail: "stderr tail",
		ThreadID:   "thread-err",
	}
	builder := Builder{
		Runner: stubRunner{err: inner},
		Logger: logrus.New(),
	}

	_, _, err := builder.handleCodexExec(context.Background(), nil, CodexExecInput{Prompt: "run"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.As(err, &inner) {
		t.Fatalf("expected wrapped RunError, got %T", err)
	}
	if !strings.Contains(err.Error(), "stderr tail") {
		t.Fatalf("expected error message to include stderr tail, got %q", err.Error())
	}
}

func TestHandleListModels(t *testing.T) {
	t.Parallel()

	models := []codexcli.Model{
		{Slug: "gpt-a", DisplayName: "GPT A"},
		{Slug: "gpt-b", DisplayName: "GPT B", DefaultReasoningLevel: "medium"},
	}
	builder := Builder{
		Runner:                 stubRunner{models: models},
		Logger:                 logrus.New(),
		DefaultModel:           "gpt-a",
		DefaultReasoningEffort: "medium",
	}

	_, result, err := builder.handleListModels(context.Background(), nil, ListModelsInput{})
	if err != nil {
		t.Fatalf("handleListModels() error = %v", err)
	}
	if len(result.Models) != 2 || result.Models[0].Slug != "gpt-a" || result.Models[1].Slug != "gpt-b" {
		t.Fatalf("unexpected models: %+v", result.Models)
	}
	if result.DefaultModel != "gpt-a" {
		t.Fatalf("unexpected default model: %q", result.DefaultModel)
	}
	if result.DefaultReasoningEffort != "medium" {
		t.Fatalf("unexpected default reasoning effort: %q", result.DefaultReasoningEffort)
	}
}

func TestHandleListModelsPropagatesError(t *testing.T) {
	t.Parallel()

	builder := Builder{
		Runner: stubRunner{err: errors.New("catalog unavailable")},
		Logger: logrus.New(),
	}

	_, _, err := builder.handleListModels(context.Background(), nil, ListModelsInput{})
	if err == nil || !strings.Contains(err.Error(), "catalog unavailable") {
		t.Fatalf("expected catalog error, got %v", err)
	}
}

// TestServerEndToEnd exercises the full MCP protocol stack over the official
// SDK's in-memory transport: initialize, list tools, and call both tools.
func TestServerEndToEnd(t *testing.T) {
	t.Parallel()

	runner := &capturingRunner{
		result: codexcli.RunResult{FinalMessage: "pong", ThreadID: "thread-1"},
		models: []codexcli.Model{{Slug: "gpt-a", DisplayName: "GPT A"}},
	}
	srv := Builder{
		Runner:       runner,
		Logger:       logrus.New(),
		Version:      "test",
		DefaultModel: "gpt-a",
	}.New()

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect() error = %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect() error = %v", err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var codexExecTool *mcp.Tool
	var foundCodexExec bool
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.Name == "codex_exec" {
			codexExecTool = tool
			foundCodexExec = true
		}
	}
	if !names["codex_exec"] || !names["codex_list_models"] {
		t.Fatalf("expected both tools to be registered, got %v", names)
	}
	if !foundCodexExec {
		t.Fatalf("codex_exec tool not found in ListTools response")
	}
	if codexExecTool == nil {
		t.Fatalf("codex_exec tool was not captured despite registration")
	}
	inputSchema, ok := codexExecTool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("expected codex_exec input schema map, got %T", codexExecTool.InputSchema)
	}
	properties, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected codex_exec schema properties map, got %T", inputSchema["properties"])
	}
	if _, ok := properties["output_schema"]; !ok {
		t.Fatalf("expected codex_exec input schema to include output_schema, got %v", properties)
	}
	if _, ok := properties["add_dirs"]; !ok {
		t.Fatalf("expected codex_exec input schema to include add_dirs, got %v", properties)
	}
	if _, ok := properties["ephemeral"]; !ok {
		t.Fatalf("expected codex_exec input schema to include ephemeral, got %v", properties)
	}

	execResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "codex_exec",
		Arguments: map[string]any{"prompt": "say pong", "model": "gpt-a"},
	})
	if err != nil {
		t.Fatalf("CallTool(codex_exec) error = %v", err)
	}
	if execResult.IsError {
		t.Fatalf("codex_exec returned tool error: %+v", execResult.Content)
	}
	structured, ok := execResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content, got %T", execResult.StructuredContent)
	}
	if structured["final_message"] != "pong" || structured["thread_id"] != "thread-1" {
		t.Fatalf("unexpected structured content: %+v", structured)
	}
	if runner.req.Model != "gpt-a" {
		t.Fatalf("expected model to be forwarded, got %+v", runner.req)
	}

	modelsResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "codex_list_models"})
	if err != nil {
		t.Fatalf("CallTool(codex_list_models) error = %v", err)
	}
	if modelsResult.IsError {
		t.Fatalf("codex_list_models returned tool error: %+v", modelsResult.Content)
	}
	structured, ok = modelsResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content, got %T", modelsResult.StructuredContent)
	}
	if structured["default_model"] != "gpt-a" {
		t.Fatalf("unexpected default model: %+v", structured)
	}
	models, ok := structured["models"].([]any)
	if !ok || len(models) != 1 {
		t.Fatalf("unexpected models payload: %+v", structured)
	}

	toolError, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "codex_exec",
		Arguments: map[string]any{"prompt": ""},
	})
	if err != nil {
		t.Fatalf("CallTool(invalid) error = %v", err)
	}
	if !toolError.IsError {
		t.Fatalf("expected validation error to surface as tool error: %+v", toolError)
	}
}

type stubRunner struct {
	result codexcli.RunResult
	models []codexcli.Model
	err    error
}

func (r stubRunner) Run(context.Context, codexcli.RunRequest) (codexcli.RunResult, error) {
	return r.result, r.err
}

func (r stubRunner) ListModels(context.Context) ([]codexcli.Model, error) {
	return r.models, r.err
}

type capturingRunner struct {
	req    codexcli.RunRequest
	result codexcli.RunResult
	models []codexcli.Model
}

func (r *capturingRunner) Run(_ context.Context, req codexcli.RunRequest) (codexcli.RunResult, error) {
	r.req = req
	return r.result, nil
}

func (r *capturingRunner) ListModels(context.Context) ([]codexcli.Model, error) {
	return r.models, nil
}
