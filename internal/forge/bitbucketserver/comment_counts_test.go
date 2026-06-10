package bitbucketserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
)

func TestCommentCountsByChange(t *testing.T) {
	// PR 1's activity feed mixes resolved/unresolved comment threads
	// (general and inline), completed/open tasks, an unpublished draft, and
	// git-spice's own navigation comment. PR 2 has no comments.
	feeds := map[int64][]map[string]any{
		1: {
			{"action": "OPENED"},
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 10, "text": "resolved general comment",
				"severity": "NORMAL", "state": "OPEN", "threadResolved": true,
			}},
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 11, "text": "open general comment",
				"severity": "NORMAL", "state": "OPEN", "threadResolved": false,
			}},
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 12, "text": "open inline comment",
				"severity": "NORMAL", "state": "OPEN", "threadResolved": false,
				"anchor": map[string]any{"path": "src/Main.java", "line": 10},
			}},
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 13, "text": "completed task",
				"severity": "BLOCKER", "state": "RESOLVED", "threadResolved": false,
			}},
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 14, "text": "open task",
				"severity": "BLOCKER", "state": "OPEN", "threadResolved": false,
			}},
			// Excluded: an unpublished draft, visible only to its author.
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 15, "text": "pending draft", "state": "PENDING",
			}},
			// Excluded: git-spice's own navigation comment.
			{"action": "COMMENTED", "comment": map[string]any{
				"id": 16, "text": "stack\n\n" + _navigationCommentMarker,
				"severity": "NORMAL", "state": "OPEN",
			}},
		},
		2: {
			{"action": "OPENED"},
			{"action": "MERGED"},
		},
	}

	// The flat task list re-reports PR 1's two top-level tasks (13, 14); the
	// dedup by ID must keep them from being counted a second time. PR 2 has
	// no tasks.
	tasks := map[int64][]map[string]any{
		1: {
			{"id": 13, "text": "completed task", "severity": "BLOCKER", "state": "RESOLVED"},
			{"id": 14, "text": "open task", "severity": "BLOCKER", "state": "OPEN"},
		},
		2: {},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case activitiesPath(1):
			writeJSON(t, w, http.StatusOK, map[string]any{"isLastPage": true, "values": feeds[1]})
		case activitiesPath(2):
			writeJSON(t, w, http.StatusOK, map[string]any{"isLastPage": true, "values": feeds[2]})
		case blockerCommentsPath(1):
			writeJSON(t, w, http.StatusOK, map[string]any{"isLastPage": true, "values": tasks[1]})
		case blockerCommentsPath(2):
			writeJSON(t, w, http.StatusOK, map[string]any{"isLastPage": true, "values": tasks[2]})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	got, err := repo.CommentCountsByChange(t.Context(), []forge.ChangeID{
		&PR{Number: 1}, &PR{Number: 2},
	})
	require.NoError(t, err)

	// Order and length match the input.
	require.Len(t, got, 2)

	// PR 1: 5 threads counted (draft + navigation comment excluded); the
	// resolved general comment and the completed task are resolved. The two
	// top-level tasks re-reported by the flat task list are deduplicated, so
	// the total is unchanged.
	assert.Equal(t, &forge.CommentCounts{Total: 5, Resolved: 2, Unresolved: 3}, got[0])

	// PR 2: no comments.
	assert.Equal(t, &forge.CommentCounts{Total: 0, Resolved: 0, Unresolved: 0}, got[1])
}

func TestCommentCountsByChange_empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	got, err := repo.CommentCountsByChange(t.Context(), nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestCommentCountsByChange_tasks(t *testing.T) {
	// Each PR exercises one facet of folding the flat task list into the
	// activity-feed counts. The feed surfaces only thread roots, so a task
	// nested as a reply arrives solely via the task list.
	feeds := map[int64][]map[string]any{
		1: {{"action": "COMMENTED", "comment": map[string]any{
			"id": 20, "text": "please fix", "severity": "NORMAL", "state": "OPEN",
		}}},
		2: {{"action": "COMMENTED", "comment": map[string]any{
			"id": 30, "text": "nit", "severity": "NORMAL", "state": "OPEN",
		}}},
		3: {{"action": "COMMENTED", "comment": map[string]any{
			"id": 40, "text": "top-level task", "severity": "BLOCKER", "state": "OPEN",
		}}},
		4: {{"action": "COMMENTED", "comment": map[string]any{
			"id": 50, "text": "general comment", "severity": "NORMAL", "state": "OPEN",
		}}},
	}
	tasks := map[int64][]map[string]any{
		// A task nested as a reply: absent from the feed, added here.
		1: {{"id": 99, "text": "nested task", "severity": "BLOCKER", "state": "OPEN"}},
		// A resolved nested task.
		2: {{"id": 88, "text": "done nested task", "severity": "BLOCKER", "state": "RESOLVED"}},
		// The top-level task (deduped against the feed) plus a nested one (added).
		3: {
			{"id": 40, "text": "top-level task", "severity": "BLOCKER", "state": "OPEN"},
			{"id": 41, "text": "nested task", "severity": "BLOCKER", "state": "OPEN"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)

		// PR 4 stands in for a pre-7.2 server without the endpoint.
		if r.URL.Path == blockerCommentsPath(4) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		for prID := int64(1); prID <= 4; prID++ {
			switch r.URL.Path {
			case activitiesPath(prID):
				writeJSON(t, w, http.StatusOK, map[string]any{"isLastPage": true, "values": feeds[prID]})
				return
			case blockerCommentsPath(prID):
				writeJSON(t, w, http.StatusOK, map[string]any{"isLastPage": true, "values": tasks[prID]})
				return
			}
		}

		t.Errorf("unexpected path: %s", r.URL.Path)
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	got, err := repo.CommentCountsByChange(t.Context(), []forge.ChangeID{
		&PR{Number: 1}, &PR{Number: 2}, &PR{Number: 3}, &PR{Number: 4},
	})
	require.NoError(t, err)
	require.Len(t, got, 4)

	// PR 1: a review thread plus a nested open task pulled from the task list.
	assert.Equal(t, &forge.CommentCounts{Total: 2, Resolved: 0, Unresolved: 2}, got[0])
	// PR 2: the nested task is complete, so it counts as resolved.
	assert.Equal(t, &forge.CommentCounts{Total: 2, Resolved: 1, Unresolved: 1}, got[1])
	// PR 3: the top-level task is deduped; only the nested task is added.
	assert.Equal(t, &forge.CommentCounts{Total: 2, Resolved: 0, Unresolved: 2}, got[2])
	// PR 4: the endpoint 404s; the activity-feed result stands, no error.
	assert.Equal(t, &forge.CommentCounts{Total: 1, Resolved: 0, Unresolved: 1}, got[3])
}
