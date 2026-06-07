package bitbucketserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/secret"
	"go.abhg.dev/gs/internal/silog"
)

func TestAuthenticationToken_saveLoadClear(t *testing.T) {
	f := &Forge{
		Log:     silog.Nop(),
		Options: Options{URL: "https://bitbucket.example.com"},
	}
	var stash secret.MemoryStash

	want := &AuthenticationToken{AccessToken: "secret-token"}

	require.NoError(t, f.SaveAuthenticationToken(&stash, want))

	got, err := f.LoadAuthenticationToken(&stash)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	require.NoError(t, f.ClearAuthenticationToken(&stash))

	_, err = f.LoadAuthenticationToken(&stash)
	require.Error(t, err)
	assert.ErrorContains(t, err, "load stored token")
}

func TestAuthenticationToken_keyedByURL(t *testing.T) {
	// Tokens are keyed by the configured instance URL,
	// so two forges with different URLs do not share credentials.
	var stash secret.MemoryStash

	fa := &Forge{Log: silog.Nop(), Options: Options{URL: "https://a.example.com"}}
	fb := &Forge{Log: silog.Nop(), Options: Options{URL: "https://b.example.com"}}

	require.NoError(t, fa.SaveAuthenticationToken(&stash,
		&AuthenticationToken{AccessToken: "tok-a"}))

	// fb has no token stored.
	_, err := fb.LoadAuthenticationToken(&stash)
	require.Error(t, err)

	got, err := fa.LoadAuthenticationToken(&stash)
	require.NoError(t, err)
	assert.Equal(t, "tok-a", got.(*AuthenticationToken).AccessToken)
}

func TestLoadAuthenticationToken_envPrecedence(t *testing.T) {
	// BITBUCKET_SERVER_TOKEN (modeled via Options.Token) takes precedence
	// over any stored token.
	f := &Forge{
		Log: silog.Nop(),
		Options: Options{
			URL:   "https://bitbucket.example.com",
			Token: "env-token",
		},
	}
	var stash secret.MemoryStash

	// Store a different token in the stash.
	require.NoError(t, stash.SaveSecret(f.URL(), "token",
		`{"access_token":"stashed-token"}`))

	got, err := f.LoadAuthenticationToken(&stash)
	require.NoError(t, err)

	assert.Equal(t, "env-token", got.(*AuthenticationToken).AccessToken)
}

func TestSaveAuthenticationToken_skipsEnvToken(t *testing.T) {
	// When the token equals the env-provided token, it is not persisted.
	f := &Forge{
		Log: silog.Nop(),
		Options: Options{
			URL:   "https://bitbucket.example.com",
			Token: "env-token",
		},
	}
	var stash secret.MemoryStash

	require.NoError(t, f.SaveAuthenticationToken(&stash,
		&AuthenticationToken{AccessToken: "env-token"}))

	_, err := stash.LoadSecret(f.URL(), "token")
	assert.ErrorIs(t, err, secret.ErrNotFound)
}

func TestForge_tokenHelp(t *testing.T) {
	f := &Forge{Options: Options{URL: "https://bitbucket.example.com"}}
	help := f.tokenHelp()
	assert.Contains(t, help,
		"https://bitbucket.example.com/plugins/servlet/access-tokens/manage")
}

func TestForge_validateToken(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		// CurrentUser issues an authenticated GET and reads the identity
		// from the X-AUSERNAME response header; a token that yields a 2xx
		// with that header validates.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			w.Header().Set("X-AUSERNAME", "jcaptain")
			writeJSON(t, w, http.StatusOK, map[string]any{
				"values":     []any{},
				"isLastPage": true,
			})
		}))
		defer srv.Close()

		f := &Forge{Log: silog.Nop(), Options: Options{URL: srv.URL}}
		require.NoError(t, f.validateToken(t.Context(),
			&AuthenticationToken{AccessToken: "tok"}))
	})

	t.Run("Unauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, http.StatusUnauthorized, map[string]any{
				"errors": []map[string]any{
					{"message": "You are not permitted to access this resource."},
				},
			})
		}))
		defer srv.Close()

		f := &Forge{Log: silog.Nop(), Options: Options{URL: srv.URL}}
		err := f.validateToken(t.Context(),
			&AuthenticationToken{AccessToken: "bad"})
		require.Error(t, err)
	})
}

func TestForge_AuthenticationFlow_missingURL(t *testing.T) {
	// Without a configured instance URL there is nothing to authenticate
	// against; the flow fails before prompting, so a nil view is safe here.
	f := &Forge{Log: silog.Nop()}

	_, err := f.AuthenticationFlow(t.Context(), nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no Bitbucket Data Center URL configured")
}

func TestForge_AuthenticationFlow_alreadyAuthenticated(t *testing.T) {
	// When BITBUCKET_SERVER_TOKEN (modeled via Options.Token) is set, the
	// login flow refuses to prompt and returns early; the view is never
	// touched, so a nil view is safe here.
	f := &Forge{
		Log: silog.Nop(),
		Options: Options{
			URL:   "https://bitbucket.example.com",
			Token: "env-token",
		},
	}

	_, err := f.AuthenticationFlow(t.Context(), nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "already authenticated")
}
