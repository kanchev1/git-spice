package bitbucketserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// CommentCountsByChange retrieves resolvable-comment counts for multiple
// pull requests, in the same order as ids.
func (r *Repository) CommentCountsByChange(
	ctx context.Context,
	ids []forge.ChangeID,
) ([]*forge.CommentCounts, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	results := make([]*forge.CommentCounts, len(ids))
	for i, id := range ids {
		counts, err := r.commentCounts(ctx, mustPR(id).Number)
		if err != nil {
			return nil, fmt.Errorf("get counts for %v: %w", id, err)
		}
		results[i] = counts
	}

	return results, nil
}

// commentCounts computes the resolvable-comment counts for a single pull
// request. Review-comment threads come from the activity feed; the
// blocker-comments (task) list is then walked to add tasks nested as replies,
// which the feed omits. The two sources are deduplicated by comment ID.
//
// A thread is resolved by its "Resolve" action (ThreadResolved) or, for a
// task, by State "RESOLVED". The navigation comment and unpublished drafts
// (State "PENDING") are excluded. The blocker-comments endpoint needs Data
// Center 7.2+; on older servers only the activity-feed result is used.
func (r *Repository) commentCounts(
	ctx context.Context,
	prID int64,
) (*forge.CommentCounts, error) {
	var total, resolved int
	seen := make(map[int64]struct{})
	for activity, err := range r.client.ActivityList(
		ctx, r.repoID.projectKey, r.repoID.slug, prID,
	) {
		if err != nil {
			return nil, fmt.Errorf("list activities: %w", err)
		}
		if activity.Action != bitbucketserver.ActivityActionCommented ||
			activity.Comment == nil {
			continue
		}
		c := activity.Comment

		if _, ok := seen[c.ID]; ok {
			continue
		}
		seen[c.ID] = struct{}{}

		if c.State == "PENDING" || strings.Contains(c.Text, _navigationCommentMarker) {
			continue
		}

		total++
		if c.ThreadResolved || (c.Severity == "BLOCKER" && c.State == "RESOLVED") {
			resolved++
		}
	}

	// Add tasks nested as replies, which the feed omits.
	for c, err := range r.client.BlockerCommentList(
		ctx, r.repoID.projectKey, r.repoID.slug, prID,
	) {
		if err != nil {
			if errors.Is(err, bitbucketserver.ErrNotFound) {
				// blocker-comments needs Data Center 7.2+; tolerate its absence.
				break
			}
			return nil, fmt.Errorf("list blocker comments: %w", err)
		}

		if _, ok := seen[c.ID]; ok {
			continue
		}
		seen[c.ID] = struct{}{}

		if c.State == "PENDING" {
			continue
		}

		total++
		if c.State == "RESOLVED" {
			resolved++
		}
	}

	return &forge.CommentCounts{
		Total:      total,
		Resolved:   resolved,
		Unresolved: total - resolved,
	}, nil
}
