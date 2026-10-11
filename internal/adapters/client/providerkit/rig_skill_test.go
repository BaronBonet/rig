package providerkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWriteRigSkill_InstallsTheSkillInANewDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills", "rig", "SKILL.md")

	require.NoError(t, WriteRigSkill(path))

	installed, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, RigSkill(), installed)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	// Nothing but the skill is left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoError(t, CheckRigSkill(path))
}

func TestWriteRigSkill_ReplacesAnEarlierSkill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte("---\nname: rig\n---\nAn earlier rig skill.\n"), 0o600))

	require.NoError(t, WriteRigSkill(path))

	installed, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, RigSkill(), installed)
	require.NoError(t, CheckRigSkill(path))
}

func TestWriteRigSkill_FailsWithoutLeavingATempFileWhenThePathIsADirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Join(path, "kept"), 0o755))

	require.Error(t, WriteRigSkill(path))

	require.DirExists(t, filepath.Join(path, "kept"))
	requireNoTempFiles(t, dir)
}

func TestWriteRigSkill_FailsAndKeepsTheSkillWhenItsDirectoryIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	earlier := []byte("An earlier rig skill.\n")
	require.NoError(t, os.WriteFile(path, earlier, 0o644))
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	require.Error(t, WriteRigSkill(path))

	kept, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, earlier, kept)
	requireNoTempFiles(t, dir)
}

// requireNoTempFiles fails when dir holds a temp file of WriteRigSkill.
func requireNoTempFiles(t *testing.T, dir string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, ".rig-skill-*"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestRigSkill_HasFrontmatterNamingIt(t *testing.T) {
	rest, ok := strings.CutPrefix(string(RigSkill()), "---\n")
	require.True(t, ok, "the skill must open with YAML frontmatter")
	rawFrontmatter, _, ok := strings.Cut(rest, "\n---\n")
	require.True(t, ok, "the skill's frontmatter must be closed")

	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(rawFrontmatter), &frontmatter))
	require.Equal(t, "rig", frontmatter.Name)
	require.NotEmpty(t, frontmatter.Description)
	// Providers cap the length of a skill's description; 500 characters fits
	// all of them.
	require.LessOrEqual(t, len(frontmatter.Description), 500)
}

func TestCheckRigSkill_FailsWhenTheSkillIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig", "SKILL.md")

	err := CheckRigSkill(path)

	require.ErrorContains(t, err, "not installed at "+path)
	require.ErrorContains(t, err, "run rig setup")
}

func TestCheckRigSkill_FailsWhenTheSkillDiffers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	require.NoError(t, os.WriteFile(path, append(RigSkill(), "An edit.\n"...), 0o644))

	err := CheckRigSkill(path)

	require.ErrorContains(t, err, path+" differs from the one this rig version installs")
	require.ErrorContains(t, err, "run rig setup")
}
