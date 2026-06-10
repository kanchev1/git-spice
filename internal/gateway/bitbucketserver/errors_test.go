package bitbucketserver

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDestinationBranchNotFound(t *testing.T) {
	newResp := func(method, path string, status int) *http.Response {
		return &http.Response{
			StatusCode: status,
			Request: &http.Request{
				Method: method,
				URL:    &url.URL{Path: path},
			},
		}
	}

	const createPath = "/rest/api/1.0/projects/ENG/repos/warp-core/pull-requests"

	tests := []struct {
		name    string
		resp    *http.Response
		details []APIErrorDetail
		want    bool
	}{
		{
			// Realistic validation 400: exceptionName is null; the message
			// is the only structured signal.
			name: "NullExceptionName",
			resp: newResp(http.MethodPost, createPath, http.StatusBadRequest),
			details: []APIErrorDetail{
				{Message: `The branch "refs/heads/main" does not exist.`},
			},
			want: true,
		},
		{
			name: "WithExceptionName",
			resp: newResp(http.MethodPost, createPath, http.StatusBadRequest),
			details: []APIErrorDetail{
				{
					Message:       `The branch "refs/heads/main" does not exist.`,
					ExceptionName: "com.atlassian.bitbucket.validation.ArgumentValidationException",
				},
			},
			want: true,
		},
		{
			name: "UnrelatedBadRequest",
			resp: newResp(http.MethodPost, createPath, http.StatusBadRequest),
			details: []APIErrorDetail{
				{Message: "Only one pull request may be open for a given source and target branch."},
			},
			want: false,
		},
		{
			name:    "NotBadRequest",
			resp:    newResp(http.MethodPost, createPath, http.StatusConflict),
			details: []APIErrorDetail{{Message: "branch does not exist"}},
			want:    false,
		},
		{
			name:    "WrongMethod",
			resp:    newResp(http.MethodGet, createPath, http.StatusBadRequest),
			details: []APIErrorDetail{{Message: "branch does not exist"}},
			want:    false,
		},
		{
			name:    "WrongPath",
			resp:    newResp(http.MethodPost, "/rest/api/1.0/projects/ENG/repos/warp-core/pull-requests/7", http.StatusBadRequest),
			details: []APIErrorDetail{{Message: "branch does not exist"}},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isDestinationBranchNotFound(tt.resp, tt.details))
		})
	}
}
