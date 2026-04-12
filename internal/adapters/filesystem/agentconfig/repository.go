package agentconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rig/internal/core"

	"gopkg.in/yaml.v3"
)

type Loader struct{}

func NewLoader() *Loader {
	return &Loader{}
}

func (l *Loader) LoadRepoConfig(_ context.Context, repoRoot string) (core.RepoConfig, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "agent.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return core.RepoConfig{}, nil
		}
		return core.RepoConfig{}, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return core.RepoConfig{}, fmt.Errorf("parse agent.yaml: %w", err)
	}
	if err := validateDuplicateKeys(&doc); err != nil {
		return core.RepoConfig{}, err
	}

	seed, err := parseSeed(&doc)
	if err != nil {
		return core.RepoConfig{}, err
	}

	return core.RepoConfig{
		Exists: true,
		Seed:   seed,
	}, nil
}

func parseSeed(doc *yaml.Node) (core.SeedConfig, error) {
	root, err := documentRoot(doc)
	if err != nil {
		return core.SeedConfig{}, err
	}
	if root == nil {
		return core.SeedConfig{}, nil
	}
	if root.Kind != yaml.MappingNode {
		return core.SeedConfig{}, fmt.Errorf("invalid agent.yaml: root must be a mapping")
	}
	if err := validateAllowedKeys(root, "agent.yaml", "seed"); err != nil {
		return core.SeedConfig{}, err
	}

	seedNode, ok, err := lookupMapping(root, "seed")
	if err != nil {
		return core.SeedConfig{}, err
	}
	if !ok {
		return core.SeedConfig{}, nil
	}
	if seedNode.Kind != yaml.MappingNode {
		return core.SeedConfig{}, fmt.Errorf("invalid agent.yaml: seed must be a mapping")
	}
	if err := validateAllowedKeys(seedNode, "seed", "copy", "setup_script"); err != nil {
		return core.SeedConfig{}, err
	}

	copyPaths, err := parseSeedCopy(seedNode)
	if err != nil {
		return core.SeedConfig{}, err
	}

	setupScript, err := parseSeedSetupScript(seedNode)
	if err != nil {
		return core.SeedConfig{}, err
	}

	return core.SeedConfig{
		Copy:        copyPaths,
		SetupScript: setupScript,
	}, nil
}

func parseSeedCopy(seedNode *yaml.Node) ([]string, error) {
	copyNode, ok, err := lookupMapping(seedNode, "copy")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if copyNode.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("invalid agent.yaml: seed.copy must be a sequence")
	}

	paths := make([]string, 0, len(copyNode.Content))
	for i, item := range copyNode.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, fmt.Errorf("invalid agent.yaml: seed.copy[%d] must be a string", i)
		}

		path := item.Value
		if path == "" {
			return nil, fmt.Errorf("invalid agent.yaml: seed.copy[%d] must not be empty", i)
		}
		if err := validateSeedPath(path); err != nil {
			return nil, fmt.Errorf("invalid agent.yaml: seed.copy[%d] %w", i, err)
		}

		paths = append(paths, path)
	}

	return paths, nil
}

func parseSeedSetupScript(seedNode *yaml.Node) (string, error) {
	setupScriptNode, ok, err := lookupMapping(seedNode, "setup_script")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	if setupScriptNode.Kind != yaml.ScalarNode || setupScriptNode.Tag != "!!str" {
		return "", fmt.Errorf("invalid agent.yaml: seed.setup_script must be a string")
	}
	setupScript := setupScriptNode.Value
	if setupScript != "" {
		if err := validateSeedPath(setupScript); err != nil {
			return "", fmt.Errorf("invalid agent.yaml: seed.setup_script %w", err)
		}
	}
	return setupScript, nil
}

func documentRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc == nil || len(doc.Content) == 0 {
		return nil, nil
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("invalid agent.yaml: expected a document")
	}
	return doc.Content[0], nil
}

func lookupMapping(node *yaml.Node, key string) (*yaml.Node, bool, error) {
	if node.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("invalid agent.yaml: expected mapping node")
	}
	var matched *yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Tag == "!!str" && keyNode.Value == key {
			if matched != nil {
				return nil, false, fmt.Errorf("invalid agent.yaml: duplicate key %q", key)
			}
			matched = valueNode
		}
	}
	if matched == nil {
		return nil, false, nil
	}
	return matched, true, nil
}

func validateSeedPath(path string) error {
	if isAbsoluteSeedPath(path) {
		return fmt.Errorf("must be repo-relative")
	}
	if strings.ContainsAny(path, "*?[]") {
		return fmt.Errorf("must not contain glob characters")
	}

	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == "." || part == ".." {
			return fmt.Errorf("must not contain path traversal")
		}
	}

	return nil
}

func validateDuplicateKeys(node *yaml.Node) error {
	if node == nil {
		return nil
	}

	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			keyName, err := canonicalKeyName(keyNode)
			if err != nil {
				return err
			}
			if _, ok := seen[keyName]; ok {
				return fmt.Errorf("invalid agent.yaml: duplicate key %q", keyName)
			}
			seen[keyName] = struct{}{}
			if err := validateDuplicateKeys(keyNode); err != nil {
				return err
			}
			if err := validateDuplicateKeys(valueNode); err != nil {
				return err
			}
		}
		return nil
	}

	for _, child := range node.Content {
		if err := validateDuplicateKeys(child); err != nil {
			return err
		}
	}

	return nil
}

func canonicalKeyName(node *yaml.Node) (string, error) {
	if node == nil {
		return "", fmt.Errorf("invalid agent.yaml: nil key")
	}
	if node.Kind == yaml.ScalarNode {
		return node.Value, nil
	}

	encoded, err := yaml.Marshal(node)
	if err != nil {
		return "", fmt.Errorf("invalid agent.yaml: encode key: %w", err)
	}
	return string(encoded), nil
}

func validateAllowedKeys(node *yaml.Node, scope string, allowed ...string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}

	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		keyName, err := canonicalKeyName(keyNode)
		if err != nil {
			return err
		}
		if _, ok := allowedSet[keyName]; !ok {
			return fmt.Errorf("invalid %s: unknown key %q", scope, keyName)
		}
	}

	return nil
}

func isAbsoluteSeedPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}

	if strings.HasPrefix(path, `\`) {
		return true
	}

	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return true
	}

	if len(path) >= 3 && isWindowsDriveLetter(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}

	return false
}

func isWindowsDriveLetter(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}
