// Package userconfig persists the User config, the user's provider setup,
// repo directories and Launch defaults, in a user-level config file. It
// intentionally lives here rather than in the task SQLite database so task
// state and user preferences stay separate.
package userconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BaronBonet/rig/internal/core"
)

const currentConfigVersion = 1

type Config struct {
	// Path is the user config file location. Empty resolves to
	// ~/.config/rig/config.json.
	Path string `env:"RIG_USER_CONFIG_PATH"`
}

// DefaultConfigPath returns the default user-level config file location.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".rig", "config.json")
	}

	return filepath.Join(home, ".config", "rig", "config.json")
}

type store struct {
	path string
	// defaultProviderOverride is the runtime default-provider override
	// (RIG_PROVIDER). It can only select a provider that is already
	// configured; it can never bypass provider setup.
	defaultProviderOverride core.Provider
	// mu serializes each save's read-modify-write of the config file, which
	// the daemon may run for Provider setup, repo directories and Launch
	// defaults at once.
	// Reads need no lock: a save replaces the file in one rename.
	mu sync.Mutex
}

func New(cfg Config, defaultProviderOverride core.Provider) core.UserConfigStore {
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = DefaultConfigPath()
	}

	return &store{
		path:                    path,
		defaultProviderOverride: defaultProviderOverride,
	}
}

// The config file's top-level fields. Version is written but not checked on
// read, so fields are added without bumping it: files written before a field
// existed simply lack it.
const (
	fieldVersion             = "version"
	fieldConfiguredProviders = "configured_providers"
	fieldDefaultProvider     = "default_provider"
	fieldRepoDirs            = "repo_dirs"
	fieldLaunchDefaults      = "launch_defaults"
)

// configFile is the config file as read from disk.
type configFile struct {
	raw []byte
	// fields are its top-level fields, undecoded. A save replaces only the
	// fields it owns and writes the rest back as they were, so it keeps any
	// this rig does not know, such as one a newer rig or an edit by hand
	// added.
	fields map[string]json.RawMessage
}

// setupFields is the on-disk shape of the Provider setup.
type setupFields struct {
	ConfiguredProviders []core.Provider `json:"configured_providers"`
	DefaultProvider     core.Provider   `json:"default_provider"`
}

// repoDirsFields is the on-disk shape of the repo directories. Configs
// written before Rig had them leave the field out, which means none.
type repoDirsFields struct {
	RepoDirs []string `json:"repo_dirs"`
}

// launchDefaultEntry is the on-disk shape of one provider's Launch default,
// an entry of launch_defaults keyed by provider. It has its own JSON tags, so
// the file format never follows a change to core.LaunchChoice.
type launchDefaultEntry struct {
	Model          string `json:"model,omitempty"`
	ReasoningLevel string `json:"reasoning_level,omitempty"`
}

func (s *store) GetProviderSetup(_ context.Context) (*core.ProviderSetup, error) {
	file, err := s.readConfigFile()
	if err != nil || file == nil {
		return nil, err
	}

	setup, err := s.savedProviderSetup(file)
	if err != nil {
		return nil, err
	}

	if s.defaultProviderOverride != "" {
		if !setup.IsConfigured(s.defaultProviderOverride) {
			return nil, fmt.Errorf(
				"RIG_PROVIDER %q is not a configured provider: run rig setup to enable it",
				s.defaultProviderOverride,
			)
		}
		setup.Default = s.defaultProviderOverride
	}

	return &setup, nil
}

// savedProviderSetup decodes and validates the Provider setup file holds, as
// saved, without the default-provider override.
func (s *store) savedProviderSetup(file *configFile) (core.ProviderSetup, error) {
	var fields setupFields
	if err := json.Unmarshal(file.raw, &fields); err != nil {
		return core.ProviderSetup{}, fmt.Errorf("decode user config %s: %w", s.path, err)
	}
	setup := core.ProviderSetup{
		Configured: fields.ConfiguredProviders,
		Default:    fields.DefaultProvider,
	}
	if err := setup.Validate(); err != nil {
		return core.ProviderSetup{}, fmt.Errorf("invalid user config %s: %w", s.path, err)
	}
	return setup, nil
}

// SaveProviderSetup replaces the Provider setup and keeps every other field,
// the repo directories and Launch defaults among them. A config file it cannot read or decode is
// replaced without them, as before Launch defaults existed: Provider setup is
// how the user repairs a broken config.
func (s *store) SaveProviderSetup(_ context.Context, setup core.ProviderSetup) error {
	if err := setup.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fields := make(map[string]json.RawMessage)
	if file, err := s.readConfigFile(); err == nil && file != nil {
		fields = file.fields
	}
	if err := setField(fields, fieldConfiguredProviders, setup.Configured); err != nil {
		return err
	}
	if err := setField(fields, fieldDefaultProvider, setup.Default); err != nil {
		return err
	}

	return s.writeConfigFile(fields)
}

func (s *store) GetRepoDirs(_ context.Context) ([]string, error) {
	file, err := s.readConfigFile()
	if err != nil || file == nil {
		return nil, err
	}

	var fields repoDirsFields
	if err := json.Unmarshal(file.raw, &fields); err != nil {
		return nil, fmt.Errorf("decode user config %s: %w", s.path, err)
	}
	return fields.RepoDirs, nil
}

// SaveRepoDirs replaces the repo directories, leaving the field out when
// there are none, and keeps every other field. It never writes a config
// without a valid Provider setup, which GetProviderSetup would reject as
// invalid instead of reporting that setup has never completed.
func (s *store) SaveRepoDirs(_ context.Context, dirs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.readConfigFile()
	if err != nil {
		return err
	}
	if file == nil {
		return core.ErrProviderSetupRequired
	}
	if _, err := s.savedProviderSetup(file); err != nil {
		return err
	}

	if len(dirs) == 0 {
		delete(file.fields, fieldRepoDirs)
	} else if err := setField(file.fields, fieldRepoDirs, dirs); err != nil {
		return err
	}

	return s.writeConfigFile(file.fields)
}

// GetLaunchDefaults returns the saved Launch defaults. Without a config file
// there are none. It skips any entry it cannot use, which only an edit by hand
// leaves: one that does not decode, is empty, or is for an unsupported
// provider. A launch_defaults field that is not an object holds none.
func (s *store) GetLaunchDefaults(_ context.Context) (map[core.Provider]core.LaunchChoice, error) {
	file, err := s.readConfigFile()
	if err != nil {
		return nil, err
	}

	defaults := make(map[core.Provider]core.LaunchChoice)
	if file == nil {
		return defaults, nil
	}
	for key, raw := range file.launchDefaultEntries() {
		provider := core.Provider(key)
		choice := decodeLaunchDefault(raw)
		if !core.IsSupportedProvider(provider) || choice.IsZero() {
			continue
		}
		defaults[provider] = choice
	}

	return defaults, nil
}

// SaveLaunchDefault sets provider's Launch default, or deletes it when choice
// is empty, and keeps every other field and entry, even those it cannot
// decode. Without a config file it saves nothing: a config file needs a valid
// Provider setup, and task creation, which saves Launch defaults, requires
// Provider setup anyway. A config file it cannot decode is left alone.
func (s *store) SaveLaunchDefault(_ context.Context, provider core.Provider, choice core.LaunchChoice) error {
	if !core.IsSupportedProvider(provider) {
		return fmt.Errorf("provider %q is not a supported provider", provider)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.readConfigFile()
	if err != nil || file == nil {
		return err
	}

	entries := file.launchDefaultEntries()
	key := string(provider)
	if choice.IsZero() {
		if _, ok := entries[key]; !ok {
			return nil
		}
		delete(entries, key)
	} else {
		if decodeLaunchDefault(entries[key]) == choice {
			return nil
		}
		entry, err := json.Marshal(launchDefaultEntry{Model: choice.Model, ReasoningLevel: choice.ReasoningLevel})
		if err != nil {
			return fmt.Errorf("encode launch default: %w", err)
		}
		entries[key] = entry
	}

	if len(entries) == 0 {
		delete(file.fields, fieldLaunchDefaults)
	} else if err := setField(file.fields, fieldLaunchDefaults, entries); err != nil {
		return err
	}

	return s.writeConfigFile(file.fields)
}

// readConfigFile reads the config file as it is on disk, without the
// default-provider override. It returns nil, and no error, when there is no
// config file, and an error when the file is not a JSON object.
func (s *store) readConfigFile() (*configFile, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read user config %s: %w", s.path, err)
	}

	file := configFile{raw: raw}
	if err := json.Unmarshal(raw, &file.fields); err != nil {
		return nil, fmt.Errorf("decode user config %s: %w", s.path, err)
	}
	if file.fields == nil {
		file.fields = make(map[string]json.RawMessage)
	}

	return &file, nil
}

// launchDefaultEntries returns the entries of the launch_defaults field,
// undecoded and keyed by provider. A field that is missing or not an object
// holds none.
func (f *configFile) launchDefaultEntries() map[string]json.RawMessage {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(f.fields[fieldLaunchDefaults], &entries); err != nil || entries == nil {
		return make(map[string]json.RawMessage)
	}
	return entries
}

// decodeLaunchDefault decodes one launch_defaults entry, or returns an empty
// choice when it does not decode.
func decodeLaunchDefault(raw json.RawMessage) core.LaunchChoice {
	var entry launchDefaultEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return core.LaunchChoice{}
	}
	return core.LaunchChoice{Model: entry.Model, ReasoningLevel: entry.ReasoningLevel}
}

func setField(fields map[string]json.RawMessage, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode user config %s: %w", name, err)
	}
	fields[name] = raw
	return nil
}

// writeConfigFile atomically replaces the config file with fields at the
// current version: it writes a private temp file beside it, flushes it to
// disk, then renames it into place. When the config path is a symlink, as in
// a dotfiles setup, it replaces the file the link points to and keeps the
// link.
func (s *store) writeConfigFile(fields map[string]json.RawMessage) error {
	if err := setField(fields, fieldVersion, currentConfigVersion); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user config: %w", err)
	}
	payload = append(payload, '\n')

	path, err := s.writePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create user config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("create user config temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write user config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure user config permissions: %w", err)
	}
	// Without a sync, a crash soon after the rename can leave an empty file.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync user config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close user config temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace user config %s: %w", path, err)
	}

	return nil
}

// writePath returns the file a save replaces: the config path, or the file
// it links to when it is a symlink. A path that does not exist yet, or a
// symlink to nothing, is written as is.
func (s *store) writePath() (string, error) {
	path, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.path, nil
		}
		return "", fmt.Errorf("resolve user config %s: %w", s.path, err)
	}
	return path, nil
}
