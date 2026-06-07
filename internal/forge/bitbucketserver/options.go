// Package bitbucketserver provides a wrapper around the Bitbucket Data
// Center/Server REST 1.0 API, compliant with the [forge.Forge] interface.
//
// Unlike Bitbucket Cloud, it has no default host; the instance URL must
// be configured.
package bitbucketserver

// Options defines command line options for the Bitbucket Data Center Forge.
// These are all hidden in the CLI,
// and are expected to be set only via environment variables or git config.
type Options struct {
	// URL is the base URL for the Bitbucket Data Center instance,
	// e.g. "https://bitbucket.example.com".
	URL string `name:"bitbucket-server-url" hidden:"" config:"forge.bitbucket-server.url" env:"BITBUCKET_SERVER_URL" help:"Base URL for Bitbucket Data Center web requests"`

	// APIURL is the URL for the Bitbucket Data Center REST API.
	// If unset, it is derived from URL. Override for testing or non-standard deployments.
	APIURL string `name:"bitbucket-server-api-url" hidden:"" config:"forge.bitbucket-server.apiURL" env:"BITBUCKET_SERVER_API_URL" help:"Base URL for Bitbucket Data Center API requests"`

	// Token is a fixed HTTP access token (PAT) used to authenticate.
	// This may be used to skip the login flow.
	Token string `name:"bitbucket-server-token" hidden:"" env:"BITBUCKET_SERVER_TOKEN" help:"Bitbucket Data Center HTTP access token"`
}
