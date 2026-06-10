package bitbucketserver

import (
	"context"
	"errors"
	"fmt"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// EditChange edits a pull request's base branch and reviewers.
//
// Data Center replaces the mutable fields wholesale under an
// optimistic-locking version, so the pull request is fetched first and its
// current title, description, and reviewers are carried into the update.
// A stale version ([bitbucketserver.ErrConflict]) triggers one refetch
// and retry.
func (r *Repository) EditChange(
	ctx context.Context,
	id forge.ChangeID,
	opts forge.EditChangeOptions,
) error {
	r.warnUnsupportedEditOptions(opts)

	if !editChangesAnything(opts) {
		return nil
	}

	num := mustPR(id).Number

	pr, _, err := r.client.PullRequestGet(
		ctx, r.repoID.projectKey, r.repoID.slug, num,
	)
	if err != nil {
		return fmt.Errorf("get pull request: %w", err)
	}

	_, _, err = r.client.PullRequestUpdate(
		ctx, r.repoID.projectKey, r.repoID.slug, num,
		buildUpdateRequest(pr, opts),
	)
	if errors.Is(err, bitbucketserver.ErrConflict) {
		r.log.Debug("Pull request version conflict; refetching and retrying", "pr", num)
		pr, _, err = r.client.PullRequestGet(
			ctx, r.repoID.projectKey, r.repoID.slug, num,
		)
		if err != nil {
			return fmt.Errorf("refetch pull request: %w", err)
		}
		// Rebuild from the refetched pull request
		// so the conflicting edit's fields are not overwritten.
		_, _, err = r.client.PullRequestUpdate(
			ctx, r.repoID.projectKey, r.repoID.slug, num,
			buildUpdateRequest(pr, opts),
		)
	}
	if err != nil {
		return fmt.Errorf("update pull request: %w", err)
	}

	r.log.Debug("Updated pull request", "pr", num)
	return nil
}

// editChangesAnything reports whether opts request a change this forge applies.
func editChangesAnything(opts forge.EditChangeOptions) bool {
	return opts.Base != "" || len(opts.AddReviewers) > 0
}

// buildUpdateRequest assembles the gateway update request from the current
// pull request and the requested edits. The update replaces the mutable
// fields wholesale, so the current title, description, and reviewers are
// always carried over; requested reviewers are appended, deduplicated by
// username, with the author dropped (Data Center rejects self-review).
func buildUpdateRequest(
	pr *bitbucketserver.PullRequest,
	opts forge.EditChangeOptions,
) bitbucketserver.PullRequestUpdateRequest {
	req := bitbucketserver.PullRequestUpdateRequest{
		Version:     pr.Version,
		Title:       pr.Title,
		Description: &pr.Description,
		Reviewers: newReviewerList(
			pr.Author.User.Name,
			reviewerNames(pr.Reviewers),
			opts.AddReviewers,
		),
	}

	if opts.Base != "" {
		req.ToRef = &bitbucketserver.UpdateRef{ID: "refs/heads/" + opts.Base}
	}

	return req
}

// warnUnsupportedEditOptions warns about edit options Data Center cannot apply.
func (r *Repository) warnUnsupportedEditOptions(opts forge.EditChangeOptions) {
	if len(opts.AddLabels) > 0 {
		r.log.Warn("Bitbucket Data Center does not support PR labels; ignoring --label flags")
	}
	if len(opts.AddAssignees) > 0 {
		r.log.Warn("Bitbucket Data Center does not support PR assignees; ignoring --assign flags")
	}
	if opts.Draft != nil {
		r.log.Warn("Bitbucket Data Center does not support toggling PR draft status after creation; ignoring --draft/--ready")
	}
}
