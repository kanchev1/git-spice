package bitbucketserver

import (
	"context"
	"fmt"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
	"go.abhg.dev/gs/internal/git"
)

// FindChangesByBranch finds pull requests opened from the given source
// branch, returning up to opts.Limit results (or a default).
//
// Cross-repository (fork) pull requests are unsupported, so
// opts.PushRepository is ignored.
func (r *Repository) FindChangesByBranch(
	ctx context.Context,
	branch string,
	opts forge.FindChangesOptions,
) ([]*forge.FindChangeItem, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	req := bitbucketserver.PullRequestListRequest{
		At:        "refs/heads/" + branch,
		Direction: "OUTGOING",
		State:     stateToAPI(opts.State),
	}

	var items []*forge.FindChangeItem
	for pr, err := range r.client.PullRequestList(
		ctx, r.repoID.projectKey, r.repoID.slug, req,
	) {
		if err != nil {
			return nil, fmt.Errorf("list pull requests: %w", err)
		}

		items = append(items, r.prToFindChangeItem(&pr))
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

// FindChangeByID finds a pull request by its ID.
func (r *Repository) FindChangeByID(
	ctx context.Context,
	id forge.ChangeID,
) (*forge.FindChangeItem, error) {
	num := mustPR(id).Number
	pr, _, err := r.client.PullRequestGet(
		ctx, r.repoID.projectKey, r.repoID.slug, num,
	)
	if err != nil {
		return nil, fmt.Errorf("get pull request: %w", err)
	}
	return r.prToFindChangeItem(pr), nil
}

// prToFindChangeItem maps a gateway pull request to a [forge.FindChangeItem].
func (r *Repository) prToFindChangeItem(
	pr *bitbucketserver.PullRequest,
) *forge.FindChangeItem {
	id := &PR{Number: pr.ID}
	return &forge.FindChangeItem{
		ID:        id,
		URL:       changeURL(pr, r.repoID, id),
		State:     stateFromAPI(pr.State),
		Subject:   pr.Title,
		BaseName:  pr.ToRef.DisplayID,
		HeadHash:  git.Hash(pr.FromRef.LatestCommit),
		Draft:     pr.Draft,
		Reviewers: reviewerNames(pr.Reviewers),
	}
}

// reviewerNames extracts the usernames of the given reviewers.
func reviewerNames(reviewers []bitbucketserver.Reviewer) []string {
	var names []string
	for _, rev := range reviewers {
		if rev.User.Name != "" {
			names = append(names, rev.User.Name)
		}
	}
	return names
}
