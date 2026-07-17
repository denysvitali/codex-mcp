package codexcli

import (
	"context"
	"strings"
	"testing"
)

const fakeCatalog = `{"models":[
  {"slug":"gpt-b","display_name":"GPT B","description":"Second choice.","default_reasoning_level":"medium",
   "supported_reasoning_levels":[{"effort":"medium","description":"d"},{"effort":"high","description":"d"}],
   "visibility":"list","priority":2,"base_instructions":"ignored"},
  {"slug":"gpt-hidden","display_name":"Hidden","visibility":"hide","priority":0},
  {"slug":"gpt-a","display_name":"GPT A","description":"First choice.","default_reasoning_level":"low",
   "supported_reasoning_levels":[{"effort":"low","description":"d"}],
   "visibility":"list","priority":1}
]}`

func TestListModelsParsesCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
if [[ "$1" != "debug" || "$2" != "models" ]]; then
  echo "unexpected command: $*" >&2
  exit 2
fi
cat <<'EOF'
`+fakeCatalog+`
EOF
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())

	models, err := runner.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 listed models, got %+v", models)
	}

	first := models[0]
	if first.Slug != "gpt-a" || first.DisplayName != "GPT A" || first.Description != "First choice." {
		t.Fatalf("unexpected first model: %+v", first)
	}
	if first.DefaultReasoningLevel != "low" {
		t.Fatalf("unexpected default reasoning level: %+v", first)
	}
	if len(first.SupportedReasoningLevels) != 1 || first.SupportedReasoningLevels[0] != "low" {
		t.Fatalf("unexpected reasoning levels: %+v", first.SupportedReasoningLevels)
	}

	second := models[1]
	if second.Slug != "gpt-b" {
		t.Fatalf("unexpected second model: %+v", second)
	}
	if len(second.SupportedReasoningLevels) != 2 || second.SupportedReasoningLevels[0] != "medium" || second.SupportedReasoningLevels[1] != "high" {
		t.Fatalf("unexpected reasoning levels: %+v", second.SupportedReasoningLevels)
	}

	for _, m := range models {
		if m.Slug == "gpt-hidden" {
			t.Fatalf("hidden model should have been filtered out: %+v", models)
		}
	}
}

func TestListModelsEmptyCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' '{"models":[{"slug":"gpt-hidden","visibility":"hide"}]}'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "catalog is empty") {
		t.Fatalf("expected empty catalog error, got %v", err)
	}
}

func TestListModelsInvalidJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
printf '%s\n' 'not json'
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "parse codex model catalog") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestListModelsCommandFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexPath := writeExecutable(t, root, fakeCodexScript(`#!/usr/bin/env bash
echo "unknown command" >&2
exit 3
`))

	runner := NewRunner(RunnerConfig{
		CodexBin:          codexPath,
		Root:              root,
		MaxConcurrentRuns: 1,
	}, testLogger())

	_, err := runner.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list codex models") {
		t.Fatalf("expected command error, got %v", err)
	}
}
