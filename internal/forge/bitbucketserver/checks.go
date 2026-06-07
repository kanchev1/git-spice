package bitbucketserver

import (
	"context"
	"fmt"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// ChangeChecksState reports the aggregate CI build status
// for the given pull request.
func (r *Repository) ChangeChecksState(
	ctx context.Context,
	id forge.ChangeID,
) (forge.ChecksState, error) {
	prID := mustPR(id).Number

	pr, _, err := r.client.PullRequestGet(
		ctx, r.repoID.projectKey, r.repoID.slug, prID,
	)
	if err != nil {
		return 0, fmt.Errorf("get pull request: %w", err)
	}

	sha := pr.FromRef.LatestCommit
	if sha == "" {
		return forge.ChecksPassed, nil
	}

	statuses, err := r.client.BuildStatusList(ctx, sha)
	if err != nil {
		return 0, fmt.Errorf("list build statuses: %w", err)
	}

	return aggregateBuildStatuses(statuses), nil
}

// aggregateBuildStatuses reduces a commit's build statuses to a single
// [forge.ChecksState]. Any non-SUCCESSFUL, non-INPROGRESS state
// (including unrecognized ones) counts as failing.
func aggregateBuildStatuses(
	statuses []bitbucketserver.BuildStatus,
) forge.ChecksState {
	if len(statuses) == 0 {
		return forge.ChecksPassed // no checks configured
	}

	pending := false
	for _, s := range statuses {
		switch s.State {
		case bitbucketserver.BuildStatusSuccessful:
		case bitbucketserver.BuildStatusInProgress:
			pending = true
		default:
			return forge.ChecksFailed
		}
	}

	if pending {
		return forge.ChecksPending
	}
	return forge.ChecksPassed
}
