package bitbucketserver

import (
	"context"
	"errors"
	"fmt"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// SubmitChange creates a new pull request in the repository.
func (r *Repository) SubmitChange(
	ctx context.Context,
	req forge.SubmitChangeRequest,
) (forge.SubmitChangeResult, error) {
	r.warnUnsupportedFeatures(req)

	if err := r.ensureSameRepository(req.PushRepository); err != nil {
		return forge.SubmitChangeResult{}, err
	}

	reviewers, err := r.buildReviewers(ctx, req.Reviewers, req.Head, req.Base)
	if err != nil {
		return forge.SubmitChangeResult{}, err
	}

	apiReq := r.buildCreatePRRequest(req, reviewers)

	// Draft PRs require Data Center 8.18+. If the version is readable and too
	// old, downgrade; if unreadable, try draft:true and verify afterward.
	verifyDraft := false
	if req.Draft {
		switch supported, known, version := r.draftSupport(ctx); {
		case known && !supported:
			r.log.Warn(
				"Bitbucket Data Center < 8.18 does not support draft pull requests; "+
					"creating a regular pull request",
				"serverVersion", version,
			)
			apiReq.Draft = false
		case !known:
			verifyDraft = true
		}
	}

	pr, _, err := r.client.PullRequestCreate(
		ctx, r.repoID.projectKey, r.repoID.slug, apiReq,
	)
	if err != nil {
		if errors.Is(err, bitbucketserver.ErrDestinationBranchNotFound) {
			return forge.SubmitChangeResult{}, fmt.Errorf(
				"create pull request: %w", forge.ErrUnsubmittedBase,
			)
		}
		return forge.SubmitChangeResult{}, fmt.Errorf("create pull request: %w", err)
	}

	if verifyDraft && !pr.Draft {
		r.log.Warn(
			"Server created a non-draft pull request; " +
				"the draft flag may be unsupported on this Bitbucket Data Center version",
		)
	}

	id := &PR{Number: pr.ID}
	url := changeURL(pr, r.repoID, id)

	r.log.Debug("Created pull request", "pr", pr.ID, "url", url)
	return forge.SubmitChangeResult{
		ID:  id,
		URL: url,
	}, nil
}

// warnUnsupportedFeatures warns about request fields Data Center cannot apply.
func (r *Repository) warnUnsupportedFeatures(req forge.SubmitChangeRequest) {
	if len(req.Labels) > 0 {
		r.log.Warn("Bitbucket Data Center does not support PR labels; ignoring --label flags")
	}
	if len(req.Assignees) > 0 {
		r.log.Warn("Bitbucket Data Center does not support PR assignees; ignoring --assign flags")
	}
}

// ensureSameRepository errors if the head branch lives in another repository;
// cross-repository (fork) pull requests are not yet supported.
func (r *Repository) ensureSameRepository(pushRepo forge.RepositoryID) error {
	if pushRepo == nil {
		return nil
	}
	if pushRepo.String() == r.repoID.String() {
		return nil
	}
	return fmt.Errorf(
		"cross-repository pull requests are not yet supported "+
			"by the Bitbucket Data Center forge: head branch is in %q, not %q",
		pushRepo.String(), r.repoID.String(),
	)
}

// buildReviewers resolves the pull request's reviewers — the project's default
// reviewers followed by the requested usernames — into the gateway shape,
// deduplicated by username with the authenticated user dropped (Data Center
// rejects self-review).
//
// A current-user lookup failure is fatal when explicit reviewers were
// requested, but only drops the best-effort default reviewers otherwise.
func (r *Repository) buildReviewers(
	ctx context.Context,
	usernames []string,
	head string,
	base string,
) ([]bitbucketserver.CreateReviewer, error) {
	// Default reviewers are best-effort; they may be empty.
	defaults := r.defaultReviewers(ctx, head, base)
	if len(defaults) == 0 && len(usernames) == 0 {
		return nil, nil
	}

	// Drop self; a lookup failure is fatal only when explicit reviewers exist.
	var self string
	if user, _, err := r.client.CurrentUser(ctx); err != nil {
		if len(usernames) > 0 {
			return nil, fmt.Errorf("identify current user: %w", err)
		}
		r.log.Debug("Could not identify current user; skipping default reviewers", "error", err)
		return nil, nil
	} else if user != nil {
		self = user.Name
	}

	// Defaults first, then explicit usernames; dedup keeps first-seen order.
	return newReviewerList(self, defaults, usernames), nil
}

// newReviewerList builds the gateway reviewer list from the name lists in
// order, dropping empty names, duplicates, and exclude (Data Center rejects
// self-review).
func newReviewerList(
	exclude string,
	nameLists ...[]string,
) []bitbucketserver.CreateReviewer {
	var reviewers []bitbucketserver.CreateReviewer
	seen := make(map[string]struct{})
	for _, names := range nameLists {
		for _, name := range names {
			if name == "" || name == exclude {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			reviewers = append(reviewers, bitbucketserver.CreateReviewer{
				User: bitbucketserver.CreateReviewerUser{Name: name},
			})
		}
	}
	return reviewers
}

// defaultReviewers returns the usernames of the project's default (required)
// reviewers for a head->base pull request.
//
// It is best-effort: any failure is logged at debug level and yields nil,
// never failing the submit. No configured default reviewers is the common
// case, not an error.
func (r *Repository) defaultReviewers(ctx context.Context, head, base string) []string {
	repoID, err := r.numericRepoID(ctx)
	if err != nil {
		r.log.Debug("Could not resolve repository ID; skipping default reviewers", "error", err)
		return nil
	}

	// Use the same fully qualified refs as creation (see createRef).
	reviewers, _, err := r.client.DefaultReviewers(
		ctx, r.repoID.projectKey, r.repoID.slug,
		repoID, repoID,
		"refs/heads/"+head, "refs/heads/"+base,
	)
	if err != nil {
		r.log.Debug("Could not fetch default reviewers; proceeding with explicit reviewers only", "error", err)
		return nil
	}

	names := make([]string, 0, len(reviewers))
	for _, reviewer := range reviewers {
		if reviewer.Name != "" {
			names = append(names, reviewer.Name)
		}
	}
	return names
}

// buildCreatePRRequest assembles the gateway create request from req.
func (r *Repository) buildCreatePRRequest(
	req forge.SubmitChangeRequest,
	reviewers []bitbucketserver.CreateReviewer,
) bitbucketserver.PullRequestCreateRequest {
	return bitbucketserver.PullRequestCreateRequest{
		Title:       req.Subject,
		Description: req.Body,
		FromRef:     r.createRef(req.Head),
		ToRef:       r.createRef(req.Base),
		Reviewers:   reviewers,
		Draft:       req.Draft,
	}
}

// createRef builds a pull request ref for branch in this repository, with the
// fully qualified "refs/heads/{branch}" ref ID.
func (r *Repository) createRef(branch string) bitbucketserver.CreateRef {
	return bitbucketserver.CreateRef{
		ID: "refs/heads/" + branch,
		Repository: bitbucketserver.CreateRefRepository{
			Slug: r.repoID.slug,
			Project: bitbucketserver.CreateRefProject{
				Key: r.repoID.projectKey,
			},
		},
	}
}

// changeURL returns the web URL for a created pull request, preferring the
// server's self link and falling back to one built from the repository ID.
func changeURL(pr *bitbucketserver.PullRequest, rid *RepositoryID, id *PR) string {
	if len(pr.Links.Self) > 0 && pr.Links.Self[0].Href != "" {
		return pr.Links.Self[0].Href
	}
	return rid.ChangeURL(id)
}
