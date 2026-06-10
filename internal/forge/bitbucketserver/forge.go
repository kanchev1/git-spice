package bitbucketserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
	"go.abhg.dev/gs/internal/silog"
)

// Forge builds a Bitbucket Data Center/Server Forge.
type Forge struct {
	Options Options

	// Log specifies the logger to use.
	Log *silog.Logger
}

var (
	_ forge.Forge             = (*Forge)(nil)
	_ forge.WithCommentFormat = (*Forge)(nil)
	_ forge.WithDisplayName   = (*Forge)(nil)
)

func (f *Forge) logger() *silog.Logger {
	if f.Log == nil {
		return silog.Nop()
	}
	return f.Log.WithPrefix("bitbucket-server")
}

// ID reports a unique key for this forge.
func (*Forge) ID() string { return "bitbucket-server" }

// DisplayName reports a human-friendly name for the forge used in the UI.
func (*Forge) DisplayName() string { return "Bitbucket Data Center" }

// URL returns the base URL configured for the Bitbucket Data Center Forge,
// or an empty string if none is set (there is no default).
func (f *Forge) URL() string {
	return f.Options.URL
}

// BaseURL reports the Bitbucket Data Center web URL used for host matching and links.
func (f *Forge) BaseURL() string {
	return f.URL()
}

// APIURL returns the REST API URL, derived from URL ("/rest/api/1.0") if unset.
func (f *Forge) APIURL() string {
	return cmp.Or(f.Options.APIURL, f.URL()+"/rest/api/1.0")
}

// _navigationCommentMarker is the invisible marker identifying git-spice's
// navigation comment, so it can be matched on update and skipped when counting.
const _navigationCommentMarker = "[gs]: # (navigation comment)"

// CommentFormat returns Bitbucket-specific comment formatting.
// Bitbucket doesn't support HTML in comments, so we use plain Markdown.
func (*Forge) CommentFormat() forge.CommentFormat {
	return forge.CommentFormat{
		// Use italic text instead of HTML <sub> tag.
		Footer: "*Change managed by [git-spice](https://abhinav.github.io/git-spice/).*",
		// Use Markdown link definition syntax instead of HTML comment.
		// This renders as invisible on Bitbucket.
		Marker: _navigationCommentMarker,
	}
}

// CLIPlugin returns the CLI plugin for the Bitbucket Data Center Forge.
func (f *Forge) CLIPlugin() any { return &f.Options }

// ChangeTemplatePaths reports the paths at which change templates
// can be found in a Bitbucket Data Center repository.
func (*Forge) ChangeTemplatePaths() []string {
	// Bitbucket Data Center has no native PR template support;
	// match the community conventions the Bitbucket Cloud forge uses.
	return []string{
		"PULL_REQUEST_TEMPLATE.md",
		"pull_request_template.md",
		".bitbucket/PULL_REQUEST_TEMPLATE.md",
		".bitbucket/pull_request_template.md",
	}
}

// ParseRepositoryPath parses a Bitbucket Data Center repository path and
// returns a [RepositoryID] for the repository it identifies. It handles SSH
// ("/{project}/{slug}"), HTTPS ("/scm/{project}/{slug}"), and personal
// ("/~{user}/{slug}") forms, with or without a ".git" suffix.
//
// It returns [forge.ErrUnsupportedURL] if the path is not valid.
func (f *Forge) ParseRepositoryPath(path string) (forge.RepositoryID, error) {
	projectKey, slug, personal, err := parseRepoPath(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", forge.ErrUnsupportedURL, err)
	}

	return &RepositoryID{
		url:        f.URL(),
		projectKey: projectKey,
		slug:       slug,
		personal:   personal,
	}, nil
}

// parseRepoPath splits a Bitbucket Data Center repository path into its
// project key (or personal user) and slug. For "~user" paths, personal is
// true and projectKey holds the username.
func parseRepoPath(path string) (projectKey, slug string, personal bool, err error) {
	s := strings.Trim(path, "/")
	s = strings.TrimSuffix(s, ".git")

	// HTTPS clone URLs are served under "/scm/"; strip it if present.
	s = strings.TrimPrefix(s, "scm/")

	owner, slug, ok := strings.Cut(s, "/")
	if !ok || owner == "" || slug == "" {
		return "", "", false, fmt.Errorf(
			"path %q does not contain a Bitbucket Data Center repository", path,
		)
	}

	// A slug must not contain further path segments.
	if strings.Contains(slug, "/") {
		return "", "", false, fmt.Errorf(
			"path %q does not contain a Bitbucket Data Center repository", path,
		)
	}

	// Personal repositories are addressed as "~user".
	if rest, ok := strings.CutPrefix(owner, "~"); ok {
		if rest == "" {
			return "", "", false, fmt.Errorf(
				"path %q does not contain a Bitbucket Data Center repository", path,
			)
		}
		return rest, slug, true, nil
	}

	return owner, slug, false, nil
}

// OpenRepository opens the Bitbucket Data Center repository the given ID points to.
func (f *Forge) OpenRepository(
	_ context.Context,
	token forge.AuthenticationToken,
	id forge.RepositoryID,
) (forge.Repository, error) {
	if f.URL() == "" {
		return nil, errors.New(
			"no Bitbucket Data Center URL configured: " +
				"set spice.forge.bitbucket-server.url or BITBUCKET_SERVER_URL",
		)
	}

	rid := mustRepositoryID(id)
	tok := token.(*AuthenticationToken)

	tokenSource, err := newGatewayTokenSource(tok)
	if err != nil {
		return nil, fmt.Errorf("build Bitbucket Data Center token source: %w", err)
	}

	client, err := bitbucketserver.NewClient(tokenSource, &bitbucketserver.ClientOptions{
		BaseURL: f.APIURL(),
	})
	if err != nil {
		return nil, fmt.Errorf("create Bitbucket Data Center client: %w", err)
	}

	return newRepository(f, rid, f.logger(), client), nil
}
