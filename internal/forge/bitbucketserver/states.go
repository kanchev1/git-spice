package bitbucketserver

import (
	"context"
	"fmt"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/git"
)

// Bitbucket Data Center pull request states.
const (
	statePROpen     = "OPEN"
	statePRMerged   = "MERGED"
	statePRDeclined = "DECLINED"
)

// stateFromAPI maps a Bitbucket Data Center pull request state to a
// [forge.ChangeState]; unknown values fall back to [forge.ChangeOpen].
func stateFromAPI(state string) forge.ChangeState {
	switch state {
	case statePRMerged:
		return forge.ChangeMerged
	case statePRDeclined:
		return forge.ChangeClosed
	case statePROpen:
		return forge.ChangeOpen
	default:
		return forge.ChangeOpen
	}
}

// stateToAPI maps a forge change-state filter to a Bitbucket Data Center
// pull request "state" query value; the zero state means all states.
func stateToAPI(state forge.ChangeState) string {
	switch state {
	case forge.ChangeMerged:
		return statePRMerged
	case forge.ChangeClosed:
		return statePRDeclined
	case forge.ChangeOpen:
		return statePROpen
	default:
		return "ALL"
	}
}

// ChangeStatuses retrieves compact statuses for multiple pull requests.
func (r *Repository) ChangeStatuses(
	ctx context.Context,
	ids []forge.ChangeID,
) ([]forge.ChangeStatus, error) {
	statuses := make([]forge.ChangeStatus, len(ids))
	for i, id := range ids {
		num := mustPR(id).Number
		pr, _, err := r.client.PullRequestGet(
			ctx, r.repoID.projectKey, r.repoID.slug, num,
		)
		if err != nil {
			return nil, fmt.Errorf("get status for PR #%d: %w", num, err)
		}
		statuses[i] = forge.ChangeStatus{
			State:    stateFromAPI(pr.State),
			HeadHash: git.Hash(pr.FromRef.LatestCommit),
		}
	}
	return statuses, nil
}
