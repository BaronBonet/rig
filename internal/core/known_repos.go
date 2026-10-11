package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// knownRepos derives the Known repositories, which nothing stores: the
// repositories of existing tasks, plus the primary git checkouts directly
// inside the user's repo directories. It is shared by the repository listing
// and by task creation, which resolves a target repository given by name
// against it.
type knownRepos struct {
	tasks       TaskRepository
	gitWorktree GitWorktreeClient
	userConfig  UserConfigStore
}

func newKnownRepos(tasks TaskRepository, gitWorktree GitWorktreeClient, userConfig UserConfigStore) *knownRepos {
	return &knownRepos{
		tasks:       tasks,
		gitWorktree: gitWorktree,
		userConfig:  userConfig,
	}
}

// list returns the known repositories, each once, sorted by name, ignoring
// case, then by root. A repository with tasks takes its tasks' repository
// name, and one found only in a repo directory the base name of its root.
// Repo directories that cannot be used or listed, such as ones that do not
// exist, are skipped: the doctor reports them.
func (r *knownRepos) list(ctx context.Context) ([]KnownRepo, error) {
	tasks, err := r.tasks.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	dirs, _, err := r.usableRepoDirs(ctx)
	if err != nil {
		return nil, err
	}

	byRoot := make(map[string]*KnownRepo)
	for _, task := range tasks {
		if task == nil || strings.TrimSpace(task.RepoRoot) == "" {
			continue
		}
		root := filepath.Clean(strings.TrimSpace(task.RepoRoot))
		repo, ok := byRoot[root]
		if !ok {
			name := strings.TrimSpace(task.RepoName)
			if name == "" {
				name = filepath.Base(root)
			}
			repo = &KnownRepo{Name: name, Root: root}
			byRoot[root] = repo
		}
		repo.TaskCount++
	}

	for _, dir := range dirs {
		checkouts, err := r.gitWorktree.ListRepoCheckouts(ctx, dir)
		if err != nil {
			continue
		}
		for _, checkout := range checkouts {
			root := filepath.Clean(checkout)
			if _, ok := byRoot[root]; !ok {
				byRoot[root] = &KnownRepo{Name: filepath.Base(root), Root: root}
			}
		}
	}

	repos := make([]KnownRepo, 0, len(byRoot))
	for _, repo := range byRoot {
		repos = append(repos, *repo)
	}
	slices.SortFunc(repos, func(a, b KnownRepo) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Root, b.Root),
		)
	})
	return repos, nil
}

// named returns the known repositories with the name, ignoring case.
func (r *knownRepos) named(ctx context.Context, name string) ([]KnownRepo, error) {
	repos, err := r.list(ctx)
	if err != nil {
		return nil, err
	}
	var matches []KnownRepo
	for _, repo := range repos {
		if strings.EqualFold(repo.Name, name) {
			matches = append(matches, repo)
		}
	}
	return matches, nil
}

// savedRepoDirs returns the user's repo directories as the User config holds
// them.
func (r *knownRepos) savedRepoDirs(ctx context.Context) ([]string, error) {
	if r.userConfig == nil {
		return nil, errors.New("user config store not configured")
	}
	dirs, err := r.userConfig.GetRepoDirs(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the repo directories: %w", err)
	}
	return dirs, nil
}

// usableRepoDirs returns the user's repo directories normalized as
// SaveRepoDirs saves them, since the User config may have been edited by
// hand, leaving out the ones that cannot be used, such as relative paths,
// and returning why each cannot.
func (r *knownRepos) usableRepoDirs(ctx context.Context) ([]string, []error, error) {
	dirs, err := r.savedRepoDirs(ctx)
	if err != nil {
		return nil, nil, err
	}
	usable, unusable := normalizeRepoDirs(dirs)
	return usable, unusable, nil
}

// healthCheck reports the repo directories that cannot be used, do not
// exist, or cannot be listed, as an optional check: their repositories are
// missing from the known repositories, but Rig works without them.
func (r *knownRepos) healthCheck(ctx context.Context) HealthCheck {
	check := HealthCheck{Name: "repo dirs"}
	dirs, unusable, err := r.usableRepoDirs(ctx)
	if err != nil {
		check.Err = err
		return check
	}

	var missing, problems []string
	for _, dir := range dirs {
		_, err := r.gitWorktree.ListRepoCheckouts(ctx, dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, dir)
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
		}
	}
	if len(missing) > 0 {
		problems = append([]string{"not found: " + strings.Join(missing, ", ")}, problems...)
	}
	for _, err := range unusable {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		check.Err = fmt.Errorf("%s (run rig setup to change them)", strings.Join(problems, "; "))
	}
	return check
}

func (s *service) ListRepos(ctx context.Context) ([]KnownRepo, error) {
	return s.repos.list(ctx)
}

func (s *service) GetRepoDirs(ctx context.Context) ([]string, error) {
	return s.repos.savedRepoDirs(ctx)
}

func (s *service) SaveRepoDirs(ctx context.Context, dirs []string) error {
	if s.userConfig == nil {
		return errors.New("user config store not configured")
	}
	normalized, unusable := normalizeRepoDirs(dirs)
	if len(unusable) > 0 {
		return unusable[0]
	}
	return s.userConfig.SaveRepoDirs(ctx, normalized)
}

func (s *service) SuggestRepoDir(ctx context.Context, cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if !filepath.IsAbs(cwd) {
		// Setup may not know its working directory, and a relative one would
		// resolve against the daemon's.
		return "", nil
	}
	repoCtx, err := s.gitWorktree.DetectRepo(ctx, cwd)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		// The suggestion only prefills Setup, so a working directory that
		// repository detection fails for, such as one outside any git
		// repository, suggests nothing.
		return "", nil
	}
	root := strings.TrimSpace(repoCtx.Root)
	if root == "" {
		return "", nil
	}
	return filepath.Dir(filepath.Clean(root)), nil
}

// normalizeRepoDirs returns the repo directories as Rig saves and uses them:
// clean absolute paths, with a leading ~ or ~/ expanded to the home
// directory, in the order given, without blanks or repeats. It leaves out
// each directory that is neither absolute nor under ~, returning an error
// naming it.
func normalizeRepoDirs(dirs []string) ([]string, []error) {
	normalized := make([]string, 0, len(dirs))
	var unusable []error
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		path, err := normalizeRepoDir(dir)
		if err != nil {
			unusable = append(unusable, err)
			continue
		}
		if !slices.Contains(normalized, path) {
			normalized = append(normalized, path)
		}
	}
	return normalized, unusable
}

// normalizeRepoDir returns a repo directory as a clean absolute path, with a
// leading ~ or ~/ expanded to the home directory.
func normalizeRepoDir(dir string) (string, error) {
	path, err := expandHome(dir)
	if err != nil {
		return "", fmt.Errorf("repo directory %q: %w", dir, err)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("repo directory %q must be an absolute path or start with ~/", dir)
	}
	return filepath.Clean(path), nil
}
