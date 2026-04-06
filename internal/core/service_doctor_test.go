package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceDoctor_ReturnsMissingBinaryFailures(t *testing.T) {
	svc := newTestService()
	svc.providerRepo.isAvailableErr = errors.New("missing codex")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "provider(codex): missing codex")
}

func TestDoctor_ReportsTaskRepositoryAvailabilityFailure(t *testing.T) {
	svc := newTestService()
	svc.taskRepo.isAvailableErr = errors.New("sqlite unavailable")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "storage: sqlite unavailable")
}

func TestServiceDoctor_ReportsRepoDetectionFailure(t *testing.T) {
	svc := newTestService()
	svc.repoClient.detectRepoErr = errors.New("not a git repo")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "repo: not a git repo")
}

func TestServiceDoctor_NotesMissingRepoConfigWhenRepoDetected(t *testing.T) {
	svc := newTestService()

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: agent.yaml not found")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_NotesLoadedEmptyRepoConfig(t *testing.T) {
	svc := newTestService()
	svc.configRepo.repoConfig = RepoConfig{Exists: true}

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: loaded agent.yaml")
	require.NotContains(t, result.Notes, "config: agent.yaml not found")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_ReportsValidSeedPathsAsNotes(t *testing.T) {
	svc := newTestService()
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed:   SeedConfig{Copy: []string{".env", "local/"}},
	}

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: loaded agent.yaml")
	require.Contains(t, result.Notes, "config: seed path ok: .env")
	require.Contains(t, result.Notes, "config: seed path ok: local/")
	require.Equal(t, "/tmp/repo", svc.workspaceSeeder.validateRepoRoot)
	require.Equal(t, []string{".env", "local/"}, svc.workspaceSeeder.validatePaths)
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_ReportsInvalidRepoConfigAsFailure(t *testing.T) {
	svc := newTestService()
	svc.configRepo.loadErr = errors.New("parse agent.yaml: invalid yaml")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "config: parse agent.yaml: invalid yaml")
}

func TestServiceDoctor_ReportsInvalidSeedPathsAsFailure(t *testing.T) {
	svc := newTestService()
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed:   SeedConfig{Copy: []string{".env"}},
	}
	svc.workspaceSeeder.validateErr = errors.New("invalid seed path \".env\": not found")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: loaded agent.yaml")
	require.Contains(t, result.Failures, "config: invalid seed path \".env\": not found")
}
