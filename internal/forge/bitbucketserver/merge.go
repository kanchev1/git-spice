package bitbucketserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// errMergeBlocked is returned when a pre-merge check blocks the merge.
var errMergeBlocked = errors.New("pull request cannot be merged")

// MergeChange merges an open pull request into its base branch.
//
// opts.Method selects a Data Center merge strategy; an unset or unknown method
// lets the server use the repository default. The merge is guarded by an
// optimistic-locking version, so a conflict triggers one refetch and retry.
// opts.HeadHash is ignored (Data Center has no expected-head assertion).
func (r *Repository) MergeChange(
	ctx context.Context,
	id forge.ChangeID,
	opts forge.MergeChangeOptions,
) error {
	num := mustPR(id).Number

	// Best-effort pre-merge probe: block early on a clear "cannot merge".
	// Any probe error falls through to the merge attempt.
	if status, _, err := r.client.PullRequestCanMerge(ctx, r.repoID.projectKey, r.repoID.slug, num); err != nil {
		r.log.Debug("Pre-merge check failed; proceeding with merge attempt", "pr", num, "error", err)
	} else if status != nil && !status.CanMerge {
		return fmt.Errorf("%w: %s", errMergeBlocked, formatVetoes(status))
	}

	version, err := r.currentPRVersion(ctx, num)
	if err != nil {
		return err
	}

	req := bitbucketserver.PullRequestMergeRequest{
		StrategyID: r.mergeStrategyID(opts.Method),
	}
	_, _, err = r.client.PullRequestMerge(
		ctx, r.repoID.projectKey, r.repoID.slug, num, version, req,
	)

	if errors.Is(err, bitbucketserver.ErrConflict) {
		r.log.Debug("Pull request version conflict; refetching and retrying", "pr", num)
		version, err = r.currentPRVersion(ctx, num)
		if err != nil {
			return err
		}
		_, _, err = r.client.PullRequestMerge(
			ctx, r.repoID.projectKey, r.repoID.slug, num, version, req,
		)
	}

	if err != nil {
		return fmt.Errorf("merge pull request: %w", err)
	}

	r.log.Debug("Merged pull request", "pr", num)
	return nil
}

// currentPRVersion fetches a pull request's current optimistic-locking version.
func (r *Repository) currentPRVersion(ctx context.Context, num int64) (int, error) {
	pr, _, err := r.client.PullRequestGet(
		ctx, r.repoID.projectKey, r.repoID.slug, num,
	)
	if err != nil {
		return 0, fmt.Errorf("get pull request: %w", err)
	}
	return pr.Version, nil
}

// formatVetoes renders a human-readable reason a pull request cannot be merged,
// joining each veto's message with "; ". With no usable vetoes it reports a
// merge conflict (when indicated) or a generic reason. Veto text is
// server-localized, so it is only displayed, never parsed.
func formatVetoes(s *bitbucketserver.MergeStatus) string {
	msgs := make([]string, 0, len(s.Vetoes))
	for _, v := range s.Vetoes {
		switch {
		case v.SummaryMessage != "":
			msgs = append(msgs, v.SummaryMessage)
		case v.DetailedMessage != "":
			msgs = append(msgs, v.DetailedMessage)
		}
	}
	if len(msgs) == 0 {
		if s.Conflicted || s.Outcome == "CONFLICTED" {
			return "the pull request has merge conflicts"
		}
		return "the server reported it is not mergeable"
	}
	return strings.Join(msgs, "; ")
}

// mergeStrategyID maps a forge merge method to a Data Center merge strategy ID.
// An unset or unrecognized method returns "", so the server uses its default.
func (r *Repository) mergeStrategyID(method forge.MergeMethod) string {
	switch method {
	case forge.MergeMethodMerge:
		return "no-ff"
	case forge.MergeMethodSquash:
		return "squash"
	case forge.MergeMethodRebase:
		return "rebase-no-ff"
	case forge.MergeMethodDefault:
		return ""
	default:
		r.log.Warn(
			"Unsupported merge method; using repository default",
			"method", method,
		)
		return ""
	}
}
