package github

import (
	"context"
	"strconv"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

type repository struct {
	runner subprocess.Runner
}

func New(runner subprocess.Runner) core.PullRequestClient {
	return &repository{runner: runner}
}

func (r *repository) HealthCheck(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "gh", "auth", "status")
	return err
}

func (r *repository) ListRepoPullRequests(ctx context.Context, repoRoot string) ([]core.RepoPullRequest, error) {
	result, err := r.runner.Run(
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

func (r *repository) CheckPullRequestStatus(
	ctx context.Context,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	result, runErr := r.runner.Run(
		ctx,
		repoRoot,
		"gh",
		"pr",
		"view",
		branchName,
		"--json",
		"number,state,isDraft",
		"--jq",
		".number,.state,.isDraft",
	)
	if runErr == nil {
		return parsePRStatusOutput(result.Stdout), nil
	}

	return &core.PRStatus{State: core.PRStateNone}, nil
}

func parsePRStatusOutput(output string) *core.PRStatus {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return &core.PRStatus{State: core.PRStateNone}
	}

	number, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	state := strings.ToLower(strings.TrimSpace(lines[1]))
	isDraft := len(lines) >= 3 && strings.EqualFold(strings.TrimSpace(lines[2]), "true")

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
		if strings.EqualFold(strings.TrimSpace(fields[3]), "true") {
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
