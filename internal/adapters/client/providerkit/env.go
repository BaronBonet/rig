package providerkit

import (
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

// EnvCommand prefixes command with env(1) so a provider runs with the
// configuration env decides for names: variables env leaves unset are removed
// and the others exported. That overrides whatever the task's shell inherited
// from the tmux server or exported through a direnv hook. Names env does not
// decide are left to the shell, and command is returned unchanged when env
// decides none.
func EnvCommand(env core.ProviderEnv, names []string, command []string) []string {
	var unset, set []string
	for _, name := range names {
		value, ok := env.Lookup(name)
		switch {
		case !ok:
		case value == "":
			unset = append(unset, "-u", name)
		default:
			set = append(set, name+"="+value)
		}
	}
	if len(unset)+len(set) == 0 {
		return command
	}
	// env(1) reads its options before any assignment, so every -u comes first.
	prefixed := append([]string{"env"}, unset...)
	prefixed = append(prefixed, set...)
	return append(prefixed, command...)
}

// EnvOverrides returns the variables among names that env decides, as a
// subprocess environment override where "" removes a variable, or nil when env
// decides none.
func EnvOverrides(env core.ProviderEnv, names []string) map[string]string {
	var overrides map[string]string
	for _, name := range names {
		value, ok := env.Lookup(name)
		if !ok {
			continue
		}
		if overrides == nil {
			overrides = make(map[string]string, len(names))
		}
		overrides[name] = value
	}
	return overrides
}

// AccountEnv returns the env that selects the account in dir through the
// variable name, which is left unset for defaultDir, the directory the
// provider uses without it.
func AccountEnv(name string, dir string, defaultDir string) core.ProviderEnv {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if defaultDir != "" && dir == filepath.Clean(defaultDir) {
		return core.ProviderEnv{name: ""}
	}
	return core.ProviderEnv{name: dir}
}

// TranscriptAccountDir returns the configuration directory a provider keeps
// the session transcript at path in: the parent of the folder named root that
// is depth folders above the transcript, as projects is two above
// <dir>/projects/<project>/<session>.jsonl. It reports false for a path laid
// out otherwise.
func TranscriptAccountDir(path string, root string, depth int) (string, bool) {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) || depth < 1 {
		return "", false
	}
	dir := filepath.Clean(path)
	for range depth {
		dir = filepath.Dir(dir)
	}
	if filepath.Base(dir) != root {
		return "", false
	}
	return filepath.Dir(dir), true
}
