package bitbucketserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
	"go.abhg.dev/gs/internal/silog"
)

func TestSubmitChange(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
		author     = "jcaptain"
	)

	var gotReq bitbucketserver.PullRequestCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// CurrentUser: identity comes from the X-AUSERNAME header.
			w.Header().Set("X-AUSERNAME", author)
			writeJSON(t, w, http.StatusOK, map[string]any{"version": "9.4.0"})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			// RepositoryGet: resolve the numeric repository ID.
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			// DefaultReviewers: no project default reviewers configured.
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
			writeJSON(t, w, http.StatusCreated, map[string]any{
				"id":      123,
				"version": 0,
				"title":   gotReq.Title,
				"links": map[string]any{
					"self": []map[string]any{
						{"href": "https://bitbucket.example.com/projects/ENG/repos/warp-core/pull-requests/123/overview"},
					},
				},
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	result, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject:   "Refit the warp core",
		Body:      "Long overdue.",
		Head:      "feature",
		Base:      "main",
		Draft:     true,
		Reviewers: []string{"spock", author}, // author must be filtered out
	})
	require.NoError(t, err)

	// Request body assertions.
	assert.Equal(t, "Refit the warp core", gotReq.Title)
	assert.Equal(t, "Long overdue.", gotReq.Description)
	assert.True(t, gotReq.Draft)

	assert.Equal(t, "refs/heads/feature", gotReq.FromRef.ID)
	assert.Equal(t, slug, gotReq.FromRef.Repository.Slug)
	assert.Equal(t, projectKey, gotReq.FromRef.Repository.Project.Key)

	assert.Equal(t, "refs/heads/main", gotReq.ToRef.ID)
	assert.Equal(t, slug, gotReq.ToRef.Repository.Slug)
	assert.Equal(t, projectKey, gotReq.ToRef.Repository.Project.Key)

	// Author (jcaptain) is filtered out; only spock remains.
	require.Len(t, gotReq.Reviewers, 1)
	assert.Equal(t, "spock", gotReq.Reviewers[0].User.Name)

	// Result assertions.
	pr := result.ID.(*PR)
	assert.Equal(t, int64(123), pr.Number)
	assert.Equal(t,
		"https://bitbucket.example.com/projects/ENG/repos/warp-core/pull-requests/123/overview",
		result.URL)
}

func TestSubmitChange_noReviewersSkipsCurrentUser(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	// /application-properties is hit by both CurrentUser and the draft
	// (ApplicationProperties) probe. With Draft unset the draft probe never
	// runs, so the only thing that can hit it here is CurrentUser; tracking
	// the endpoint therefore still proves no current-user lookup happens.
	var appPropsHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			appPropsHit = true
			w.Header().Set("X-AUSERNAME", "jcaptain")
			writeJSON(t, w, http.StatusOK, map[string]any{"version": "9.4.0"})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			// No default reviewers, so the candidate list stays empty.
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost:
			var req bitbucketserver.PullRequestCreateRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Empty(t, req.Reviewers)
			writeJSON(t, w, http.StatusCreated, map[string]any{"id": 7, "version": 0})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	result, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
	})
	require.NoError(t, err)

	assert.False(t, appPropsHit,
		"CurrentUser must not be called when there are no reviewers")

	// No self link: URL falls back to the repository-derived change URL.
	assert.Equal(t, int64(7), result.ID.(*PR).Number)
	assert.Equal(t,
		srv.URL+"/projects/ENG/repos/warp-core/pull-requests/7/overview",
		result.URL)
}

func TestSubmitChange_mergesDefaultReviewers(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
		author     = "jcaptain"
	)

	tests := []struct {
		name             string
		defaultReviewers []map[string]any
		reviewers        []string
		want             []string
	}{
		{
			// Default reviewers come first, the author is filtered out, and
			// the explicit reviewer follows.
			name:             "DefaultsFirst",
			defaultReviewers: []map[string]any{{"name": "alice", "id": 10}},
			reviewers:        []string{"spock", author},
			want:             []string{"alice", "spock"},
		},
		{
			// A default reviewer that is also requested explicitly appears
			// only once, keeping its defaults-first position.
			name:             "DedupAcrossSources",
			defaultReviewers: []map[string]any{{"name": "alice", "id": 10}},
			reviewers:        []string{"alice", "spock"},
			want:             []string{"alice", "spock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotReq bitbucketserver.PullRequestCreateRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/rest/api/1.0/application-properties":
					w.Header().Set("X-AUSERNAME", author)
					writeJSON(t, w, http.StatusOK, map[string]any{"version": "9.4.0"})

				case r.Method == http.MethodGet &&
					r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
					writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

				case r.Method == http.MethodGet &&
					r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
					writeJSON(t, w, http.StatusOK, tt.defaultReviewers)

				case r.Method == http.MethodPost &&
					r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
					require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
					writeJSON(t, w, http.StatusCreated, map[string]any{"id": 1, "version": 0})

				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				}
			}))
			defer srv.Close()

			repo := newTestRepository(t, srv.URL, &RepositoryID{
				url:        srv.URL,
				projectKey: projectKey,
				slug:       slug,
			})

			_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
				Subject:   "Refit",
				Head:      "feature",
				Base:      "main",
				Reviewers: tt.reviewers,
			})
			require.NoError(t, err)

			got := make([]string, len(gotReq.Reviewers))
			for i, rev := range gotReq.Reviewers {
				got[i] = rev.User.Name
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSubmitChange_defaultReviewersFailureIsBestEffort(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
		author     = "jcaptain"
	)

	tests := []struct {
		name string
		// repoGetStatus and reviewersStatus select where the best-effort
		// failure occurs.
		repoGetStatus   int
		reviewersStatus int
	}{
		{
			// The repository-ID lookup fails, so default reviewers are
			// skipped entirely.
			name:            "RepositoryGetFails",
			repoGetStatus:   http.StatusInternalServerError,
			reviewersStatus: http.StatusOK,
		},
		{
			// The repository ID resolves but the default-reviewers call
			// fails.
			name:            "DefaultReviewersFails",
			repoGetStatus:   http.StatusOK,
			reviewersStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotReq bitbucketserver.PullRequestCreateRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/rest/api/1.0/application-properties":
					w.Header().Set("X-AUSERNAME", author)
					writeJSON(t, w, http.StatusOK, map[string]any{"version": "9.4.0"})

				case r.Method == http.MethodGet &&
					r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
					if tt.repoGetStatus != http.StatusOK {
						http.Error(w, "boom", tt.repoGetStatus)
						return
					}
					writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

				case r.Method == http.MethodGet &&
					r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
					if tt.reviewersStatus != http.StatusOK {
						http.Error(w, "boom", tt.reviewersStatus)
						return
					}
					writeJSON(t, w, http.StatusOK, []any{})

				case r.Method == http.MethodPost &&
					r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
					require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
					writeJSON(t, w, http.StatusCreated, map[string]any{"id": 1, "version": 0})

				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				}
			}))
			defer srv.Close()

			repo := newTestRepository(t, srv.URL, &RepositoryID{
				url:        srv.URL,
				projectKey: projectKey,
				slug:       slug,
			})

			// The submit still succeeds, using only the explicit reviewer.
			_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
				Subject:   "Refit",
				Head:      "feature",
				Base:      "main",
				Reviewers: []string{"spock"},
			})
			require.NoError(t, err)

			require.Len(t, gotReq.Reviewers, 1)
			assert.Equal(t, "spock", gotReq.Reviewers[0].User.Name)
		})
	}
}

// When only default reviewers are present and the current-user lookup fails,
// the defaults are dropped (since self cannot be filtered out) and the submit
// proceeds, consistent with default reviewers being best-effort everywhere.
func TestSubmitChange_defaultReviewersOnlyCurrentUserErrorIsBestEffort(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	var gotReq bitbucketserver.PullRequestCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// CurrentUser cannot be resolved. (Draft is unset, so the draft
			// probe never hits this endpoint.)
			http.Error(w, "boom", http.StatusInternalServerError)

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			writeJSON(t, w, http.StatusOK, []map[string]any{{"name": "alice"}})

		case r.Method == http.MethodPost &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
			writeJSON(t, w, http.StatusCreated, map[string]any{"id": 1, "version": 0})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	// No explicit reviewers: the failed current-user lookup must not fail the
	// submit.
	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
	})
	require.NoError(t, err)

	// The default reviewer is dropped because self could not be resolved.
	assert.Empty(t, gotReq.Reviewers)
}

// When explicit reviewers are requested, the current-user lookup must succeed
// so they can be self-filtered before they are sent; a failure is fatal.
func TestSubmitChange_explicitReviewersCurrentUserErrorFails(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// CurrentUser cannot be resolved. (Draft is unset, so the draft
			// probe never hits this endpoint.)
			http.Error(w, "boom", http.StatusInternalServerError)

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost:
			t.Errorf("pull request must not be created when current-user lookup fails")
			http.Error(w, "unexpected create", http.StatusInternalServerError)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject:   "Refit",
		Head:      "feature",
		Base:      "main",
		Reviewers: []string{"spock"},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "identify current user")
}

func TestSubmitChange_draftDowngradedOnOldServer(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	var gotReq bitbucketserver.PullRequestCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// Server too old for draft pull requests.
			writeJSON(t, w, http.StatusOK, map[string]any{"version": "8.17.0"})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
			writeJSON(t, w, http.StatusCreated, map[string]any{"id": 1, "version": 0})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
		Draft:   true,
	})
	require.NoError(t, err)

	// The old server cannot honor draft, so the create request is
	// downgraded to a regular pull request.
	assert.False(t, gotReq.Draft)
}

func TestSubmitChange_draftKeptOnNewServer(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	var gotReq bitbucketserver.PullRequestCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// Exactly the first version that supports draft pull requests.
			writeJSON(t, w, http.StatusOK, map[string]any{"version": "8.18.0"})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
			writeJSON(t, w, http.StatusCreated, map[string]any{
				"id": 1, "version": 0, "draft": true,
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
		Draft:   true,
	})
	require.NoError(t, err)

	// A new-enough server honors draft, so the flag is sent as-is.
	assert.True(t, gotReq.Draft)
}

func TestSubmitChange_draftBestEffortUnknownVersion(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	var gotReq bitbucketserver.PullRequestCreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// Version cannot be read; the forge proceeds best-effort.
			http.Error(w, "boom", http.StatusInternalServerError)

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug+"/pull-requests":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
			// The server echoes a non-draft pull request, exercising the
			// post-create warning path.
			writeJSON(t, w, http.StatusCreated, map[string]any{
				"id": 1, "version": 0, "draft": false,
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
		Draft:   true,
	})
	require.NoError(t, err)

	// With an unreadable version, draft:true is still sent best-effort.
	assert.True(t, gotReq.Draft)
}

func TestSubmitChange_destinationBranchMissing(t *testing.T) {
	const (
		projectKey = "ENG"
		slug       = "warp-core"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/api/1.0/projects/"+projectKey+"/repos/"+slug:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 42, "slug": slug})

		case r.Method == http.MethodGet &&
			r.URL.Path == "/rest/default-reviewers/1.0/projects/"+projectKey+"/repos/"+slug+"/reviewers":
			writeJSON(t, w, http.StatusOK, []any{})

		case r.Method == http.MethodPost:
			// The create POST fails because the destination branch is
			// missing, which must map to forge.ErrUnsubmittedBase.
			writeJSON(t, w, http.StatusBadRequest, map[string]any{
				"errors": []map[string]any{
					{
						"message":       `The branch "refs/heads/main" does not exist.`,
						"exceptionName": "com.atlassian.bitbucket.validation.ArgumentValidationException",
					},
				},
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: projectKey,
		slug:       slug,
	})

	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, forge.ErrUnsubmittedBase)
}

func TestSubmitChange_crossRepositoryUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	repo := newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: "ENG",
		slug:       "warp-core",
	})

	_, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Refit",
		Head:    "feature",
		Base:    "main",
		PushRepository: &RepositoryID{
			url:        srv.URL,
			projectKey: "ENG",
			slug:       "warp-core-fork",
		},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "cross-repository")
}

// newTestRepository opens a Repository against the given test server URL,
// using OpenRepository so the gateway client is wired exactly as in
// production (BaseURL = URL + "/rest/api/1.0").
func newTestRepository(t *testing.T, serverURL string, rid *RepositoryID) *Repository {
	t.Helper()

	f := &Forge{
		Log:     silog.Nop(),
		Options: Options{URL: serverURL},
	}

	r, err := f.OpenRepository(
		t.Context(),
		&AuthenticationToken{AccessToken: "test-token"},
		rid,
	)
	require.NoError(t, err)
	return r.(*Repository)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(t *testing.T, w http.ResponseWriter, code int, v any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	require.NoError(t, json.NewEncoder(w).Encode(v))
}
