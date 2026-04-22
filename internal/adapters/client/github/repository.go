package github

import (
	"context"
	"strconv"
	"strings"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"
)

type repository struct {
	runner subprocess.Runner
}

func New(runner subprocess.Runner) core.PullRequestClient {
	return &repository{runner: runner}
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
