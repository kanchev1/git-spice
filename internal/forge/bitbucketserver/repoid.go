package bitbucketserver

import (
	"fmt"

	"go.abhg.dev/gs/internal/forge"
)

// RepositoryID is a unique identifier for a Bitbucket Data Center repository.
type RepositoryID struct {
	url        string // required
	projectKey string // required
	slug       string // required

	// personal reports whether this is a personal ("~user") repository;
	// when true, projectKey holds the username.
	personal bool
}

var _ forge.RepositoryID = (*RepositoryID)(nil)

func mustRepositoryID(id forge.RepositoryID) *RepositoryID {
	rid, ok := id.(*RepositoryID)
	if ok {
		return rid
	}
	panic(fmt.Sprintf("bitbucket-server: expected *RepositoryID, got %T", id))
}

// String returns a human-readable name for the repository ID.
func (rid *RepositoryID) String() string {
	if rid.personal {
		return fmt.Sprintf("~%s/%s", rid.projectKey, rid.slug)
	}
	return fmt.Sprintf("%s/%s", rid.projectKey, rid.slug)
}

// ChangeURL returns the web URL for a Pull Request hosted on Bitbucket Data Center.
func (rid *RepositoryID) ChangeURL(id forge.ChangeID) string {
	prNum := mustPR(id).Number
	return fmt.Sprintf(
		"%s/pull-requests/%d/overview",
		rid.webBase(), prNum,
	)
}

// webBase returns the web URL prefix for the repository,
// up to and including the repository slug.
func (rid *RepositoryID) webBase() string {
	if rid.personal {
		return fmt.Sprintf("%s/users/%s/repos/%s", rid.url, rid.projectKey, rid.slug)
	}
	return fmt.Sprintf("%s/projects/%s/repos/%s", rid.url, rid.projectKey, rid.slug)
}
