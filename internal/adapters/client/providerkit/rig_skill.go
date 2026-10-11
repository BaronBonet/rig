package providerkit

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// rigSkill is the Rig skill: the instructions that teach a provider's agents
// when and how to hand work off to a new task with the rig CLI. Every
// provider installs the same text, each in its own user-level skills
// directory.
//
//go:embed rig_skill.md
var rigSkill []byte

// RigSkill returns the Rig skill this Rig version installs.
func RigSkill() []byte {
	return bytes.Clone(rigSkill)
}

// WriteRigSkill installs the Rig skill at path, creating its directory and
// replacing whatever file is there. The file is replaced atomically, so a
// provider reading its skills never sees a partial one.
func WriteRigSkill(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create rig skill dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".rig-skill-*")
	if err != nil {
		return fmt.Errorf("write rig skill: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	_, writeErr := tmp.Write(rigSkill)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write rig skill: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("write rig skill: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install rig skill at %s: %w", path, err)
	}

	return nil
}

// CheckRigSkill fails when path does not hold the Rig skill this Rig version
// installs: when it is missing, or an earlier or edited copy. Either way
// rig setup installs the current one. Its errors are terse, for rig doctor to
// show beside the check's name.
func CheckRigSkill(path string) error {
	installed, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("not installed at %s: run rig setup to install it", path)
	case err != nil:
		return fmt.Errorf("read rig skill: %w", err)
	case !bytes.Equal(installed, rigSkill):
		return fmt.Errorf("%s differs from the one this rig version installs: run rig setup to update it", path)
	}

	return nil
}
