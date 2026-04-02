package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceDoctor_ReturnsMissingBinaryFailures(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.isAvailableErr = errors.New("missing codex")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "codex: missing codex")
}

func TestServiceDoctor_ReportsRepoDetectionFailure(t *testing.T) {
	svc := newTestService()
	svc.gitRepo.detectRepoErr = errors.New("not a git repo")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "repo: not a git repo")
}
