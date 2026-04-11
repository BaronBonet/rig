package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestServiceDoctor_ReturnsMissingBinaryFailures(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.isAvailableErr = errors.New("missing codex")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "provider(codex): missing codex")
}

func TestDoctor_ReportsTaskRepositoryAvailabilityFailure(t *testing.T) {
	svc := newTestService(t)
	svc.taskRepo.isAvailableErr = errors.New("sqlite unavailable")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "storage: sqlite unavailable")
}

func TestServiceDoctor_ReportsRepoDetectionFailure(t *testing.T) {
	svc := newTestService(t)
	svc.repoClient.detectRepoErr = errors.New("not a git repo")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "repo: not a git repo")
}

func TestServiceDoctor_NotesMissingRepoConfigWhenRepoDetected(t *testing.T) {
	svc := newTestService(t)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: agent.yaml not found")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_NotesLoadedEmptyRepoConfig(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{Exists: true}

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: loaded agent.yaml")
	require.NotContains(t, result.Notes, "config: agent.yaml not found")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_ReportsValidSeedPathsAsNotes(t *testing.T) {
	svc := newTestService(t)
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
	svc := newTestService(t)
	svc.configRepo.loadErr = errors.New("parse agent.yaml: invalid yaml")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "config: parse agent.yaml: invalid yaml")
}

func TestServiceDoctor_ReportsValidSetupScriptAsNote(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed: SeedConfig{
			SetupScript: "scripts/setup.sh",
		},
	}

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: setup script ok: scripts/setup.sh")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_ReportsInvalidSetupScriptAsFailure(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed: SeedConfig{
			SetupScript: "scripts/missing.sh",
		},
	}
	svc.setupRunner.validateErr = errors.New("setup script \"scripts/missing.sh\" not found")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "config: setup script \"scripts/missing.sh\" not found")
}

func TestServiceDoctor_ReportsInvalidSeedPathsAsFailure(t *testing.T) {
	svc := newTestService(t)
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

func TestServiceDoctor_NotesGHUnavailable(t *testing.T) {
	svc := newTestService(t)
	prChecker := NewMockPRStatusChecker(t)
	prChecker.EXPECT().IsAvailable(mock.Anything).Return(errors.New("gh not found")).Once()
	svc.service.SetPRStatusChecker(prChecker)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "gh: gh CLI not found, PR status checks will be unavailable")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_NoNoteWhenGHAvailable(t *testing.T) {
	svc := newTestService(t)
	prChecker := NewMockPRStatusChecker(t)
	prChecker.EXPECT().IsAvailable(mock.Anything).Return(nil).Once()
	svc.service.SetPRStatusChecker(prChecker)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	for _, note := range result.Notes {
		require.NotContains(t, note, "gh:")
	}
}

func TestServiceDoctor_NoNoteWhenPRCheckerNotSet(t *testing.T) {
	svc := newTestService(t)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	for _, note := range result.Notes {
		require.NotContains(t, note, "gh:")
	}
}
