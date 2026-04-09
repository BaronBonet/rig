package github

import (
	"context"
	"strconv"
	"strings"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type PRStatusChecker struct {
	runner execx.Runner
}

func NewPRStatusChecker(runner execx.Runner) *PRStatusChecker {
	return &PRStatusChecker{runner: runner}
}

func (c *PRStatusChecker) CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error) {
	result, err := c.runner.Run(
		ctx, repoRoot,
		"gh", "pr", "view",
		"--head", branchName,
		"--json", "number,state",
		"--jq", ".number,.state",
	)
	if err != nil {
		return &core.PRStatus{State: core.PRStateNone}, nil
	}

	return parsePROutput(result.Stdout), nil
}

func parsePROutput(output string) *core.PRStatus {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return &core.PRStatus{State: core.PRStateNone}
	}

	number, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	state := strings.TrimSpace(strings.ToLower(lines[1]))

	switch state {
	case "open":
		return &core.PRStatus{State: core.PRStateOpen, Number: number}
	case "merged":
		return &core.PRStatus{State: core.PRStateMerged, Number: number}
	default:
		return &core.PRStatus{State: core.PRStateNone}
	}
}
