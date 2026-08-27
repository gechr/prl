package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	xslices "github.com/gechr/x/slices"
)

const envKeyGitHubToken = "PRL_GITHUB_TOKEN" //nolint:gosec // env var name, not a credential

var tokenEnvKeys = []string{envKeyGitHubToken, "GITHUB_TOKEN", "GH_TOKEN"}

func githubToken() string {
	for _, key := range tokenEnvKeys {
		if token := os.Getenv(key); token != "" {
			return token
		}
	}
	if token, _ := auth.TokenForHost("github.com"); token != "" {
		return token
	}
	return ""
}

func ensureGHAuth() error {
	token := githubToken()
	if token == "" {
		return fmt.Errorf(
			"not authenticated with GitHub (set %s or run 'gh auth login')",
			tokenEnvKeys[0],
		)
	}
	_ = os.Setenv("GH_TOKEN", token)
	return nil
}

// githubErrorDetail returns the error messages GitHub sent that go-gh dropped
// from err. api.HandleHTTPError swaps an item's own message for a generic
// "<resource>.<field> is invalid" summary whenever the item carries a code
// other than "custom", so a search refused because the token may not reach
// `user:<org>` reports only "Search.q is invalid" and loses GitHub's reason.
// The originals survive on HTTPError.Errors.
func githubErrorDetail(err error) []string {
	httpErr, ok := errors.AsType[*api.HTTPError](err)
	if !ok {
		return nil
	}
	detail := make([]string, 0, len(httpErr.Errors))
	for _, item := range httpErr.Errors {
		msg := strings.TrimSpace(item.Message)
		if msg == "" || strings.Contains(httpErr.Message, msg) {
			continue
		}
		detail = append(detail, msg)
	}
	return xslices.Unique(detail)
}

// githubDetailError appends GitHub's dropped messages to the error it wraps.
// Wrapping happens where errors are displayed, leaving the wrapped error itself
// intact so errors.Is and errors.As keep matching what the caller returned.
type githubDetailError struct {
	err    error
	detail []string
}

func (e *githubDetailError) Error() string {
	return strings.Join(append([]string{e.err.Error()}, e.detail...), "\n")
}

func (e *githubDetailError) Unwrap() error { return e.err }

// withGitHubErrorDetail returns err with GitHub's own messages appended when
// go-gh replaced them with its generic summary, and err untouched otherwise.
func withGitHubErrorDetail(err error) error {
	detail := githubErrorDetail(err)
	if len(detail) == 0 {
		return err
	}
	return &githubDetailError{err: err, detail: detail}
}

func newRESTClient(options ...clientOption) (*api.RESTClient, error) {
	opts := api.ClientOptions{}
	for _, o := range options {
		o(&opts)
	}
	opts.Transport = sharedGitHubRateLimiter.wrap(opts.Transport)
	return api.NewRESTClient(opts)
}

func newGraphQLClient(options ...clientOption) (*api.GraphQLClient, error) {
	opts := api.ClientOptions{}
	for _, o := range options {
		o(&opts)
	}
	opts.Transport = sharedGitHubRateLimiter.wrap(opts.Transport)
	return api.NewGraphQLClient(opts)
}

// getCurrentLogin returns the login of the authenticated GitHub user.
func getCurrentLogin(rest *api.RESTClient) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if err := rest.Get("user", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// newActionRunner creates an ActionRunner, initializing a GraphQL client
// only when the CLI flags require one.
func newActionRunner(cli *CLI, rest *api.RESTClient) (*ActionRunner, error) {
	var gql *api.GraphQLClient
	if cli.Approve || cli.ForceMerge || cli.MarkDraft || cli.MarkReady || cli.Merge != nil ||
		cli.Unsubscribe {
		var err error
		gql, err = newGraphQLClient(withDebug(cli.Debug))
		if err != nil {
			return nil, fmt.Errorf("creating GraphQL client: %w", err)
		}
	}
	return NewActionRunner(rest, gql), nil
}
