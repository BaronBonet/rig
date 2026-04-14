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
	if err := ensureMissing(dest); err != nil {
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

func ensureMissing(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("destination %q already exists", path)
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
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("%s %q is not a directory", label, current)
		}
	}

	return nil
}

func validateDirectoryContents(root, sourceRoot, dir string, seen map[string]struct{}) error {
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if err := ensureResolvedPathWithinRoot(root, resolvedDir); err != nil {
		return fmt.Errorf("source %q contains a symlink at %q resolving outside repo root", sourceRoot, dir)
	}
	if _, ok := seen[resolvedDir]; ok {
		return fmt.Errorf("source %q contains a symlink cycle at %q", sourceRoot, dir)
	}
	seen[resolvedDir] = struct{}{}
	defer delete(seen, resolvedDir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, resolvedInfo, err := resolveNestedSymlink(root, sourceRoot, path)
			if err != nil {
				return err
			}
			if resolvedInfo.IsDir() {
				if err := validateDirectoryContents(root, sourceRoot, resolved, seen); err != nil {
					return err
				}
			}
			continue
		}
		if info.IsDir() {
			if err := validateDirectoryContents(root, sourceRoot, path, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveNestedSymlink(root, sourceRoot, linkPath string) (string, fs.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return "", nil, fmt.Errorf("source %q contains a symlink at %q: %w", sourceRoot, linkPath, err)
	}
	if err := ensureResolvedPathWithinRoot(root, resolved); err != nil {
		return "", nil, fmt.Errorf(
			"source %q contains a symlink at %q resolving outside repo root",
			sourceRoot,
			linkPath,
		)
	}
	info, err := os.Stat(linkPath)
	if err != nil {
		return "", nil, fmt.Errorf("source %q contains a symlink at %q: %w", sourceRoot, linkPath, err)
	}
	return resolved, info, nil
}

func copyDirectory(root, source, dest string) error {
	return copyDirectoryContents(root, source, source, dest, make(map[string]struct{}))
}

func copyDirectoryContents(root, sourceRoot, source, dest string, seen map[string]struct{}) error {
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	if err := ensureResolvedPathWithinRoot(root, resolvedSource); err != nil {
		return fmt.Errorf("source %q contains a symlink at %q resolving outside repo root", sourceRoot, source)
	}
	if _, ok := seen[resolvedSource]; ok {
		return fmt.Errorf("source %q contains a symlink cycle at %q", sourceRoot, source)
	}
	seen[resolvedSource] = struct{}{}
	defer delete(seen, resolvedSource)

	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := ensureMissing(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(dest, sourceInfo.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chmod(dest, sourceInfo.Mode().Perm()); err != nil {
		return err
	}

	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(source, entry.Name())
		target := filepath.Join(dest, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, resolvedInfo, err := resolveNestedSymlink(root, sourceRoot, path)
			if err != nil {
				return err
			}
			if resolvedInfo.IsDir() {
				if err := copyDirectoryContents(root, sourceRoot, resolved, target, seen); err != nil {
					return err
				}
				continue
			}
			if err := copyFile(resolved, target, resolvedInfo); err != nil {
				return err
			}
			continue
		}
		if info.IsDir() {
			if err := copyDirectoryContents(root, sourceRoot, path, target, seen); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(path, target, info); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, dest string, info fs.FileInfo) error {
	if err := ensureMissing(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, info.Mode().Perm())
}

func isWindowsDriveLetter(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}
