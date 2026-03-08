package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"

	"github.com/denysvitali/codex-mcp/internal/codexcli"
)

func TestCodexExecInputSchemaCompiles(t *testing.T) {
	t.Parallel()

	var input CodexExecInput
	if input.Prompt != "" {
		t.Fatal("zero value mismatch")
	}
}

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
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := builder.handleCodexExec(context.Background(), mcp.CallToolRequest{}, tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestHandleCodexExecPassesAsyncFlag(t *testing.T) {
	t.Parallel()

	runner := &capturingRunner{
		result: codexcli.RunResult{FinalMessage: "ok"},
	}
	builder := Builder{
		Runner: runner,
		Logger: logrus.New(),
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Task: &mcp.TaskParams{},
		},
	}
	skip := true
	result, err := builder.handleCodexExec(context.Background(), req, CodexExecInput{
		Prompt:           "ship it",
		Cwd:              "repo",
		ThreadID:         "thread-1",
		Model:            "gpt-test",
		Profile:          "fast",
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

	if runner.req.Prompt != "ship it" || runner.req.Cwd != "repo" || runner.req.ThreadID != "thread-1" {
		t.Fatalf("unexpected request forwarded to runner: %+v", runner.req)
	}
	if !runner.req.Async {
		t.Fatalf("expected async request: %+v", runner.req)
	}
	if runner.req.SkipGitRepoCheck == nil || !*runner.req.SkipGitRepoCheck {
		t.Fatalf("expected skip_git_repo_check to be forwarded: %+v", runner.req)
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

			_, err := builder.handleCodexExec(context.Background(), mcp.CallToolRequest{}, CodexExecInput{
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

func TestHandleCodexExecWrapsRunErrorWithStderrTail(t *testing.T) {
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

	_, err := builder.handleCodexExec(context.Background(), mcp.CallToolRequest{}, CodexExecInput{Prompt: "run"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.As(err, &inner) {
		t.Fatalf("expected wrapped RunError, got %T", err)
	}
	if !strings.Contains(err.Error(), "stderr tail") {
		t.Fatalf("expected wrapped error message to include stderr tail, got %q", err.Error())
	}
}

func TestHandleCodexExecWrapsNestedRunErrorWithStderrTail(t *testing.T) {
	t.Parallel()

	inner := &codexcli.RunError{
		Err:        errors.New("boom"),
		ExitCode:   17,
		StderrTail: "stderr tail",
		ThreadID:   "thread-err",
	}
	builder := Builder{
		Runner: stubRunner{err: fmt.Errorf("runner failed: %w", inner)},
		Logger: logrus.New(),
	}

	_, err := builder.handleCodexExec(context.Background(), mcp.CallToolRequest{}, CodexExecInput{Prompt: "run"})
	if err == nil {
		t.Fatal("expected error")
	}

	var runErr *codexcli.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("expected wrapped RunError, got %T", err)
	}
	if runErr != inner {
		t.Fatalf("expected original RunError pointer, got %#v", runErr)
	}
	if !strings.Contains(err.Error(), "stderr tail") {
		t.Fatalf("expected wrapped error message to include stderr tail, got %q", err.Error())
	}
}

func TestHandleCodexExecReturnsPlainErrorWithoutStderrTail(t *testing.T) {
	t.Parallel()

	inner := &codexcli.RunError{
		Err:      errors.New("boom"),
		ExitCode: 17,
	}
	builder := Builder{
		Runner: stubRunner{err: inner},
		Logger: logrus.New(),
	}

	_, err := builder.handleCodexExec(context.Background(), mcp.CallToolRequest{}, CodexExecInput{Prompt: "run"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.As(err, &inner) {
		t.Fatalf("expected RunError, got %T", err)
	}
}

func TestAsRunErrorFindsWrappedRunError(t *testing.T) {
	t.Parallel()

	inner := &codexcli.RunError{Err: errors.New("boom"), ExitCode: 42}
	var target *codexcli.RunError

	if !AsRunError(fmt.Errorf("outer: %w", inner), &target) {
		t.Fatal("expected AsRunError to match wrapped RunError")
	}
	if target != inner {
		t.Fatalf("expected original RunError pointer, got %#v", target)
	}
}

type stubRunner struct {
	result codexcli.RunResult
	err    error
}

func (r stubRunner) Run(context.Context, codexcli.RunRequest) (codexcli.RunResult, error) {
	return r.result, r.err
}

type capturingRunner struct {
	req    codexcli.RunRequest
	result codexcli.RunResult
}

func (r *capturingRunner) Run(_ context.Context, req codexcli.RunRequest) (codexcli.RunResult, error) {
	r.req = req
	return r.result, nil
}
