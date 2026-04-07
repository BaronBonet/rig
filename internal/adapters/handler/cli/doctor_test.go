package cli

import (
	"bytes"
	"strings"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDoctorCommand_PrintsFailures(t *testing.T) {
	out := &bytes.Buffer{}
	service := NewMockTaskService(t)
	service.EXPECT().
		Doctor(mock.Anything, "/tmp/repo").
		Return(core.DoctorResult{
			Notes:    []string{"config: loaded agent.yaml"},
			Failures: []string{"codex: missing codex"},
		}, nil).
		Once()

	cmd := newDoctorCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  out,
		Cwd:     "/tmp/repo",
	})
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "config: loaded agent.yaml")
	require.Contains(t, out.String(), "codex: missing codex")
}

func TestDoctorCommand_PrintsNotesBeforeOk(t *testing.T) {
	out := &bytes.Buffer{}
	service := NewMockTaskService(t)
	service.EXPECT().
		Doctor(mock.Anything, "/tmp/repo").
		Return(core.DoctorResult{
			Notes: []string{
				"config: loaded agent.yaml",
				"config: seed path ok: .env",
			},
		}, nil).
		Once()

	cmd := newDoctorCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  out,
		Cwd:     "/tmp/repo",
	})
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Less(t, strings.Index(out.String(), "config: loaded agent.yaml"), strings.Index(out.String(), "doctor: ok"))
	require.Less(
		t,
		strings.Index(out.String(), "config: seed path ok: .env"),
		strings.Index(out.String(), "doctor: ok"),
	)
	require.Contains(t, out.String(), "doctor: ok")
}
