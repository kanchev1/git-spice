package bitbucketserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
	"go.abhg.dev/gs/internal/secret"
	"go.abhg.dev/gs/internal/ui"
)

// AuthenticationToken defines the token returned
// by the Bitbucket Data Center forge.
type AuthenticationToken struct {
	forge.AuthenticationToken

	// AccessToken is the Bitbucket Data Center HTTP access token (PAT).
	AccessToken string `json:"access_token,omitempty"`
}

var _ forge.AuthenticationToken = (*AuthenticationToken)(nil)

// AuthenticationFlow prompts the user to authenticate
// with Bitbucket Data Center using an HTTP access token (PAT).
//
// It rejects the request if the instance URL is not configured,
// or if the user is already authenticated
// with a BITBUCKET_SERVER_TOKEN environment variable.
func (f *Forge) AuthenticationFlow(
	ctx context.Context,
	view ui.View,
) (forge.AuthenticationToken, error) {
	log := f.logger()

	if f.URL() == "" {
		return nil, errors.New(
			"no Bitbucket Data Center URL configured: " +
				"set spice.forge.bitbucket-server.url or BITBUCKET_SERVER_URL",
		)
	}

	if f.Options.Token != "" {
		log.Error("Already authenticated with BITBUCKET_SERVER_TOKEN.")
		log.Error("Unset BITBUCKET_SERVER_TOKEN to login with a different method.")
		return nil, errors.New("already authenticated")
	}

	token, err := promptRequired(view,
		"Enter HTTP access token",
		f.tokenHelp(),
		"HTTP access token is required",
	)
	if err != nil {
		return nil, fmt.Errorf("prompt for HTTP access token: %w", err)
	}

	tok := &AuthenticationToken{AccessToken: token}

	if err := f.validateToken(ctx, tok); err != nil {
		log.Error("Could not validate the HTTP access token.")
		log.Error("Ensure the token is valid and has Repository Write scope.")
		return nil, fmt.Errorf("validate token: %w", err)
	}

	return tok, nil
}

// tokenHelp builds help text pointing at the instance's access-token page.
func (f *Forge) tokenHelp() string {
	host := f.URL()
	if u, err := url.Parse(f.URL()); err == nil && u.Host != "" {
		host = u.Scheme + "://" + u.Host
	}

	return "Create an HTTP access token with Repository Write scope at:\n" +
		host + "/plugins/servlet/access-tokens/manage"
}

// validateToken verifies the access token via the gateway's CurrentUser endpoint.
func (f *Forge) validateToken(ctx context.Context, tok *AuthenticationToken) error {
	tokenSource, err := newGatewayTokenSource(tok)
	if err != nil {
		return err
	}

	client, err := bitbucketserver.NewClient(tokenSource, &bitbucketserver.ClientOptions{
		BaseURL: f.APIURL(),
	})
	if err != nil {
		return fmt.Errorf("create Bitbucket Data Center client: %w", err)
	}

	if _, _, err := client.CurrentUser(ctx); err != nil {
		return err
	}
	return nil
}

func promptRequired(view ui.View, title, description, errMsg string) (string, error) {
	var value string
	err := ui.Run(view, ui.NewInput().
		WithTitle(title).
		WithDescription(description).
		WithValidate(requiredValidator(errMsg)).
		WithValue(&value),
	)
	return value, err
}

func requiredValidator(errMsg string) func(string) error {
	return func(input string) error {
		if strings.TrimSpace(input) == "" {
			return errors.New(errMsg)
		}
		return nil
	}
}

// SaveAuthenticationToken saves the given authentication token to the stash.
func (f *Forge) SaveAuthenticationToken(
	stash secret.Stash,
	t forge.AuthenticationToken,
) error {
	bst := t.(*AuthenticationToken)

	// If the user has set BITBUCKET_SERVER_TOKEN, don't save it to the stash.
	if f.Options.Token != "" && f.Options.Token == bst.AccessToken {
		return nil
	}

	data, err := json.Marshal(bst)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	return stash.SaveSecret(f.URL(), "token", string(data))
}

// LoadAuthenticationToken loads the authentication token from the stash.
// Priority order:
//  1. Environment variable (BITBUCKET_SERVER_TOKEN)
//  2. Stored token in secret stash
func (f *Forge) LoadAuthenticationToken(stash secret.Stash) (forge.AuthenticationToken, error) {
	// Environment variable takes highest precedence.
	if f.Options.Token != "" {
		return &AuthenticationToken{AccessToken: f.Options.Token}, nil
	}

	token, err := f.loadStoredToken(stash)
	if err != nil {
		return nil, fmt.Errorf("load stored token: %w", err)
	}

	return token, nil
}

func (f *Forge) loadStoredToken(stash secret.Stash) (*AuthenticationToken, error) {
	data, err := stash.LoadSecret(f.URL(), "token")
	if err != nil {
		return nil, err
	}

	var token AuthenticationToken
	if err := json.Unmarshal([]byte(data), &token); err != nil {
		return nil, err
	}
	return &token, nil
}

// ClearAuthenticationToken removes the authentication token from the stash.
func (f *Forge) ClearAuthenticationToken(stash secret.Stash) error {
	return stash.DeleteSecret(f.URL(), "token")
}
