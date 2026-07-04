// Package providerkit holds the hook and CLI plumbing shared by Rig's
// provider adapters. Each provider declares one hook event catalog — which
// events it observes, how they are matched, and which runtime phase each
// drives — and providerkit derives the provider's hook registration rules,
// its required-events health check, and its hook-to-status mapping from that
// single declaration. What stays in each provider package is only what
// genuinely differs: file locations, registration mechanism, and the catalog
// itself.
package providerkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

// Binding declares one hook event a provider observes: the event's canonical
// name (a core.HookEvent* constant), the provider-side matcher restricting
// when the hook fires (empty = always), and the runtime phase the event
// drives.
type Binding struct {
	Event   string
	Matcher string
	Phase   core.TaskStatusPhase
}

// Catalog is a provider's single declaration of the hook events it observes.
type Catalog []Binding

// HookConfig is the hooks JSON document shape shared by Claude workspace
// settings and Codex global hook config.
type HookConfig struct {
	Hooks map[string][]HookRule `json:"hooks"`
}

// HookRule is one matcher-scoped rule inside a HookConfig.
type HookRule struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []HookCommand `json:"hooks"`
}

// HookCommand is one command invocation registered for a rule.
type HookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// HookRules derives the provider's hook registration rules from the catalog.
// command renders the shell command that forwards the named event to Rig.
func (c Catalog) HookRules(command func(eventName string) string) map[string][]HookRule {
	rules := make(map[string][]HookRule, len(c))
	for _, binding := range c {
		rules[binding.Event] = append(rules[binding.Event], HookRule{
			Matcher: binding.Matcher,
			Hooks:   []HookCommand{{Type: "command", Command: command(binding.Event)}},
		})
	}
	return rules
}

// RenderHookConfig encodes the catalog's registration rules as the hooks JSON
// document both providers write.
func (c Catalog) RenderHookConfig(command func(eventName string) string) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(HookConfig{Hooks: c.HookRules(command)}); err != nil {
		return nil, fmt.Errorf("encode hook config: %w", err)
	}

	return buf.Bytes(), nil
}

// EventNames returns the observed event names in catalog order, for
// required-events health checks.
func (c Catalog) EventNames() []string {
	names := make([]string, 0, len(c))
	for _, binding := range c {
		names = append(names, binding.Event)
	}
	return names
}

// StatusUpdate is the shared body of ProviderClient.HookEventToTaskStatus:
// it normalizes a hook event into the task status update implied by the
// catalog, returning nil for events the catalog does not drive status from.
func (c Catalog) StatusUpdate(
	provider core.Provider,
	input core.HookEventInput,
) (*core.TaskStatusUpdate, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return nil, core.ErrUnmanagedHookEvent
	}

	eventName := strings.TrimSpace(input.EventName)
	if eventName == "" {
		return nil, core.ErrUnmanagedHookEvent
	}

	for _, binding := range c {
		if binding.Event != eventName {
			continue
		}
		return &core.TaskStatusUpdate{
			TaskID:       taskID,
			Provider:     provider,
			Phase:        binding.Phase,
			RawEventName: eventName,
			ObservedAt:   input.OccurredAt,
		}, nil
	}

	return nil, nil
}

// MergeRigHookRules replaces Rig's own hook rules — identified by commands
// referencing scriptPath — with the given rig rules, preserving every foreign
// rule. Stale Rig registrations for events no longer in the catalog are
// removed.
func MergeRigHookRules(
	existing map[string][]HookRule,
	rig map[string][]HookRule,
	scriptPath string,
) map[string][]HookRule {
	merged := make(map[string][]HookRule, len(existing)+len(rig))
	for eventName, rules := range existing {
		if kept := rulesWithoutScriptCommand(rules, scriptPath); len(kept) > 0 {
			merged[eventName] = kept
		}
	}
	for eventName, rules := range rig {
		for _, rule := range rules {
			if !containsHookRule(merged[eventName], rule) {
				merged[eventName] = append(merged[eventName], rule)
			}
		}
	}
	return merged
}

func rulesWithoutScriptCommand(rules []HookRule, scriptPath string) []HookRule {
	var filtered []HookRule
	for _, rule := range rules {
		if HookRuleHasScriptCommand(rule, scriptPath) {
			continue
		}
		filtered = append(filtered, rule)
	}
	return filtered
}

// HookRuleHasScriptCommand reports whether the rule invokes the given
// forwarder script — the marker for rules Rig owns.
func HookRuleHasScriptCommand(rule HookRule, scriptPath string) bool {
	for _, hook := range rule.Hooks {
		if hook.Type == "command" && strings.Contains(hook.Command, scriptPath) {
			return true
		}
	}
	return false
}

func containsHookRule(existing []HookRule, candidate HookRule) bool {
	for _, rule := range existing {
		if rule.Matcher != candidate.Matcher || len(rule.Hooks) != len(candidate.Hooks) {
			continue
		}

		matches := true
		for idx := range rule.Hooks {
			if rule.Hooks[idx] != candidate.Hooks[idx] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}

	return false
}
