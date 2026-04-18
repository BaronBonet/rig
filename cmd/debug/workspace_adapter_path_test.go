package main

import (
	"testing"

	repositoryworkspace "rig/internal/adapters/repository/workspace"
)

func TestRepositoryWorkspacePackage_ExposesPreparer(t *testing.T) {
	if repositoryworkspace.NewPreparer() == nil {
		t.Fatal("expected repository workspace preparer")
	}
}
