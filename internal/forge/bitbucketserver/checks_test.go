package bitbucketserver

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
)

func buildStatusPath(sha string) string {
	return "/rest/build-status/1.0/commits/" + sha
}

func TestChangeChecksState(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   forge.ChecksState
	}{
		{"Empty", nil, forge.ChecksPassed},
		{"Successful", []string{"SUCCESSFUL"}, forge.ChecksPassed},
		{"InProgress", []string{"SUCCESSFUL", "INPROGRESS"}, forge.ChecksPending},
		{"Failed", []string{"SUCCESSFUL", "INPROGRESS", "FAILED"}, forge.ChecksFailed},
		{"FailedBeatsPending", []string{"INPROGRESS", "FAILED"}, forge.ChecksFailed},
		// An unknown/future state (e.g. CANCELLED or STOPPED) must not be
		// reported as passing.
		{"Unknown", []string{"CANCELLED"}, forge.ChecksFailed},
		{"UnknownBeatsSuccessful", []string{"SUCCESSFUL", "CANCELLED"}, forge.ChecksFailed},
	}

	const sha = "feedface"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == prItemPath(7):
					writeJSON(t, w, http.StatusOK, map[string]any{
						"id":      7,
						"state":   "OPEN",
						"fromRef": map[string]any{"latestCommit": sha},
					})
				case r.Method == http.MethodGet && r.URL.Path == buildStatusPath(sha):
					values := make([]map[string]any, 0, len(tt.states))
					for i, s := range tt.states {
						values = append(values, map[string]any{
							"key":   "build-" + strconv.Itoa(i),
							"state": s,
						})
					}
					writeJSON(t, w, http.StatusOK, map[string]any{
						"isLastPage": true,
						"values":     values,
					})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()

			repo := newOpsTestRepository(t, srv)
			got, err := repo.ChangeChecksState(t.Context(), &PR{Number: 7})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChangeChecksState_noHeadCommit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, prItemPath(7), r.URL.Path)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"id":      7,
			"state":   "OPEN",
			"fromRef": map[string]any{}, // no latestCommit
		})
	}))
	defer srv.Close()

	repo := newOpsTestRepository(t, srv)
	got, err := repo.ChangeChecksState(t.Context(), &PR{Number: 7})
	require.NoError(t, err)
	// No head commit -> nothing to check -> passing.
	assert.Equal(t, forge.ChecksPassed, got)
}
