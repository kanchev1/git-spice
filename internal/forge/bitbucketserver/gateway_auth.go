package bitbucketserver

import (
	"errors"

	"go.abhg.dev/gs/internal/gateway/bitbucketserver"
)

// newGatewayTokenSource adapts a stored authentication token into the
// gateway's [bitbucketserver.TokenSource].
func newGatewayTokenSource(tok *AuthenticationToken) (bitbucketserver.TokenSource, error) {
	if tok == nil {
		return nil, errors.New("nil authentication token")
	}

	return bitbucketserver.StaticTokenSource(bitbucketserver.Token{
		AccessToken: tok.AccessToken,
	}), nil
}
