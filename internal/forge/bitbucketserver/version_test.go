package bitbucketserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDraftSupport(t *testing.T) {
	tests := []struct {
		name string

		// props is the /application-properties body, or nil to make the
		// endpoint fail with a 500.
		props map[string]any

		wantSupported bool
		wantKnown     bool
		wantVersion   string
	}{
		{
			name:          "supported/version",
			props:         map[string]any{"version": "9.4.0"},
			wantSupported: true,
			wantKnown:     true,
			wantVersion:   "9.4.0",
		},
		{
			name:          "supported/exactThreshold",
			props:         map[string]any{"version": "8.18.0"},
			wantSupported: true,
			wantKnown:     true,
			wantVersion:   "8.18.0",
		},
		{
			name:          "unsupported/justBelow",
			props:         map[string]any{"version": "8.17.9"},
			wantSupported: false,
			wantKnown:     true,
			wantVersion:   "8.17.9",
		},
		{
			name:          "supported/buildNumberFallback",
			props:         map[string]any{"version": "weird", "buildNumber": "8018000"},
			wantSupported: true,
			wantKnown:     true,
			wantVersion:   "weird",
		},
		{
			name:          "unsupported/buildNumberFallback",
			props:         map[string]any{"version": "weird", "buildNumber": "8017000"},
			wantSupported: false,
			wantKnown:     true,
			wantVersion:   "weird",
		},
		{
			// Neither a valid semver version nor a usable build number, so
			// support cannot be determined: known is false.
			name:          "unknown/invalidVersionAndBuildNumber",
			props:         map[string]any{"version": "weird", "buildNumber": "0"},
			wantSupported: false,
			wantKnown:     false,
			wantVersion:   "weird",
		},
		{
			name:          "unknown/endpointError",
			props:         nil,
			wantSupported: false,
			wantKnown:     false,
			wantVersion:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/rest/api/1.0/application-properties" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				if tt.props == nil {
					http.Error(w, "boom", http.StatusInternalServerError)
					return
				}
				writeJSON(t, w, http.StatusOK, tt.props)
			}))
			defer srv.Close()

			repo := newTestRepository(t, srv.URL, &RepositoryID{
				url:        srv.URL,
				projectKey: "ENG",
				slug:       "warp-core",
			})

			supported, known, version := repo.draftSupport(t.Context())
			assert.Equal(t, tt.wantSupported, supported, "supported")
			assert.Equal(t, tt.wantKnown, known, "known")
			assert.Equal(t, tt.wantVersion, version, "version")
		})
	}
}
