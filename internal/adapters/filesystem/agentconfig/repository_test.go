package agentconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryLoadRepoConfig(t *testing.T) {
	t.Run("missing rig.yaml returns empty config", func(t *testing.T) {
		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), t.TempDir())
		require.NoError(t, err)
		require.False(t, cfg.Exists)
		require.Empty(t, cfg.Seed.Copy)
	})

	t.Run("valid yaml preserves authored seed copy entries", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  copy:
    - .env
    - local/
    - nested/path///
`), 0o644))

		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Equal(t, "rig.yaml", cfg.ConfigFileName)
		require.Equal(t, []string{".env", "local/", "nested/path///"}, cfg.Seed.Copy)
	})

	t.Run("hidden .rig.yaml is supported", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".rig.yaml"), []byte(`
seed:
  copy:
    - .env
  setup_script: scripts/setup.sh
`), 0o644))

		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Equal(t, ".rig.yaml", cfg.ConfigFileName)
		require.Equal(t, []string{".env"}, cfg.Seed.Copy)
		require.Equal(t, "scripts/setup.sh", cfg.Seed.SetupScript)
	})

	t.Run("both .rig.yaml and rig.yaml returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".rig.yaml"), []byte("seed:\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte("seed:\n"), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "only one of .rig.yaml or rig.yaml may exist")
	})

	t.Run("empty file returns existing empty config", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(""), 0o644))

		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Empty(t, cfg.Seed.Copy)
	})

	t.Run("invalid yaml returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte("seed:\n  copy: [\n"), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
	})

	t.Run("invalid hidden yaml returns an error mentioning .rig.yaml", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".rig.yaml"), []byte("seed:\n  copy: [\n"), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "parse .rig.yaml")
	})

	t.Run("null values return an error", func(t *testing.T) {
		tests := []struct {
			name    string
			body    string
			wantErr string
		}{
			{
				name:    "null seed",
				body:    "seed: null\n",
				wantErr: "seed must be a mapping",
			},
			{
				name:    "null seed copy",
				body:    "seed:\n  copy: null\n",
				wantErr: "seed.copy must be a sequence",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repoRoot := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(tc.body), 0o644))

				repo := NewLoader()

				_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
			})
		}
	})

	t.Run("unknown keys return an error", func(t *testing.T) {
		tests := []struct {
			name    string
			body    string
			wantErr string
		}{
			{
				name:    "unknown root key",
				body:    "sead:\n  copy:\n    - .env\n",
				wantErr: "unknown key \"sead\"",
			},
			{
				name:    "unknown nested key",
				body:    "seed:\n  copies:\n    - .env\n",
				wantErr: "unknown key \"copies\"",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repoRoot := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(tc.body), 0o644))

				repo := NewLoader()

				_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
			})
		}
	})

	t.Run("invalid seed copy entries return an error", func(t *testing.T) {
		tests := []struct {
			name    string
			body    string
			wantErr string
		}{
			{
				name:    "non-string entry",
				body:    "seed:\n  copy:\n    - {path: .env}\n",
				wantErr: "must be a string",
			},
			{
				name:    "empty entry",
				body:    "seed:\n  copy:\n    - \"\"\n",
				wantErr: "must not be empty",
			},
			{
				name:    "absolute path",
				body:    "seed:\n  copy:\n    - /tmp/secret\n",
				wantErr: "must be repo-relative",
			},
			{
				name:    "windows absolute path",
				body:    "seed:\n  copy:\n    - 'C:\\\\secret\\\\file'\n",
				wantErr: "must be repo-relative",
			},
			{
				name:    "unc path",
				body:    "seed:\n  copy:\n    - '\\\\\\\\server\\\\share\\\\file'\n",
				wantErr: "must be repo-relative",
			},
			{
				name:    "windows rooted path",
				body:    "seed:\n  copy:\n    - '\\foo\\bar'\n",
				wantErr: "must be repo-relative",
			},
			{
				name:    "path traversal",
				body:    "seed:\n  copy:\n    - ../secret\n",
				wantErr: "must not contain path traversal",
			},
			{
				name:    "glob pattern",
				body:    "seed:\n  copy:\n    - '*.env'\n",
				wantErr: "must not contain glob characters",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repoRoot := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(tc.body), 0o644))

				repo := NewLoader()

				_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
			})
		}
	})

	t.Run("setup_script with unknown nested key still errors", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(
			t,
			os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte("seed:\n  copies:\n    - .env\n"), 0o644),
		)

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "unknown key \"copies\"")
	})

	t.Run("duplicate keys return an error", func(t *testing.T) {
		tests := []struct {
			name    string
			body    string
			wantErr string
		}{
			{
				name:    "duplicate seed key",
				body:    "seed:\n  copy:\n    - .env\nseed:\n  copy:\n    - local/\n",
				wantErr: "duplicate key \"seed\"",
			},
			{
				name:    "duplicate copy key",
				body:    "seed:\n  copy:\n    - .env\n  copy:\n    - local/\n",
				wantErr: "duplicate key \"copy\"",
			},
			{
				name:    "duplicate unknown root key",
				body:    "other: 1\nother: 2\n",
				wantErr: "duplicate key \"other\"",
			},
			{
				name:    "duplicate unknown nested key",
				body:    "seed:\n  other: 1\n  other: 2\n",
				wantErr: "duplicate key \"other\"",
			},
			{
				name:    "duplicate non-string key",
				body:    "1: a\n1: b\n",
				wantErr: "duplicate key \"1\"",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				repoRoot := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(tc.body), 0o644))

				repo := NewLoader()

				_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
			})
		}
	})
}

func TestRepositoryLoadRepoConfig_ParsesSetupScript(t *testing.T) {
	t.Run("valid setup_script is returned in config", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: scripts/setup.sh
`), 0o644))

		repo := NewLoader()
		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Equal(t, "scripts/setup.sh", cfg.Seed.SetupScript)
	})

	t.Run("setup_script with copy paths", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  copy:
    - .env
  setup_script: scripts/setup.sh
`), 0o644))

		repo := NewLoader()
		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Equal(t, []string{".env"}, cfg.Seed.Copy)
		require.Equal(t, "scripts/setup.sh", cfg.Seed.SetupScript)
	})

	t.Run("empty setup_script is treated as absent", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: ""
`), 0o644))

		repo := NewLoader()
		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Empty(t, cfg.Seed.SetupScript)
	})

	t.Run("non-string setup_script returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: [a, b]
`), 0o644))

		repo := NewLoader()
		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "setup_script must be a string")
	})

	t.Run("null setup_script returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: null
`), 0o644))

		repo := NewLoader()
		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "setup_script must be a string")
	})

	t.Run("setup_script with path traversal returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: ../evil.sh
`), 0o644))

		repo := NewLoader()
		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "must not contain path traversal")
	})

	t.Run("setup_script with absolute path returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: /tmp/evil.sh
`), 0o644))

		repo := NewLoader()
		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "must be repo-relative")
	})

	t.Run("setup_script with glob pattern returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "rig.yaml"), []byte(`
seed:
  setup_script: "scripts/*.sh"
`), 0o644))

		repo := NewLoader()
		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "must not contain glob characters")
	})
}
