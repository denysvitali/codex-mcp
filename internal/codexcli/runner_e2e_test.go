//go:build e2e

package codexcli

// End-to-end tests against the real Codex CLI. They require `codex` in
// PATH and valid credentials, and they consume model quota, so they are
// excluded from the default test run. Run them explicitly with:
//
//	go test -tags e2e ./internal/codexcli/ -run E2E -v

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const e2eModel = "gpt-5.3-codex-spark"

func e2eRunner(t *testing.T, root string) *Runner {
	t.Helper()
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex binary not found in PATH")
	}
	return NewRunner(RunnerConfig{
		CodexBin:          codexBin,
		Root:              root,
		DefaultYolo:       false,
		DefaultSandbox:    "read-only",
		MaxConcurrentRuns: 1,
	}, testLogger())
}

func TestRunnerE2EBasicSpark(t *testing.T) {
	runner := e2eRunner(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := runner.Run(ctx, RunRequest{
		Prompt:          "Reply with exactly: pong",
		Model:           e2eModel,
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result.FinalMessage), "pong") {
		t.Fatalf("unexpected final message: %q", result.FinalMessage)
	}
	if result.ThreadID == "" {
		t.Fatal("expected thread_id from real codex run")
	}
	if result.Usage.OutputTokens == 0 {
		t.Fatalf("expected non-zero output tokens: %+v", result.Usage)
	}
	if result.RawEventCount == 0 {
		t.Fatal("expected JSONL events to be parsed")
	}
	t.Logf("thread_id=%s final=%q usage=%+v events=%d elapsed_ms=%d",
		result.ThreadID, result.FinalMessage, result.Usage, result.RawEventCount, result.ElapsedMS)
}

func TestRunnerE2EStructuredOutput(t *testing.T) {
	runner := e2eRunner(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := runner.Run(ctx, RunRequest{
		Prompt:          `Reply with a JSON object whose "answer" field is the string "pong".`,
		Model:           e2eModel,
		ReasoningEffort: "low",
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"answer": map[string]any{"type": "string"},
			},
			"required": []string{"answer"},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var structured struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.FinalMessage)), &structured); err != nil {
		t.Fatalf("final message is not JSON matching output_schema: %q: %v", result.FinalMessage, err)
	}
	if structured.Answer != "pong" {
		t.Fatalf("unexpected structured answer: %q", structured.Answer)
	}
	t.Logf("structured final message: %q", result.FinalMessage)
}

func TestRunnerE2EEphemeralAndAddDirs(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("mkdir", "-p", root+"/extra").Run(); err != nil {
		t.Fatalf("mkdir extra: %v", err)
	}
	runner := e2eRunner(t, root)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := runner.Run(ctx, RunRequest{
		Prompt:          "Reply with exactly: pong",
		Model:           e2eModel,
		ReasoningEffort: "low",
		AddDirs:         []string{"extra"},
		Ephemeral:       true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result.FinalMessage), "pong") {
		t.Fatalf("unexpected final message: %q", result.FinalMessage)
	}
	t.Logf("ephemeral run thread_id=%s", result.ThreadID)
}
