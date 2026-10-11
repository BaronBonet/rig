package codex

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

// modelsCacheFile is where Codex keeps, in its home, the models the home's
// account may use, as fetched from OpenAI when Codex starts. Accounts differ:
// one may have models another lacks.
const modelsCacheFile = "models_cache.json"

// listedModelVisibility marks the models Codex's own picker offers; the others
// are internal or retired.
const listedModelVisibility = "list"

// fallbackLaunchOptions are offered for a Codex home without a models cache,
// one Codex has not started with yet: the models and reasoning efforts a
// ChatGPT account offered in October 2026, in Codex's picker order.
var fallbackLaunchOptions = core.ProviderLaunchOptions{
	Models: []string{
		"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	},
	Efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
}

type codexModelsCache struct {
	Models []codexCachedModel `json:"models"`
}

type codexCachedModel struct {
	Slug                     string                `json:"slug"`
	Visibility               string                `json:"visibility"`
	Priority                 int                   `json:"priority"`
	SupportedReasoningLevels []codexReasoningLevel `json:"supported_reasoning_levels"`
}

type codexReasoningLevel struct {
	Effort string `json:"effort"`
}

// LaunchOptions lists the models the account of env's Codex home may use, and
// their reasoning efforts, falling back to fallbackLaunchOptions while that
// home has no readable models cache. Codex writes handoff notes.
func (r *repository) LaunchOptions(env core.ProviderEnv) core.ProviderLaunchOptions {
	options, ok := r.accountLaunchOptions(env)
	if !ok {
		options = core.ProviderLaunchOptions{
			Models:  append([]string(nil), fallbackLaunchOptions.Models...),
			Efforts: append([]string(nil), fallbackLaunchOptions.Efforts...),
		}
	}
	options.Handoff = true
	return options
}

// accountLaunchOptions reads the models cache of env's Codex home.
func (r *repository) accountLaunchOptions(env core.ProviderEnv) (core.ProviderLaunchOptions, bool) {
	codexHome, err := r.resolveCodexHomeDir(env)
	if err != nil {
		return core.ProviderLaunchOptions{}, false
	}
	return readModelsCache(filepath.Join(codexHome, modelsCacheFile))
}

// readModelsCache returns the listed models of a Codex models cache in
// Codex's priority order, and every reasoning effort one of them supports, in
// the order they first appear. It reports false when the cache is missing,
// unreadable or lists no model.
func readModelsCache(path string) (core.ProviderLaunchOptions, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return core.ProviderLaunchOptions{}, false
	}
	var cache codexModelsCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return core.ProviderLaunchOptions{}, false
	}

	models := slices.Clone(cache.Models)
	slices.SortStableFunc(models, func(a, b codexCachedModel) int { return cmp.Compare(a.Priority, b.Priority) })

	var options core.ProviderLaunchOptions
	for _, model := range models {
		slug := strings.TrimSpace(model.Slug)
		if model.Visibility != listedModelVisibility || slug == "" || slices.Contains(options.Models, slug) {
			continue
		}
		options.Models = append(options.Models, slug)
		for _, level := range model.SupportedReasoningLevels {
			effort := strings.TrimSpace(level.Effort)
			if effort != "" && !slices.Contains(options.Efforts, effort) {
				options.Efforts = append(options.Efforts, effort)
			}
		}
	}
	return options, len(options.Models) > 0
}
