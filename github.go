package main

import (
	"fmt"

	"github.com/cli/go-gh/v2/pkg/api"
)

func newRESTClient(options ...clientOption) (*api.RESTClient, error) {
	opts := api.ClientOptions{}
	for _, o := range options {
		o(&opts)
	}
	return api.NewRESTClient(opts)
}

func newGraphQLClient(options ...clientOption) (*api.GraphQLClient, error) {
	opts := api.ClientOptions{}
	for _, o := range options {
		o(&opts)
	}
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

// requireOwnAuthor returns an error if any value in authors is not the
// authenticated user (i.e. not valueAtMe and not their actual GitHub login).
// The API call to resolve the login is skipped when all values are valueAtMe.
func requireOwnAuthor(rest *api.RESTClient, authors []string) error {
	allMe := true
	for _, a := range authors {
		if a != valueAtMe {
			allMe = false
			break
		}
	}
	if allMe {
		return nil
	}

	login, err := getCurrentLogin(rest)
	if err != nil {
		return fmt.Errorf("resolving current user: %w", err)
	}

	for _, a := range authors {
		if a != valueAtMe && a != login {
			return fmt.Errorf("--send is only allowed for your own PRs (got author %q)", a)
		}
	}
	return nil
}

// newActionRunner creates an ActionRunner, initializing a GraphQL client
// only when the CLI flags require one.
func newActionRunner(cli *CLI, rest *api.RESTClient) (*ActionRunner, error) {
	var gql *api.GraphQLClient
	if cli.ForceMerge || cli.MarkDraft || cli.MarkReady || cli.Merge != nil {
		var err error
		gql, err = newGraphQLClient(withDebug(cli.Debug))
		if err != nil {
			return nil, fmt.Errorf("creating GraphQL client: %w", err)
		}
	}
	return NewActionRunner(rest, gql), nil
}
