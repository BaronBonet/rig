package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rig/internal/core"
)

type Seeder struct{}

func NewSeeder() *Seeder {
	return &Seeder{}
}

func (s *Seeder) SeedWorkspace(ctx context.Context, in core.SeedWorkspaceInput, progress func(string)) error {
	paths, err := prepareSeedPaths(in.RepoRoot, in.RelativePaths)
	if err != nil {
		return err
	}

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := seedPath(in.WorktreePath, path); err != nil {
			return err
		}
		if progress != nil {
			progress(path.Authored)
		}
	}

	return nil
}

func (s *Seeder) ValidateSeedPaths(ctx context.Context, repoRoot string, relativePaths []string) error {
	paths, err := prepareSeedPaths(repoRoot, relativePaths)
	if err != nil {
		return err
	}

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateSource(path); err != nil {
			return err
		}
	}

	return nil
}

type preparedSeedPath struct {
	Authored string
	Root     string
	Source   string
}

func prepareSeedPaths(repoRoot string, relativePaths []string) ([]preparedSeedPath, error) {
	paths := make([]preparedSeedPath, 0, len(relativePaths))
	seen := make([]string, 0, len(relativePaths))

	for _, authored := range relativePaths {
		cleaned, err := validateRelativePath(authored)
		if err != nil {
			return nil, fmt.Errorf("invalid seed path %q: %w", authored, err)
		}
		for _, existing := range seen {
			if cleaned == existing {
				return nil, fmt.Errorf("invalid seed path %q: duplicate path", authored)
			}
			if pathsOverlap(existing, cleaned) {
				return nil, fmt.Errorf("invalid seed path %q: overlaps with %q", authored, existing)
			}
		}
		seen = append(seen, cleaned)

		paths = append(paths, preparedSeedPath{
			Authored: authored,
			Root:     repoRoot,
			Source:   filepath.Join(repoRoot, cleaned),
		})
	}

	return paths, nil
}

func pathsOverlap(left, right string) bool {
	return hasPathPrefix(left, right) || hasPathPrefix(right, left)
}

func hasPathPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}

func validateSource(path preparedSeedPath) error {
	if err := ensurePathWithinRoot(path.Root, path.Source); err != nil {
		return err
	}
	if err := ensurePathComponentsSafe(path.Root, path.Source, false, "source"); err != nil {
		return err
	}

	info, err := os.Lstat(path.Source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("source %q not found", path.Source)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source %q is a symlink", path.Source)
	}

	if info.IsDir() {
		return validateDirectoryContents(path.Root, path.Source, path.Source, make(map[string]struct{}))
	}

	return nil
}

func seedPath(worktreePath string, path preparedSeedPath) error {
	if err := validateSource(path); err != nil {
		return err
	}

	info, err := os.Lstat(path.Source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("source %q not found", path.Authored)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source %q is a symlink", path.Authored)
	}

	dest := filepath.Join(worktreePath, path.Authored)
	if err := ensurePathWithinRoot(worktreePath, dest); err != nil {
		return err
	}
	if err := ensurePathComponentsSafe(worktreePath, dest, true, "destination"); err != nil {
		return err
	}
	if err := removeExisting(dest); err != nil {
		return err
	}

	if info.IsDir() {
		return copyDirectory(path.Root, path.Source, dest)
	}

	return copyFile(path.Source, dest, info)
}

func validateRelativePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("must not be empty")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return "", fmt.Errorf("must be repo-relative")
	}
	if len(path) >= 3 && isWindowsDriveLetter(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return "", fmt.Errorf("must be repo-relative")
	}

	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return "", fmt.Errorf("must not be empty")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("must not escape repo root")
	}

	return cleaned, nil
}

func removeExisting(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return os.RemoveAll(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ensurePathWithinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q escapes root %q", path, root)
	}
	return nil
}

func ensureResolvedPathWithinRoot(root, path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	return ensurePathWithinRoot(resolvedRoot, path)
}

func ensurePathComponentsSafe(root, path string, allowMissingLeaf bool, label string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}

	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if allowMissingLeaf {
					return nil
				}
				return fmt.Errorf("%s %q not found", label, current)
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s %q is a symlink", label, current)
		}
		if !info.IsDir() && i < len(parts)-1 {
			return fmt.Errorf("%s %q is not a directory", label, current)
		}
	}
	return nil
}

func validateDirectoryContents(repoRoot, root, current string, visited map[string]struct{}) error {
	resolvedCurrent, err := filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	if err := ensureResolvedPathWithinRoot(repoRoot, resolvedCurrent); err != nil {
		return fmt.Errorf("symlink target %q resolves outside repo root", current)
	}
	if _, seen := visited[resolvedCurrent]; seen {
		return nil
	}
	visited[resolvedCurrent] = struct{}{}

	entries, err := os.ReadDir(current)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryPath := filepath.Join(current, entry.Name())
		info, err := os.Lstat(entryPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(entryPath)
			if err != nil {
				return err
			}
			if err := ensureResolvedPathWithinRoot(repoRoot, target); err != nil {
				return fmt.Errorf("symlink target %q resolves outside repo root", entryPath)
			}
			continue
		}
		if entry.IsDir() {
			if err := validateDirectoryContents(repoRoot, root, entryPath, visited); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyDirectory(repoRoot, srcDir, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	return filepath.WalkDir(srcDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, rel)
		if err := ensurePathWithinRoot(destDir, destPath); err != nil {
			return err
		}

		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			resolvedTarget, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if err := ensureResolvedPathWithinRoot(repoRoot, resolvedTarget); err != nil {
				return fmt.Errorf("symlink target %q resolves outside repo root", path)
			}
			return os.Symlink(target, destPath)
		}

		if entry.IsDir() {
			return os.MkdirAll(destPath, info.Mode().Perm())
		}

		return copyFile(path, destPath, info)
	})
}

func copyFile(src, dest string, info os.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func isWindowsDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
