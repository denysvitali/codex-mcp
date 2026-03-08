package codexcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestRunnerRunSuccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
if [[ "$1" != "exec" ]]; then
  echo "unexpected command" >&2
  exit 2
fi
printf '%s\n' '{"type":"thread.started","thread_id":"thread-123"}'
printf '%s\n' '{"type":"turn.started"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"pong"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	result, err := runner.Run(context.Background(), RunRequest{Prompt: "say pong"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.ThreadID != "thread-123" {
		t.Fatalf("unexpected thread id: %q", result.ThreadID)
	}
	if result.FinalMessage != "pong" {
		t.Fatalf("unexpected final message: %q", result.FinalMessage)
	}
	if result.Usage.InputTokens != 10 || result.Usage.CachedInputTokens != 4 || result.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
}

func TestRunnerRunResumeAndAsync(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' "$@" > "`+filepath.Join(root, `args.txt`)+`"
printf '%s\n' '{"type":"thread.started","thread_id":"thread-456"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"done"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	result, err := runner.Run(context.Background(), RunRequest{
		Prompt:   "continue",
		ThreadID: "session-1",
		Async:    true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Mode != "task" {
		t.Fatalf("unexpected mode: %q", result.Mode)
	}

	argsData, err := os.ReadFile(filepath.Join(root, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(argsData)
	for _, want := range []string{
		"resume",
		"session-1",
		"--model",
		"gpt-5.4",
		"--dangerously-bypass-approvals-and-sandbox",
		"continue",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected args to contain %q, got %q", want, args)
		}
	}
}

func TestRunnerConfigDisablesYoloAndAddsSandbox(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' "$@" > "`+filepath.Join(root, `args.txt`)+`"
printf '%s\n' '{"type":"thread.started","thread_id":"thread-789"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       false,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{
		Prompt:  "continue",
		Sandbox: "read-only",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	argsData, err := os.ReadFile(filepath.Join(root, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(argsData)
	if strings.Contains(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("expected yolo flag to be omitted: %q", args)
	}
	if !strings.Contains(args, "--sandbox\nread-only") {
		t.Fatalf("expected sandbox flag in args: %q", args)
	}
}

func TestRunnerOutsideGitRepoAutoSkipsCheck(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' "$@" > "`+filepath.Join(root, `args.txt`)+`"
printf '%s\n' '{"type":"thread.started","thread_id":"thread-999"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{Prompt: "continue"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	argsData, err := os.ReadFile(filepath.Join(root, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if !strings.Contains(string(argsData), "--skip-git-repo-check") {
		t.Fatalf("expected auto skip git repo check, got %q", string(argsData))
	}
}

func TestResolveSkipGitRepoCheckErrorsWhenGitMissing(t *testing.T) {
	root := t.TempDir()
	runner := NewRunner(RunnerConfig{
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", "")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", originalPath)
	})

	skip, err := runner.resolveSkipGitRepoCheck(context.Background(), root, nil)
	if err == nil {
		t.Fatal("expected error when git is missing")
	}
	if skip {
		t.Fatal("expected skip to remain disabled when git is missing")
	}
	if !errors.Is(err, errGitNotFound) {
		t.Fatalf("expected errGitNotFound, got %v", err)
	}
}

func TestResolveSkipGitRepoCheckWarnsAndSkipsOnUnexpectedGitError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)

	runner := NewRunner(RunnerConfig{
		Root:              root,
		MaxConcurrentRuns: 1,
	}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	skip, err := runner.resolveSkipGitRepoCheck(ctx, root, nil)
	if err != nil {
		t.Fatalf("expected warning path, got error: %v", err)
	}
	if !skip {
		t.Fatal("expected skip to be enabled on unexpected git error")
	}
	if !strings.Contains(logs.String(), "git repo detection failed; enabling skip-git-repo-check") {
		t.Fatalf("expected warning log, got %q", logs.String())
	}
}

func TestRunnerFailureIncludesStderrTail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' '{"type":"thread.started","thread_id":"thread-fail"}'
echo "boom" >&2
exit 7
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{Prompt: "continue"})
	if err == nil {
		t.Fatal("expected error")
	}

	runErr, ok := err.(*RunError)
	if !ok {
		t.Fatalf("expected RunError, got %T", err)
	}
	if runErr.ExitCode != 7 {
		t.Fatalf("unexpected exit code: %d", runErr.ExitCode)
	}
	if runErr.StderrTail != "boom" {
		t.Fatalf("unexpected stderr tail: %q", runErr.StderrTail)
	}
}

func TestRunnerRejectsDisallowedCwd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	other := t.TempDir()
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
exit 0
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{
		Prompt: "continue",
		Cwd:    other,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunnerRejectsSymlinkEscapingAllowedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link-outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
exit 0
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{
		Prompt: "continue",
		Cwd:    link,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "outside the allowed roots") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunnerRunTimeout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' '{"type":"thread.started","thread_id":"thread-timeout"}'
sleep 1
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"late"}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	start := time.Now()
	_, err := runner.Run(context.Background(), RunRequest{
		Prompt:    "continue",
		TimeoutMS: 50,
	})
	if err == nil {
		t.Fatal("expected error")
	}

	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected TimeoutError, got %T", err)
	}
	if timeoutErr.DurationMS != 50 {
		t.Fatalf("unexpected timeout: %+v", timeoutErr)
	}
	if timeoutErr.ThreadID != "thread-timeout" {
		t.Fatalf("unexpected thread id: %q", timeoutErr.ThreadID)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected timeout before script completed, elapsed=%s", elapsed)
	}
}

func TestRunnerCompletesBeforeTimeout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
sleep 0.05
printf '%s\n' '{"type":"thread.started","thread_id":"thread-fast"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"done"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	result, err := runner.Run(context.Background(), RunRequest{
		Prompt:    "continue",
		TimeoutMS: 500,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinalMessage != "done" {
		t.Fatalf("unexpected final message: %q", result.FinalMessage)
	}
}

func TestRunnerReturnsErrorWhenFinalAgentMessageMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGitRepo(t, root)
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' '{"type":"thread.started","thread_id":"thread-no-final"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"tool_result","text":"ignore me"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		DefaultYolo:       true,
		DefaultModel:      "gpt-5.4",
		DefaultSandbox:    "workspace-write",
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{Prompt: "continue"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "codex returned no final agent message") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseJSONLMalformed(t *testing.T) {
	t.Parallel()

	_, err := parseJSONL(strings.NewReader("{\"type\":\"thread.started\"}\nnot-json\n"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "decode event 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseJSONLIgnoresNonAgentItemCompleted(t *testing.T) {
	t.Parallel()

	state, err := parseJSONL(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-42"}`,
		`{"type":"item.completed","item":{"type":"tool_result","text":"ignore me"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"final"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":2,"cached_input_tokens":1,"output_tokens":3}}`,
	}, "\n")))
	if err != nil {
		t.Fatalf("parseJSONL() error = %v", err)
	}
	if state.threadID != "thread-42" || state.finalMessage != "final" {
		t.Fatalf("unexpected parser state: %+v", state)
	}
	if state.usage.InputTokens != 2 || state.usage.CachedInputTokens != 1 || state.usage.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", state.usage)
	}
}

func TestParseJSONLMissingFinalAgentMessage(t *testing.T) {
	t.Parallel()

	state, err := parseJSONL(strings.NewReader(strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-42"}`,
		`{"type":"item.completed","item":{"type":"tool_result","text":"ignore me"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":2,"cached_input_tokens":1,"output_tokens":3}}`,
	}, "\n")))
	if err != nil {
		t.Fatalf("parseJSONL() error = %v", err)
	}
	if state.finalMessage != "" {
		t.Fatalf("expected missing final message, got %+v", state)
	}
	if state.eventCount != 3 {
		t.Fatalf("unexpected event count: %+v", state)
	}
}

func TestParseJSONLEmptyInput(t *testing.T) {
	t.Parallel()

	state, err := parseJSONL(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseJSONL() error = %v", err)
	}
	if state != (parserState{}) {
		t.Fatalf("expected zero parser state, got %+v", state)
	}
}

func TestRunnerRejectsBlankPrompt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := NewRunner(RunnerConfig{
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.Run(context.Background(), RunRequest{Prompt: " \t\n"})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "prompt is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunnerReturnsContextCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runner := NewRunner(RunnerConfig{
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())
	runner.semaphore <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner.Run(ctx, RunRequest{Prompt: "continue"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func testLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(ioDiscard{})
	return logger
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func writeExecutable(t *testing.T, dir string, script string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-codex.sh")
	writeFile(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return path
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func fakeCodexScript(body string) string {
	return body
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	cmd := exec.Command("git", "init", dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, output)
	}
}
