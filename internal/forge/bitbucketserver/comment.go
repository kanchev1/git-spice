package bitbucketserver

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// PostChangeComment posts a top-level comment on a pull request.
func (r *Repository) PostChangeComment(
	ctx context.Context,
	id forge.ChangeID,
	body string,
) (forge.ChangeCommentID, error) {
	prID := mustPR(id).Number
	comment, _, err := r.client.CommentCreate(
		ctx, r.repoID.projectKey, r.repoID.slug, prID, body,
	)
	if err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}

	r.log.Debug("Posted comment", "pr", prID, "comment", comment.ID)
	return &PRComment{
		ID:      comment.ID,
		PRID:    prID,
		Version: comment.Version,
	}, nil
}

// UpdateChangeComment updates the body of an existing pull request comment.
//
// A stale optimistic-locking version ([bitbucketserver.ErrConflict]) triggers
// one refetch of the live version and a single retry. A comment that no longer
// exists yields [forge.ErrNotFound] so the caller can recreate it.
func (r *Repository) UpdateChangeComment(
	ctx context.Context,
	id forge.ChangeCommentID,
	body string,
) error {
	comment := mustPRComment(id)

	_, _, err := r.client.CommentUpdate(
		ctx, r.repoID.projectKey, r.repoID.slug,
		comment.PRID, comment.ID, body, comment.Version,
	)

	if errors.Is(err, bitbucketserver.ErrConflict) {
		r.log.Debug("Comment version conflict; refetching and retrying",
			"pr", comment.PRID, "comment", comment.ID)
		version, found, ferr := r.liveCommentVersion(ctx, comment.PRID, comment.ID)
		if ferr != nil {
			return ferr
		}
		if !found {
			return fmt.Errorf("comment %d not found: %w", comment.ID, forge.ErrNotFound)
		}
		_, _, err = r.client.CommentUpdate(
			ctx, r.repoID.projectKey, r.repoID.slug,
			comment.PRID, comment.ID, body, version,
		)
	}

	if err != nil {
		if errors.Is(err, bitbucketserver.ErrNotFound) {
			return fmt.Errorf("comment %d not found: %w", comment.ID, forge.ErrNotFound)
		}
		return fmt.Errorf("update comment: %w", err)
	}

	return nil
}

// DeleteChangeComment deletes a pull request comment.
//
// Like UpdateChangeComment, a stale version triggers one refetch and retry;
// an already-deleted comment is treated as success.
func (r *Repository) DeleteChangeComment(
	ctx context.Context,
	id forge.ChangeCommentID,
) error {
	comment := mustPRComment(id)

	_, err := r.client.CommentDelete(
		ctx, r.repoID.projectKey, r.repoID.slug,
		comment.PRID, comment.ID, comment.Version,
	)

	if errors.Is(err, bitbucketserver.ErrConflict) {
		r.log.Debug("Comment version conflict; refetching and retrying",
			"pr", comment.PRID, "comment", comment.ID)
		version, found, ferr := r.liveCommentVersion(ctx, comment.PRID, comment.ID)
		if ferr != nil {
			return ferr
		}
		if !found {
			// Already gone; nothing to delete.
			return nil
		}
		_, err = r.client.CommentDelete(
			ctx, r.repoID.projectKey, r.repoID.slug,
			comment.PRID, comment.ID, version,
		)
	}

	if err != nil {
		if errors.Is(err, bitbucketserver.ErrNotFound) {
			// Already deleted; treat as success.
			return nil
		}
		return fmt.Errorf("delete comment: %w", err)
	}

	return nil
}

// liveCommentVersion recovers a comment's current optimistic-locking version
// from the pull request activity feed. found is false if the comment is
// absent (deleted).
func (r *Repository) liveCommentVersion(
	ctx context.Context,
	prID, commentID int64,
) (version int, found bool, err error) {
	for activity, aerr := range r.client.ActivityList(
		ctx, r.repoID.projectKey, r.repoID.slug, prID,
	) {
		if aerr != nil {
			return 0, false, fmt.Errorf("list activities: %w", aerr)
		}
		if activity.Action != bitbucketserver.ActivityActionCommented ||
			activity.Comment == nil {
			continue
		}
		if activity.Comment.ID == commentID {
			return activity.Comment.Version, true, nil
		}
	}
	return 0, false, nil
}

// ListChangeComments lists top-level comments on a pull request, filtered by
// opts. Data Center has no comment-listing endpoint, so comments are read
// from the pull request activity feed.
func (r *Repository) ListChangeComments(
	ctx context.Context,
	id forge.ChangeID,
	opts *forge.ListChangeCommentsOptions,
) iter.Seq2[*forge.ListChangeCommentItem, error] {
	prID := mustPR(id).Number

	return func(yield func(*forge.ListChangeCommentItem, error) bool) {
		// CanUpdate filtering needs the current user's name; resolve it once.
		var (
			currentUser     string
			haveCurrentUser bool
		)
		if opts != nil && opts.CanUpdate {
			user, _, err := r.client.CurrentUser(ctx)
			if err != nil {
				yield(nil, fmt.Errorf("get current user: %w", err))
				return
			}
			currentUser = user.Name
			haveCurrentUser = true
		}

		for activity, err := range r.client.ActivityList(
			ctx, r.repoID.projectKey, r.repoID.slug, prID,
		) {
			if err != nil {
				yield(nil, fmt.Errorf("list activities: %w", err))
				return
			}
			if activity.Action != bitbucketserver.ActivityActionCommented ||
				activity.Comment == nil {
				continue
			}
			comment := activity.Comment

			if !matchesBodyFilter(comment.Text, opts) {
				continue
			}

			if haveCurrentUser && comment.Author.Name != currentUser {
				continue
			}

			item := &forge.ListChangeCommentItem{
				ID: &PRComment{
					ID:      comment.ID,
					PRID:    prID,
					Version: comment.Version,
				},
				Body: comment.Text,
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// matchesBodyFilter reports whether body matches all of opts.BodyMatchesAll.
func matchesBodyFilter(body string, opts *forge.ListChangeCommentsOptions) bool {
	if opts == nil {
		return true
	}
	for _, re := range opts.BodyMatchesAll {
		if !re.MatchString(body) {
			return false
		}
	}
	return true
}
