package bitbucketserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
	"go.abhg.dev/gs/internal/git"
)

const (
	testProjectKey = "ENG"
	testSlug       = "warp-core"
)

func TestFindChangesByBranch(t *testing.T) {
	var gotQuery struct {
		at, direction, state string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, prListPath(), r.URL.Path)
		gotQuery.at = r.URL.Query().Get("at")
		gotQuery.direction = r.URL.Query().Get("direction")
		gotQuery.state = r.URL.Query().Get("state")

		writeJSON(t, w, http.StatusOK, map[string]any{
			"isLastPage": true,
			"values": []map[string]any{
				{
					"id":      11,
					"version": 3,
					"title":   "Refit the warp core",
					"state":   "OPEN",
					"open":    true,
					"draft":   true,
					"fromRef": map[string]any{
						"displayId":    "feature",
						"latestCommit": "abc123",
					},
					"toRef": map[string]any{
						"displayId": "develop",
					},
					"reviewers": []map[string]any{
						{"user": map[string]any{"name": "spock"}},
						{"user": map[string]any{"name": "uhura"}},
					},
					"links": map[string]any{
						"self": []map[string]any{
							{"href": "https://bb.example.com/pr/11"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	items, err := repo.FindChangesByBranch(t.Context(), "feature", forge.FindChangesOptions{})
	require.NoError(t, err)

	// A zero state means "all states" (per the forge interface contract),
	// which Bitbucket Data Center expresses as the ALL filter.
	assert.Equal(t, "refs/heads/feature", gotQuery.at)
	assert.Equal(t, "OUTGOING", gotQuery.direction)
	assert.Equal(t, "ALL", gotQuery.state)

	require.Len(t, items, 1)
	got := items[0]
	assert.Equal(t, int64(11), got.ID.(*PR).Number)
	assert.Equal(t, "https://bb.example.com/pr/11", got.URL)
	assert.Equal(t, forge.ChangeOpen, got.State)
	assert.Equal(t, "Refit the warp core", got.Subject)
	assert.Equal(t, "develop", got.BaseName)
	assert.Equal(t, git.Hash("abc123"), got.HeadHash)
	assert.True(t, got.Draft)
	assert.Equal(t, []string{"spock", "uhura"}, got.Reviewers)
}

func TestFindChangesByBranch_stateFilter(t *testing.T) {
	tests := []struct {
		name      string
		state     forge.ChangeState
		wantState string
	}{
		{"AllStates", 0, "ALL"},
		{"Open", forge.ChangeOpen, "OPEN"},
		{"Merged", forge.ChangeMerged, "MERGED"},
		{"Closed", forge.ChangeClosed, "DECLINED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotState string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotState = r.URL.Query().Get("state")
				writeJSON(t, w, http.StatusOK, map[string]any{
					"isLastPage": true,
					"values":     []map[string]any{},
				})
			}))
			defer srv.Close()

			repo := newOpsTestRepository(t, srv)
			_, err := repo.FindChangesByBranch(t.Context(), "feature",
				forge.FindChangesOptions{State: tt.state})
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, gotState)
		})
	}
}

func TestFindChangesByBranch_respectsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		values := make([]map[string]any, 0, 5)
		for i := int64(1); i <= 5; i++ {
			values = append(values, map[string]any{
				"id": i, "title": "PR", "state": "OPEN",
			})
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"isLastPage": true,
			"values":     values,
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	items, err := repo.FindChangesByBranch(t.Context(), "feature",
		forge.FindChangesOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestFindChangeByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, prItemPath(42), r.URL.Path)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"id":      42,
			"version": 1,
			"title":   "Test PR",
			"state":   "MERGED",
			"fromRef": map[string]any{"latestCommit": "deadbeef"},
			"toRef":   map[string]any{"displayId": "main"},
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	item, err := repo.FindChangeByID(t.Context(), &PR{Number: 42})
	require.NoError(t, err)

	assert.Equal(t, int64(42), item.ID.(*PR).Number)
	assert.Equal(t, "Test PR", item.Subject)
	assert.Equal(t, "main", item.BaseName)
	assert.Equal(t, git.Hash("deadbeef"), item.HeadHash)
	assert.Equal(t, forge.ChangeMerged, item.State)
	// No self link: URL falls back to the repository-derived change URL.
	assert.Equal(t, srv.URL+"/projects/ENG/repos/warp-core/pull-requests/42/overview", item.URL)
}

func TestFindChangeByID_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	_, err := repo.FindChangeByID(t.Context(), &PR{Number: 99})
	require.Error(t, err)
	assert.ErrorIs(t, err, bitbucketserver.ErrNotFound)
}

func TestChangeStatuses(t *testing.T) {
	states := map[int64]string{1: "OPEN", 2: "MERGED", 3: "DECLINED"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		id, err := strconv.ParseInt(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], 10, 64)
		require.NoError(t, err)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"id":      id,
			"state":   states[id],
			"fromRef": map[string]any{"latestCommit": "hash" + strconv.FormatInt(id, 10)},
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	got, err := repo.ChangeStatuses(t.Context(), []forge.ChangeID{
		&PR{Number: 1}, &PR{Number: 2}, &PR{Number: 3},
	})
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Equal(t, forge.ChangeOpen, got[0].State)
	assert.Equal(t, git.Hash("hash1"), got[0].HeadHash)
	assert.Equal(t, forge.ChangeMerged, got[1].State)
	assert.Equal(t, forge.ChangeClosed, got[2].State)
}

func TestEditChange_baseAndReviewers(t *testing.T) {
	var gotUpdate bitbucketserver.PullRequestUpdateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == prItemPath(7):
			writeJSON(t, w, http.StatusOK, map[string]any{
				"id":          7,
				"version":     4,
				"title":       "Original title",
				"description": "Original description",
				"state":       "OPEN",
				"author":      map[string]any{"user": map[string]any{"name": "jcaptain"}},
				"reviewers": []map[string]any{
					// A default reviewer auto-injected by Bitbucket DC.
					{"user": map[string]any{"name": "default-rev"}},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == prItemPath(7):
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotUpdate))
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 7, "version": 5, "title": "Original title"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.EditChange(t.Context(), &PR{Number: 7}, forge.EditChangeOptions{
		Base:         "develop",
		AddReviewers: []string{"spock", "jcaptain"}, // author must be excluded
	})
	require.NoError(t, err)

	// Version, current title, and current description are carried so the
	// wholesale update does not clear them.
	assert.Equal(t, 4, gotUpdate.Version)
	assert.Equal(t, "Original title", gotUpdate.Title)
	require.NotNil(t, gotUpdate.Description)
	assert.Equal(t, "Original description", *gotUpdate.Description)

	// Base change sets the new toRef.
	require.NotNil(t, gotUpdate.ToRef)
	assert.Equal(t, "refs/heads/develop", gotUpdate.ToRef.ID)

	// Reviewers: existing default reviewer preserved, spock added,
	// author (jcaptain) excluded.
	names := make([]string, len(gotUpdate.Reviewers))
	for i, rev := range gotUpdate.Reviewers {
		names[i] = rev.User.Name
	}
	assert.Equal(t, []string{"default-rev", "spock"}, names)
}

func TestEditChange_noChangesIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	require.NoError(t, repo.EditChange(t.Context(), &PR{Number: 7}, forge.EditChangeOptions{}))
}

func TestEditChange_conflictRetries(t *testing.T) {
	var (
		getCount int
		putCount int
		versions []int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCount++
			// First GET reports version 4 (stale); refetch reports 9.
			version := 4
			if getCount > 1 {
				version = 9
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"id": 7, "version": version, "title": "T",
				"author": map[string]any{"user": map[string]any{"name": "jcaptain"}},
			})
		case http.MethodPut:
			putCount++
			var req bitbucketserver.PullRequestUpdateRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			versions = append(versions, req.Version)
			if putCount == 1 {
				// Reject the first PUT with a version conflict.
				writeJSON(t, w, http.StatusConflict, map[string]any{
					"errors": []map[string]any{{"message": "out of date"}},
				})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 7, "version": 10, "title": "T"})
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.EditChange(t.Context(), &PR{Number: 7}, forge.EditChangeOptions{
		AddReviewers: []string{"spock"},
	})
	require.NoError(t, err)

	assert.Equal(t, 2, getCount, "expected an initial GET plus one refetch")
	assert.Equal(t, 2, putCount, "expected the PUT to be retried once")
	// The retry carries the refreshed version.
	assert.Equal(t, []int{4, 9}, versions)
}

func TestMergeChange_strategyMapping(t *testing.T) {
	tests := []struct {
		name         string
		method       forge.MergeMethod
		wantStrategy string
		wantOmitted  bool
	}{
		{"Default", forge.MergeMethodDefault, "", true},
		{"Merge", forge.MergeMethodMerge, "no-ff", false},
		{"Squash", forge.MergeMethodSquash, "squash", false},
		{"Rebase", forge.MergeMethodRebase, "rebase-no-ff", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				gotVersion string
				gotRaw     map[string]any
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge"):
					writeJSON(t, w, http.StatusOK, map[string]any{"canMerge": true, "outcome": "CLEAN"})
				case r.Method == http.MethodGet:
					writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 2, "state": "OPEN"})
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
					gotVersion = r.URL.Query().Get("version")
					require.NoError(t, json.NewDecoder(r.Body).Decode(&gotRaw))
					writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 3, "state": "MERGED"})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()

			repo := newOpsTestRepository(t, srv)
			err := repo.MergeChange(t.Context(), &PR{Number: 5},
				forge.MergeChangeOptions{Method: tt.method})
			require.NoError(t, err)

			assert.Equal(t, "2", gotVersion)
			if tt.wantOmitted {
				assert.NotContains(t, gotRaw, "strategyId")
			} else {
				assert.Equal(t, tt.wantStrategy, gotRaw["strategyId"])
			}
		})
	}
}

func TestMergeChange_conflictRetries(t *testing.T) {
	var (
		getCount   int
		mergeCount int
		versions   []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge"):
			writeJSON(t, w, http.StatusOK, map[string]any{"canMerge": true, "outcome": "CLEAN"})
		case r.Method == http.MethodGet:
			getCount++
			version := 2
			if getCount > 1 {
				version = 8
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": version, "state": "OPEN"})
		case strings.HasSuffix(r.URL.Path, "/merge"):
			mergeCount++
			versions = append(versions, r.URL.Query().Get("version"))
			if mergeCount == 1 {
				writeJSON(t, w, http.StatusConflict, map[string]any{
					"errors": []map[string]any{{"message": "stale"}},
				})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 9, "state": "MERGED"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.MergeChange(t.Context(), &PR{Number: 5}, forge.MergeChangeOptions{})
	require.NoError(t, err)

	assert.Equal(t, 2, getCount)
	assert.Equal(t, 2, mergeCount)
	assert.Equal(t, []string{"2", "8"}, versions)
}

func TestMergeChange_disabledStrategyFails(t *testing.T) {
	// A merge strategy the repository does not allow is surfaced as an
	// error rather than silently merging with a different strategy.
	var merges int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge"):
			writeJSON(t, w, http.StatusOK, map[string]any{"canMerge": true, "outcome": "CLEAN"})
		case r.Method == http.MethodGet:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 2, "state": "OPEN"})
		case strings.HasSuffix(r.URL.Path, "/merge"):
			merges++
			writeJSON(t, w, http.StatusBadRequest, map[string]any{
				"errors": []map[string]any{
					{"message": "The merge strategy squash is not enabled for this repository."},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.MergeChange(t.Context(), &PR{Number: 5},
		forge.MergeChangeOptions{Method: forge.MergeMethodSquash})
	require.Error(t, err)
	assert.ErrorContains(t, err, "not enabled for this repository")
	assert.Equal(t, 1, merges, "the merge must not be retried")
}

func TestMergeChange_blockedByVeto(t *testing.T) {
	var merged bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge"):
			writeJSON(t, w, http.StatusOK, map[string]any{
				"canMerge":   false,
				"conflicted": false,
				"outcome":    "UNKNOWN",
				"vetoes": []map[string]any{
					{"summaryMessage": "requires 2 approvals"},
				},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
			merged = true
			t.Errorf("merge must not be attempted when blocked by a veto")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.MergeChange(t.Context(), &PR{Number: 5}, forge.MergeChangeOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errMergeBlocked)
	assert.Contains(t, err.Error(), "requires 2 approvals")
	assert.False(t, merged, "the merge POST must never be issued")
}

func TestMergeChange_probeErrorFallsThrough(t *testing.T) {
	var (
		versionFetched bool
		merged         bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge"):
			// The pre-merge probe fails; merge must proceed regardless.
			http.Error(w, "boom", http.StatusInternalServerError)
		case r.Method == http.MethodGet:
			versionFetched = true
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 2, "state": "OPEN"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
			merged = true
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 3, "state": "MERGED"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.MergeChange(t.Context(), &PR{Number: 5}, forge.MergeChangeOptions{})
	require.NoError(t, err)
	assert.True(t, versionFetched, "the version GET must still happen")
	assert.True(t, merged, "the merge POST must still happen")
}

func TestMergeChange_canMergeCleanProceeds(t *testing.T) {
	var merged bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge"):
			writeJSON(t, w, http.StatusOK, map[string]any{"canMerge": true, "outcome": "CLEAN"})
		case r.Method == http.MethodGet:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 2, "state": "OPEN"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
			merged = true
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 5, "version": 3, "state": "MERGED"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.MergeChange(t.Context(), &PR{Number: 5}, forge.MergeChangeOptions{})
	require.NoError(t, err)
	assert.True(t, merged, "a clean pre-merge check must let the merge proceed")
}

// prListPath and prItemPath build the REST paths the forge operations
// hit for the test repository.
func prListPath() string {
	return "/rest/api/1.0/projects/" + testProjectKey + "/repos/" + testSlug + "/pull-requests"
}

func prItemPath(id int64) string {
	return prListPath() + "/" + strconv.FormatInt(id, 10)
}

func newOpsTestRepository(t *testing.T, srv *httptest.Server) *Repository {
	t.Helper()
	return newTestRepository(t, srv.URL, &RepositoryID{
		url:        srv.URL,
		projectKey: testProjectKey,
		slug:       testSlug,
	})
}
