package bitbucketserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

func TestBuildUpdateRequest(t *testing.T) {
	reviewer := func(name string) bitbucketserver.Reviewer {
		return bitbucketserver.Reviewer{User: bitbucketserver.User{Name: name}}
	}
	mkPR := func(author string, existing ...string) *bitbucketserver.PullRequest {
		pr := &bitbucketserver.PullRequest{
			Version:     7,
			Title:       "Refit",
			Description: "the warp core needs a refit",
		}
		pr.Author.User.Name = author
		for _, name := range existing {
			pr.Reviewers = append(pr.Reviewers, reviewer(name))
		}
		return pr
	}

	tests := []struct {
		name          string
		pr            *bitbucketserver.PullRequest
		opts          forge.EditChangeOptions
		wantReviewers []string
		wantToRef     string
	}{
		{
			// The Data Center update replaces fields wholesale, so a
			// base-only retarget must carry the existing reviewers.
			name:          "BaseOnlyPreservesReviewers",
			pr:            mkPR("kirk", "uhura", "spock"),
			opts:          forge.EditChangeOptions{Base: "main"},
			wantReviewers: []string{"uhura", "spock"},
			wantToRef:     "refs/heads/main",
		},
		{
			name:          "ExistingPreservedAndAdded",
			pr:            mkPR("kirk", "uhura"),
			opts:          forge.EditChangeOptions{AddReviewers: []string{"spock"}},
			wantReviewers: []string{"uhura", "spock"},
		},
		{
			name: "DedupExistingAndAdded",
			pr:   mkPR("kirk", "spock"),
			opts: forge.EditChangeOptions{
				AddReviewers: []string{"spock", "uhura", "uhura"},
			},
			wantReviewers: []string{"spock", "uhura"},
		},
		{
			name: "ExcludesAuthor",
			pr:   mkPR("kirk", "uhura"),
			opts: forge.EditChangeOptions{
				AddReviewers: []string{"kirk", "spock"},
			},
			wantReviewers: []string{"uhura", "spock"},
		},
		{
			name: "SkipsEmptyNames",
			pr:   mkPR("kirk", ""),
			opts: forge.EditChangeOptions{
				AddReviewers: []string{"", "spock"},
			},
			wantReviewers: []string{"spock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := buildUpdateRequest(tt.pr, tt.opts)

			// Version, title, and description are always carried over.
			assert.Equal(t, tt.pr.Version, req.Version)
			assert.Equal(t, tt.pr.Title, req.Title)
			require.NotNil(t, req.Description)
			assert.Equal(t, tt.pr.Description, *req.Description)

			names := make([]string, len(req.Reviewers))
			for i, rev := range req.Reviewers {
				names[i] = rev.User.Name
			}
			assert.Equal(t, tt.wantReviewers, names)

			if tt.wantToRef == "" {
				assert.Nil(t, req.ToRef)
			} else {
				require.NotNil(t, req.ToRef)
				assert.Equal(t, tt.wantToRef, req.ToRef.ID)
			}
		})
	}
}
