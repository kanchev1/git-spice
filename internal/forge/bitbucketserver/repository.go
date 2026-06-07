package bitbucketserver

import (
	"context"
	"fmt"
	"sync"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
	"go.abhg.dev/gs/internal/silog"
)

// Repository is a Bitbucket Data Center repository.
type Repository struct {
	client *bitbucketserver.Client
	repoID *RepositoryID
	log    *silog.Logger
	forge  *Forge

	// repoNumericID memoizes the numeric repository ID; see numericRepoID.
	repoNumericMu sync.Mutex
	repoNumericID int64
}

var (
	_ forge.Repository    = (*Repository)(nil)
	_ forge.WithChangeURL = (*Repository)(nil)
)

func newRepository(
	forge *Forge,
	repoID *RepositoryID,
	log *silog.Logger,
	client *bitbucketserver.Client,
) *Repository {
	return &Repository{
		client: client,
		repoID: repoID,
		forge:  forge,
		log:    log,
	}
}

// Forge returns the forge this repository belongs to.
func (r *Repository) Forge() forge.Forge { return r.forge }

// numericRepoID resolves and memoizes the numeric repository ID, which the
// default-reviewers endpoint requires but [RepositoryID] does not carry.
// Only a successful lookup is cached, so failures are retried.
func (r *Repository) numericRepoID(ctx context.Context) (int64, error) {
	r.repoNumericMu.Lock()
	defer r.repoNumericMu.Unlock()
	if r.repoNumericID != 0 {
		return r.repoNumericID, nil
	}
	repo, _, err := r.client.RepositoryGet(ctx, r.repoID.projectKey, r.repoID.slug)
	if err != nil {
		return 0, fmt.Errorf("get repository: %w", err)
	}
	r.repoNumericID = repo.ID
	return r.repoNumericID, nil
}

// ChangeURL returns the web URL for viewing the given pull request.
func (r *Repository) ChangeURL(id forge.ChangeID) string {
	return r.repoID.ChangeURL(id)
}

// NewChangeMetadata returns the metadata for a pull request.
func (r *Repository) NewChangeMetadata(
	_ context.Context,
	id forge.ChangeID,
) (forge.ChangeMetadata, error) {
	pr := mustPR(id)
	return &PRMetadata{PR: pr}, nil
}

// ListChangeTemplates lists PR templates; Data Center has none, so it returns nil.
func (r *Repository) ListChangeTemplates(
	_ context.Context,
) ([]*forge.ChangeTemplate, error) {
	return nil, nil
}
