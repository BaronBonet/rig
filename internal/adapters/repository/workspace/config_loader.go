package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rig/internal/core"

	"gopkg.in/yaml.v3"
)

const (
	hiddenConfigName = ".rig.yaml"
	legacyConfigName = "rig.yaml"
)

func loadRepoConfig(repoRoot string) (core.RepoConfig, error) {
	configName, raw, err := readRepoConfig(repoRoot)
	if err != nil {
		return core.RepoConfig{}, err
	}
	if configName == "" {
		return core.RepoConfig{}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return core.RepoConfig{}, fmt.Errorf("parse %s: %w", configName, err)
	}
	if err := validateDuplicateKeys(&doc, configName); err != nil {
		return core.RepoConfig{}, err
	}

	seed, err := parseSeed(&doc, configName)
	if err != nil {
		return core.RepoConfig{}, err
	}

	return core.RepoConfig{
		Exists:         true,
		ConfigFileName: configName,
		Seed:           seed,
	}, nil
}

func readRepoConfig(repoRoot string) (string, []byte, error) {
	hiddenExists, err := fileExists(filepath.Join(repoRoot, hiddenConfigName))
	if err != nil {
		return "", nil, err
	}
	legacyExists, err := fileExists(filepath.Join(repoRoot, legacyConfigName))
	if err != nil {
		return "", nil, err
	}

	if hiddenExists && legacyExists {
		return "", nil, fmt.Errorf("only one of %s or %s may exist", hiddenConfigName, legacyConfigName)
	}

	configName := ""
	switch {
	case hiddenExists:
		configName = hiddenConfigName
	case legacyExists:
		configName = legacyConfigName
	default:
		return "", nil, nil
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot, configName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, nil
		}
		return "", nil, err
	}

	return configName, raw, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func parseSeed(doc *yaml.Node, configName string) (core.SeedConfig, error) {
	root, err := documentRoot(doc, configName)
	if err != nil {
		return core.SeedConfig{}, err
	}
	if root == nil {
		return core.SeedConfig{}, nil
	}
	if root.Kind != yaml.MappingNode {
		return core.SeedConfig{}, fmt.Errorf("invalid %s: root must be a mapping", configName)
	}
	if err := validateAllowedKeys(root, configName, configName, "seed"); err != nil {
		return core.SeedConfig{}, err
	}

	seedNode, ok, err := lookupMapping(root, "seed", configName)
	if err != nil {
		return core.SeedConfig{}, err
	}
	if !ok {
		return core.SeedConfig{}, nil
	}
	if seedNode.Kind != yaml.MappingNode {
		return core.SeedConfig{}, fmt.Errorf("invalid %s: seed must be a mapping", configName)
	}
	if err := validateAllowedKeys(seedNode, configName, "seed", "copy", "setup_script"); err != nil {
		return core.SeedConfig{}, err
	}

	copyPaths, err := parseSeedCopy(seedNode, configName)
	if err != nil {
		return core.SeedConfig{}, err
	}

	setupScript, err := parseSeedSetupScript(seedNode, configName)
	if err != nil {
		return core.SeedConfig{}, err
	}

	return core.SeedConfig{
		Copy:        copyPaths,
		SetupScript: setupScript,
	}, nil
}

func parseSeedCopy(seedNode *yaml.Node, configName string) ([]string, error) {
	copyNode, ok, err := lookupMapping(seedNode, "copy", configName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if copyNode.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("invalid %s: seed.copy must be a sequence", configName)
	}

	paths := make([]string, 0, len(copyNode.Content))
	for i, item := range copyNode.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, fmt.Errorf("invalid %s: seed.copy[%d] must be a string", configName, i)
		}

		path := item.Value
		if path == "" {
			return nil, fmt.Errorf("invalid %s: seed.copy[%d] must not be empty", configName, i)
		}
		if err := validateSeedPath(path); err != nil {
			return nil, fmt.Errorf("invalid %s: seed.copy[%d] %w", configName, i, err)
		}

		paths = append(paths, path)
	}

	return paths, nil
}

func parseSeedSetupScript(seedNode *yaml.Node, configName string) (string, error) {
	setupScriptNode, ok, err := lookupMapping(seedNode, "setup_script", configName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	if setupScriptNode.Kind != yaml.ScalarNode || setupScriptNode.Tag != "!!str" {
		return "", fmt.Errorf("invalid %s: seed.setup_script must be a string", configName)
	}
	setupScript := setupScriptNode.Value
	if setupScript != "" {
		if err := validateSeedPath(setupScript); err != nil {
			return "", fmt.Errorf("invalid %s: seed.setup_script %w", configName, err)
		}
	}
	return setupScript, nil
}

func documentRoot(doc *yaml.Node, configName string) (*yaml.Node, error) {
	if doc == nil || len(doc.Content) == 0 {
		return nil, nil
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("invalid %s: expected a document", configName)
	}
	return doc.Content[0], nil
}

func lookupMapping(node *yaml.Node, key string, configName string) (*yaml.Node, bool, error) {
	if node.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("invalid %s: expected mapping node", configName)
	}
	var matched *yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Tag == "!!str" && keyNode.Value == key {
			if matched != nil {
				return nil, false, fmt.Errorf("invalid %s: duplicate key %q", configName, key)
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

func validateDuplicateKeys(node *yaml.Node, configName string) error {
	if node == nil {
		return nil
	}

	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			keyName, err := canonicalKeyName(keyNode, configName)
			if err != nil {
				return err
			}
			if _, exists := seen[keyName]; exists {
				return fmt.Errorf("invalid %s: duplicate key %q", configName, keyName)
			}
			seen[keyName] = struct{}{}
			if err := validateDuplicateKeys(valueNode, configName); err != nil {
				return err
			}
		}
		return nil
	}

	for _, child := range node.Content {
		if err := validateDuplicateKeys(child, configName); err != nil {
			return err
		}
	}

	return nil
}

func canonicalKeyName(node *yaml.Node, configName string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("invalid %s: map keys must be strings", configName)
	}
	return node.Value, nil
}

func validateAllowedKeys(node *yaml.Node, configName string, path string, allowedKeys ...string) error {
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}

	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		keyName, err := canonicalKeyName(keyNode, configName)
		if err != nil {
			return err
		}
		if _, ok := allowed[keyName]; !ok {
			return fmt.Errorf("invalid %s: %s contains unknown key %q", configName, path, keyName)
		}
	}
	return nil
}

func isAbsoluteSeedPath(path string) bool {
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) {
		return true
	}
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return true
	}
	return len(path) >= 3 && isWindowsDriveLetter(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}
