package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"time"
)

// Model describes a Codex model that callers may pass as the model argument
// to codex_exec. It is a distilled view of the local Codex CLI model catalog.
type Model struct {
	Slug                     string   `json:"slug"`
	DisplayName              string   `json:"display_name"`
	Description              string   `json:"description,omitempty"`
	DefaultReasoningLevel    string   `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels []string `json:"supported_reasoning_levels,omitempty"`
}

// modelCatalog mirrors the JSON emitted by `codex debug models`.
type modelCatalog struct {
	Models []catalogModel `json:"models"`
}

type catalogModel struct {
	Slug                     string `json:"slug"`
	DisplayName              string `json:"display_name"`
	Description              string `json:"description"`
	DefaultReasoningLevel    string `json:"default_reasoning_level"`
	SupportedReasoningLevels []struct {
		Effort string `json:"effort"`
	} `json:"supported_reasoning_levels"`
	Visibility string `json:"visibility"`
	Priority   int    `json:"priority"`
}

const listModelsTimeout = 15 * time.Second

// ListModels returns the models advertised by the local Codex CLI model
// catalog (`codex debug models`), restricted to models with "list" visibility
// and ordered by Codex's own priority. When the server is configured with an
// allow-list of model slugs, only those models are returned. The catalog is
// baked into the Codex binary, so the unfiltered result is cached for the
// lifetime of the process.
func (r *Runner) ListModels(ctx context.Context) ([]Model, error) {
	r.modelsMu.Lock()
	defer r.modelsMu.Unlock()

	if r.modelsCache == nil {
		models, err := r.fetchModels(ctx)
		if err != nil {
			return nil, err
		}
		r.modelsCache = models
	}

	if len(r.cfg.AllowModels) == 0 {
		return r.modelsCache, nil
	}
	filtered := make([]Model, 0, len(r.cfg.AllowModels))
	for _, model := range r.modelsCache {
		if slices.Contains(r.cfg.AllowModels, model.Slug) {
			filtered = append(filtered, model)
		}
	}
	return filtered, nil
}

func (r *Runner) fetchModels(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.cfg.CodexBin, "debug", "models")
	cmd.Dir = r.cfg.Root
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list codex models (requires a Codex CLI that supports `codex debug models`): %w", err)
	}

	var catalog modelCatalog
	if err := json.Unmarshal(output, &catalog); err != nil {
		return nil, fmt.Errorf("parse codex model catalog: %w", err)
	}

	sort.SliceStable(catalog.Models, func(i, j int) bool {
		return catalog.Models[i].Priority < catalog.Models[j].Priority
	})

	models := make([]Model, 0, len(catalog.Models))
	seen := make(map[string]struct{}, len(catalog.Models))
	for _, m := range catalog.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		if _, ok := seen[m.Slug]; ok {
			continue
		}
		seen[m.Slug] = struct{}{}

		model := Model{
			Slug:                  m.Slug,
			DisplayName:           m.DisplayName,
			Description:           m.Description,
			DefaultReasoningLevel: m.DefaultReasoningLevel,
		}
		for _, level := range m.SupportedReasoningLevels {
			model.SupportedReasoningLevels = append(model.SupportedReasoningLevels, level.Effort)
		}
		models = append(models, model)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("codex model catalog is empty")
	}
	return models, nil
}
