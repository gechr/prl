package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

// blockedOrgDetail is the shape of explanation GitHub returns when a search
// names an organization the token may not reach.
const blockedOrgDetail = "Access to the requested organization (example-org) is blocked."

// blockedOrgBody is the 422 body GitHub sends for such a search.
const blockedOrgBody = `{
	"message": "Validation Failed",
	"errors": [{
		"resource": "Search",
		"field": "q",
		"code": "invalid",
		"message": "` + blockedOrgDetail + `"
	}]
}`

const searchIssuesURL = "https://api.github.com/search/issues"

// blockedOrgHTTPError is that response as go-gh parses it: the explanation
// survives on Errors but never reaches Message.
func blockedOrgHTTPError() *api.HTTPError {
	return &api.HTTPError{
		StatusCode: http.StatusUnprocessableEntity,
		Message:    "Validation Failed\nSearch.q is invalid",
		RequestURL: &url.URL{Scheme: "https", Host: "api.github.com", Path: "/search/issues"},
		Errors: []api.HTTPErrorItem{{
			Resource: "Search",
			Field:    "q",
			Code:     "invalid",
			Message:  blockedOrgDetail,
		}},
	}
}

// TestGoGHDropsErrorItemMessages pins the upstream behaviour githubErrorDetail
// works around: HandleHTTPError keeps the item's message on Errors but replaces
// it with a generic summary in Message, the only field HTTPError.Error prints.
func TestGoGHDropsErrorItemMessages(t *testing.T) {
	resp := jsonResponse(
		&http.Request{
			Method: http.MethodGet,
			URL:    &url.URL{Scheme: "https", Host: "api.github.com", Path: "/search/issues"},
		},
		http.StatusUnprocessableEntity,
		blockedOrgBody,
	)

	httpErr, ok := errors.AsType[*api.HTTPError](api.HandleHTTPError(resp))
	require.True(t, ok)
	require.Equal(
		t,
		"HTTP 422: Validation Failed ("+searchIssuesURL+")\nSearch.q is invalid",
		httpErr.Error(),
	)

	require.Equal(t, []string{blockedOrgDetail}, githubErrorDetail(httpErr))
	require.Equal(
		t,
		"HTTP 422: Validation Failed ("+searchIssuesURL+")\nSearch.q is invalid\n"+
			blockedOrgDetail,
		withGitHubErrorDetail(httpErr).Error(),
	)
}

func TestGitHubErrorDetailRecoversDroppedMessage(t *testing.T) {
	detail := githubErrorDetail(fmt.Errorf("search failed: %w", blockedOrgHTTPError()))

	require.Equal(t, []string{blockedOrgDetail}, detail)
}

func TestGitHubErrorDetailSkipsMessagesAlreadyShown(t *testing.T) {
	httpErr := &api.HTTPError{
		StatusCode: http.StatusUnprocessableEntity,
		Message:    "Validation Failed\nNo commits between main and topic",
		Errors: []api.HTTPErrorItem{{
			Resource: "PullRequest",
			Field:    "base",
			Code:     "custom",
			Message:  "No commits between main and topic",
		}},
	}

	require.Empty(t, githubErrorDetail(httpErr))
}

func TestGitHubErrorDetailDeduplicatesAndIgnoresEmpty(t *testing.T) {
	httpErr := &api.HTTPError{
		StatusCode: http.StatusUnprocessableEntity,
		Message:    "Validation Failed",
		Errors: []api.HTTPErrorItem{
			{Code: "invalid", Message: "same reason"},
			{Code: "invalid", Message: "  same reason  "},
			{Code: "invalid", Message: "   "},
		},
	}

	require.Equal(t, []string{"same reason"}, githubErrorDetail(httpErr))
}

func TestGitHubErrorDetailIgnoresNonHTTPErrors(t *testing.T) {
	require.Empty(t, githubErrorDetail(nil))
	require.Empty(t, githubErrorDetail(errors.New("boom")))
	require.Empty(t, githubErrorDetail(&api.GraphQLError{
		Errors: []api.GraphQLErrorItem{{Message: "field does not exist"}},
	}))
}

func TestWithGitHubErrorDetailAppendsDetailAndPreservesChain(t *testing.T) {
	httpErr := blockedOrgHTTPError()
	wrapped := withGitHubErrorDetail(fmt.Errorf("search failed: %w", httpErr))

	require.Equal(
		t,
		"search failed: HTTP 422: Validation Failed ("+searchIssuesURL+")\n"+
			"Search.q is invalid\n"+blockedOrgDetail,
		wrapped.Error(),
	)

	var unwrapped *api.HTTPError
	require.ErrorAs(t, wrapped, &unwrapped)
	require.Same(t, httpErr, unwrapped)
}

func TestWithGitHubErrorDetailReturnsErrorUnchangedWithoutDetail(t *testing.T) {
	err := errors.New("boom")

	require.Same(t, err, withGitHubErrorDetail(err))
	require.NoError(t, withGitHubErrorDetail(nil))
}
