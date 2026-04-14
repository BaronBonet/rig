package github

import (
	"context"
	"strconv"
	"strings"

	"rig/internal/core"
	"rig/internal/pkg/execx"
)

type PRStatusChecker struct {
	runner execx.Runner
}

func NewPRStatusChecker(runner execx.Runner) *PRStatusChecker {
	return &PRStatusChecker{runner: runner}
}

func (c *PRStatusChecker) IsAvailable(ctx context.Context) error {
	_, err := c.runner.Run(ctx, "", "gh", "--version")
	return err
}

func (c *PRStatusChecker) CheckPRStatus(
	ctx context.Context,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	result, err := c.runner.Run(
		ctx, repoRoot,
		"gh", "pr", "view",
		branchName,
		"--json", "number,state,isDraft",
		"--jq", ".number,.state,.isDraft",
	)
	if err != nil {
		return &core.PRStatus{State: core.PRStateNone}, nil
	}

	return parsePROutput(result.Stdout), nil
}

func (c *PRStatusChecker) ListRepoPullRequests(
	ctx context.Context,
	repoRoot string,
) ([]core.RepoPullRequest, error) {
	result, err := c.runner.Run(
		ctx,
		repoRoot,
		"gh",
		"pr",
		"list",
		"--state",
		"open",
		"--json",
		"number,title,headRefName,isDraft",
		"--jq",
		".[] | [.number, .title, .headRefName, .isDraft] | @tsv",
	)
	if err != nil {
		return nil, err
	}

	return parsePRListOutput(result.Stdout), nil
}

func parsePROutput(output string) *core.PRStatus {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return &core.PRStatus{State: core.PRStateNone}
	}

	number, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	state := strings.TrimSpace(strings.ToLower(lines[1]))

	isDraft := false
	if len(lines) >= 3 {
		isDraft = strings.TrimSpace(strings.ToLower(lines[2])) == "true"
	}

	switch state {
	case "open":
		if isDraft {
			return &core.PRStatus{State: core.PRStateDraft, Number: number}
		}
		return &core.PRStatus{State: core.PRStateOpen, Number: number}
	case "merged":
		return &core.PRStatus{State: core.PRStateMerged, Number: number}
	case "closed":
		return &core.PRStatus{State: core.PRStateClosed, Number: number}
	default:
		return &core.PRStatus{State: core.PRStateNone}
	}
}

func parsePRListOutput(output string) []core.RepoPullRequest {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	prs := make([]core.RepoPullRequest, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}

		number, _ := strconv.Atoi(strings.TrimSpace(fields[0]))
		state := core.PRStateOpen
		if strings.TrimSpace(strings.ToLower(fields[3])) == "true" {
			state = core.PRStateDraft
		}

		prs = append(prs, core.RepoPullRequest{
			Number:     number,
			Title:      strings.TrimSpace(fields[1]),
			BranchName: strings.TrimSpace(fields[2]),
			State:      state,
		})
	}

	return prs
}
