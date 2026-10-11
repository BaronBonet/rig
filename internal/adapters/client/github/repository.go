package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

// ghTimeout bounds each gh call, so a gh that hangs, as on a network that
// drops packets, is killed rather than left running while the task list's
// refreshes start more.
const ghTimeout = 20 * time.Second

// branchPullRequestsQuery lists the pull requests whose head branch has a
// name, in every state, with the commits each contains. It asks GitHub's
// GraphQL API for no more than Rig reads: gh pr list --json commits also asks
// for every commit's authors, which costs GitHub's rate limit about a point
// per pull request listed and, for a hundred, exceeds its node limit.
//
// GitHub matches the name in forks too, whose pull requests Rig leaves out,
// so a name forks use too, such as patch-1, needs room for a hundred. Of a
// pull request's commits, the first hundred are listed.
const branchPullRequestsQuery = `query($owner: String!, $repo: String!, $head: String!) {
  repository(owner: $owner, name: $repo) {
    pullRequests(headRefName: $head, first: 100) {
      nodes {
        number
        state
        isDraft
        headRefName
        headRefOid
        baseRefName
        isCrossRepository
        commits(first: 100) { nodes { commit { oid } } }
      }
    }
  }
}`

// defaultBranchQuery looks a repository's default branch up.
const defaultBranchQuery = `query($owner: String!, $repo: String!) {
  repository(owner: $owner, name: $repo) { defaultBranchRef { name } }
}`

type repository struct {
	runner subprocess.Runner
}

func New(runner subprocess.Runner) core.PullRequestClient {
	return &repository{runner: runner}
}

// gh runs gh in cwd, killing it once ghTimeout has passed.
func (r *repository) gh(ctx context.Context, cwd string, args ...string) (subprocess.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	return r.runner.Run(ctx, cwd, "gh", args...)
}

func (r *repository) HealthCheck(ctx context.Context) error {
	_, err := r.gh(ctx, "", "auth", "status")
	return err
}

func (r *repository) ListRepoPullRequests(ctx context.Context, repoRoot string) ([]core.RepoPullRequest, error) {
	result, err := r.gh(
		ctx,
		repoRoot,
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

// ListBranchPullRequests asks gh for the pull requests whose head branch has
// the name. gh matches the name in forks too, as a fork's pull request from
// its own main has the head main, so those are left out.
func (r *repository) ListBranchPullRequests(
	ctx context.Context,
	repoRoot string,
	branchName string,
) ([]core.PullRequest, error) {
	result, err := r.graphQL(ctx, repoRoot, branchPullRequestsQuery, "head="+branchName)
	if err != nil {
		return nil, err
	}

	return parseBranchPullRequests(result.Stdout)
}

// DefaultBranch asks GitHub for the default branch of the repository its
// pull requests are listed in.
func (r *repository) DefaultBranch(ctx context.Context, repoRoot string) (string, error) {
	result, err := r.graphQL(ctx, repoRoot, defaultBranchQuery)
	if err != nil {
		return "", err
	}

	var response struct {
		Data struct {
			Repository *struct {
				DefaultBranchRef *struct {
					Name string `json:"name"`
				} `json:"defaultBranchRef"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return "", fmt.Errorf("decode default branch: %w", err)
	}
	if response.Data.Repository == nil || response.Data.Repository.DefaultBranchRef == nil {
		return "", nil
	}
	return strings.TrimSpace(response.Data.Repository.DefaultBranchRef.Name), nil
}

// graphQL runs query against the repository of repoRoot's checkout, which
// gh fills in for the owner and repo variables as it finds it for any of its
// commands, with variables given as name=value strings.
func (r *repository) graphQL(
	ctx context.Context,
	repoRoot string,
	query string,
	variables ...string,
) (subprocess.Result, error) {
	args := []string{"api", "graphql", "-f", "query=" + query, "-F", "owner={owner}", "-F", "repo={repo}"}
	for _, variable := range variables {
		args = append(args, "-f", variable)
	}
	return r.gh(ctx, repoRoot, args...)
}

// ghPullRequest is one pull request as branchPullRequestsQuery returns it.
type ghPullRequest struct {
	State       string `json:"state"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	Commits     struct {
		Nodes []struct {
			Commit struct {
				Oid string `json:"oid"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Number            int  `json:"number"`
	IsDraft           bool `json:"isDraft"`
	IsCrossRepository bool `json:"isCrossRepository"`
}

func parseBranchPullRequests(output string) ([]core.PullRequest, error) {
	var response struct {
		Data struct {
			Repository *struct {
				PullRequests struct {
					Nodes []ghPullRequest `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return nil, fmt.Errorf("decode branch pull requests: %w", err)
	}
	if response.Data.Repository == nil {
		return nil, nil
	}

	listed := response.Data.Repository.PullRequests.Nodes
	pullRequests := make([]core.PullRequest, 0, len(listed))
	for _, pr := range listed {
		state := pullRequestState(pr.State, pr.IsDraft)
		if pr.IsCrossRepository || state == core.PRStateNone || pr.Number <= 0 {
			continue
		}
		var commits []string
		for _, node := range pr.Commits.Nodes {
			if node.Commit.Oid != "" {
				commits = append(commits, node.Commit.Oid)
			}
		}
		pullRequests = append(pullRequests, core.PullRequest{
			Number:     pr.Number,
			State:      state,
			HeadBranch: pr.HeadRefName,
			HeadCommit: pr.HeadRefOid,
			BaseBranch: pr.BaseRefName,
			Commits:    commits,
		})
	}
	return pullRequests, nil
}

// pullRequestState maps a GitHub pull request state, OPEN, MERGED or CLOSED,
// to Rig's, in which an open draft is a draft. Other states are none.
func pullRequestState(state string, isDraft bool) core.PRState {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "open":
		if isDraft {
			return core.PRStateDraft
		}
		return core.PRStateOpen
	case "merged":
		return core.PRStateMerged
	case "closed":
		return core.PRStateClosed
	default:
		return core.PRStateNone
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
