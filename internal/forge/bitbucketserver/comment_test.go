package bitbucketserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
)

// commentsPath is the REST path for creating/listing comments on a PR.
func commentsPath(prID int64) string {
	return prItemPath(prID) + "/comments"
}

// commentItemPath is the REST path for a single comment on a PR.
func commentItemPath(prID, commentID int64) string {
	return commentsPath(prID) + "/" + strconv.FormatInt(commentID, 10)
}

// activitiesPath is the REST path for a PR's activity feed.
func activitiesPath(prID int64) string {
	return prItemPath(prID) + "/activities"
}

// blockerCommentsPath is the REST path for a PR's flat task list.
func blockerCommentsPath(prID int64) string {
	return prItemPath(prID) + "/blocker-comments"
}

func TestPostChangeComment(t *testing.T) {
	var gotBody struct {
		Text string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, commentsPath(7), r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"id":      101,
			"version": 0,
			"text":    gotBody.Text,
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	id, err := repo.PostChangeComment(t.Context(), &PR{Number: 7}, "hello world")
	require.NoError(t, err)

	assert.Equal(t, "hello world", gotBody.Text)
	comment := id.(*PRComment)
	assert.Equal(t, int64(101), comment.ID)
	assert.Equal(t, int64(7), comment.PRID)
	assert.Equal(t, 0, comment.Version)
}

func TestUpdateChangeComment(t *testing.T) {
	var (
		gotVersion int
		gotText    string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, commentItemPath(7, 101), r.URL.Path)
		var body struct {
			Text    string `json:"text"`
			Version *int   `json:"version"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotText = body.Text
		require.NotNil(t, body.Version)
		gotVersion = *body.Version
		writeJSON(t, w, http.StatusOK, map[string]any{
			"id": 101, "version": 3, "text": body.Text,
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.UpdateChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 2}, "updated body")
	require.NoError(t, err)

	assert.Equal(t, "updated body", gotText)
	assert.Equal(t, 2, gotVersion)
}

func TestUpdateChangeComment_conflictRefetchRetry(t *testing.T) {
	var (
		putCount int
		versions []int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == commentItemPath(7, 101):
			putCount++
			var body struct {
				Version *int `json:"version"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NotNil(t, body.Version)
			versions = append(versions, *body.Version)
			if putCount == 1 {
				// Reject the first update with a version conflict.
				writeJSON(t, w, http.StatusConflict, map[string]any{
					"errors": []map[string]any{{"message": "comment out of date"}},
				})
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 101, "version": 10})
		case r.Method == http.MethodGet && r.URL.Path == activitiesPath(7):
			// The live version is 9, newer than the stale persisted 2.
			writeJSON(t, w, http.StatusOK, map[string]any{
				"isLastPage": true,
				"values": []map[string]any{
					{"action": "OPENED"},
					{
						"action": "COMMENTED",
						"comment": map[string]any{
							"id": 101, "version": 9, "text": "x",
						},
					},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.UpdateChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 2}, "updated body")
	require.NoError(t, err)

	assert.Equal(t, 2, putCount, "expected the update to be retried once")
	// First attempt uses the stale version; the retry uses the refreshed one.
	assert.Equal(t, []int{2, 9}, versions)
}

func TestUpdateChangeComment_deletedRecreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			return
		}
		// Comment was deleted between read and update.
		http.NotFound(w, r)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.UpdateChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 2}, "updated body")
	require.Error(t, err)
	// The sentinel that tells the caller to recreate the comment.
	assert.ErrorIs(t, err, forge.ErrNotFound)
}

func TestUpdateChangeComment_conflictThenGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			writeJSON(t, w, http.StatusConflict, map[string]any{
				"errors": []map[string]any{{"message": "out of date"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == activitiesPath(7):
			// The comment is absent from the feed: it was deleted.
			writeJSON(t, w, http.StatusOK, map[string]any{
				"isLastPage": true,
				"values": []map[string]any{
					{"action": "OPENED"},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.UpdateChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 2}, "updated body")
	require.Error(t, err)
	assert.ErrorIs(t, err, forge.ErrNotFound)
}

func TestDeleteChangeComment(t *testing.T) {
	var gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, commentItemPath(7, 101), r.URL.Path)
		gotVersion = r.URL.Query().Get("version")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.DeleteChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 4})
	require.NoError(t, err)

	// The optimistic-locking version travels in the query string.
	assert.Equal(t, "4", gotVersion)
}

func TestDeleteChangeComment_conflictRefetchRetry(t *testing.T) {
	var (
		deleteCount int
		versions    []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == commentItemPath(7, 101):
			deleteCount++
			versions = append(versions, r.URL.Query().Get("version"))
			if deleteCount == 1 {
				writeJSON(t, w, http.StatusConflict, map[string]any{
					"errors": []map[string]any{{"message": "out of date"}},
				})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == activitiesPath(7):
			writeJSON(t, w, http.StatusOK, map[string]any{
				"isLastPage": true,
				"values": []map[string]any{
					{
						"action": "COMMENTED",
						"comment": map[string]any{
							"id": 101, "version": 12,
						},
					},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	err := repo.DeleteChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 4})
	require.NoError(t, err)

	assert.Equal(t, 2, deleteCount, "expected the delete to be retried once")
	assert.Equal(t, []string{"4", "12"}, versions)
}

func TestDeleteChangeComment_alreadyDeleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	// A comment that is already gone deletes cleanly.
	err := repo.DeleteChangeComment(t.Context(),
		&PRComment{ID: 101, PRID: 7, Version: 4})
	require.NoError(t, err)
}

func TestListChangeComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, activitiesPath(7), r.URL.Path)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"isLastPage": true,
			"values": []map[string]any{
				{"action": "OPENED"},
				{
					"action": "COMMENTED",
					"comment": map[string]any{
						"id": 1, "version": 0, "text": "first comment",
						"author": map[string]any{"name": "alice"},
					},
				},
				{"action": "RESCOPED"},
				{
					"action": "COMMENTED",
					"comment": map[string]any{
						"id": 2, "version": 1, "text": "second comment",
						"author": map[string]any{"name": "bob"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)

	var got []*forge.ListChangeCommentItem
	for item, err := range repo.ListChangeComments(t.Context(), &PR{Number: 7}, nil) {
		require.NoError(t, err)
		got = append(got, item)
	}

	// Only COMMENTED activities are surfaced, in feed order.
	require.Len(t, got, 2)
	assert.Equal(t, "first comment", got[0].Body)
	assert.Equal(t, int64(1), got[0].ID.(*PRComment).ID)
	assert.Equal(t, int64(7), got[0].ID.(*PRComment).PRID)
	assert.Equal(t, "second comment", got[1].Body)
	assert.Equal(t, int64(2), got[1].ID.(*PRComment).ID)
}

func TestListChangeComments_bodyFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"isLastPage": true,
			"values": []map[string]any{
				{
					"action":  "COMMENTED",
					"comment": map[string]any{"id": 1, "text": "<!-- gs marker -->\nstack"},
				},
				{
					"action":  "COMMENTED",
					"comment": map[string]any{"id": 2, "text": "unrelated chatter"},
				},
			},
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	opts := &forge.ListChangeCommentsOptions{
		BodyMatchesAll: []*regexp.Regexp{regexp.MustCompile(`gs marker`)},
	}

	var got []*forge.ListChangeCommentItem
	for item, err := range repo.ListChangeComments(t.Context(), &PR{Number: 7}, opts) {
		require.NoError(t, err)
		got = append(got, item)
	}

	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].ID.(*PRComment).ID)
}

func TestListChangeComments_canUpdateFiltersToSelf(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/1.0/application-properties":
			// CurrentUser identifies the authenticated user via X-AUSERNAME.
			w.Header().Set("X-AUSERNAME", "alice")
			writeJSON(t, w, http.StatusOK, map[string]any{"version": "9.4.0"})
		case r.URL.Path == activitiesPath(7):
			writeJSON(t, w, http.StatusOK, map[string]any{
				"isLastPage": true,
				"values": []map[string]any{
					{
						"action": "COMMENTED",
						"comment": map[string]any{
							"id": 1, "text": "mine",
							"author": map[string]any{"name": "alice"},
						},
					},
					{
						"action": "COMMENTED",
						"comment": map[string]any{
							"id": 2, "text": "theirs",
							"author": map[string]any{"name": "bob"},
						},
					},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	opts := &forge.ListChangeCommentsOptions{CanUpdate: true}

	var got []*forge.ListChangeCommentItem
	for item, err := range repo.ListChangeComments(t.Context(), &PR{Number: 7}, opts) {
		require.NoError(t, err)
		got = append(got, item)
	}

	// Only the comment authored by the authenticated user (alice) remains.
	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].ID.(*PRComment).ID)
	assert.Equal(t, "mine", got[0].Body)
}

func TestPostUpdateDeleteRoundTrip(t *testing.T) {
	var (
		created  bool
		updated  bool
		deleted  bool
		lastText string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == commentsPath(7):
			created = true
			var body struct {
				Text string `json:"text"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			lastText = body.Text
			writeJSON(t, w, http.StatusCreated, map[string]any{
				"id": 55, "version": 0, "text": body.Text,
			})
		case r.Method == http.MethodPut && r.URL.Path == commentItemPath(7, 55):
			updated = true
			var body struct {
				Text string `json:"text"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			lastText = body.Text
			writeJSON(t, w, http.StatusOK, map[string]any{
				"id": 55, "version": 1, "text": body.Text,
			})
		case r.Method == http.MethodDelete && r.URL.Path == commentItemPath(7, 55):
			deleted = true
			assert.Equal(t, "1", r.URL.Query().Get("version"))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	ctx := t.Context()

	id, err := repo.PostChangeComment(ctx, &PR{Number: 7}, "v1")
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, "v1", lastText)

	require.NoError(t, repo.UpdateChangeComment(ctx, id, "v2"))
	require.True(t, updated)
	assert.Equal(t, "v2", lastText)

	// Track the version returned by the update so delete sends the right one.
	updatedID := &PRComment{ID: id.(*PRComment).ID, PRID: id.(*PRComment).PRID, Version: 1}
	require.NoError(t, repo.DeleteChangeComment(ctx, updatedID))
	require.True(t, deleted)
}

func TestLiveCommentVersion(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, activitiesPath(7), r.URL.Path)
			writeJSON(t, w, http.StatusOK, map[string]any{
				"isLastPage": true,
				"values": []map[string]any{
					// Non-comment activity must be skipped.
					{"action": "OPENED"},
					// Different comment ID must be skipped.
					{"action": "COMMENTED", "comment": map[string]any{"id": 100, "version": 1}},
					// Target comment: its live version is returned.
					{"action": "COMMENTED", "comment": map[string]any{"id": 101, "version": 5}},
				},
			})
		}))
		defer srv.Close()

		repo := newOpsTestRepository(t, srv)
		version, found, err := repo.liveCommentVersion(t.Context(), 7, 101)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, 5, version)
	})

	t.Run("NotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, http.StatusOK, map[string]any{
				"isLastPage": true,
				"values": []map[string]any{
					{"action": "COMMENTED", "comment": map[string]any{"id": 100, "version": 1}},
				},
			})
		}))
		defer srv.Close()

		repo := newOpsTestRepository(t, srv)
		version, found, err := repo.liveCommentVersion(t.Context(), 7, 101)
		require.NoError(t, err)
		assert.False(t, found)
		assert.Zero(t, version)
	})

	t.Run("Error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		repo := newOpsTestRepository(t, srv)
		_, found, err := repo.liveCommentVersion(t.Context(), 7, 101)
		require.Error(t, err)
		assert.False(t, found)
	})
}
