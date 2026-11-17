package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func unapproveTestRunner(t *testing.T, transport roundTripFunc) *ActionRunner {
	t.Helper()
	rest, err := api.NewRESTClient(api.ClientOptions{
		AuthToken: "test", Host: "github.com", Transport: transport,
	})
	require.NoError(t, err)
	return NewActionRunner(rest, nil)
}

func TestUnapproveOnlyDismissesOwnApprovalsAcrossPages(t *testing.T) {
	var dismissed []string
	var pages []string
	const message = "Rechecking the revised implementation."
	actions := unapproveTestRunner(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/user":
			return jsonResponse(req, http.StatusOK, `{"login":"viewer"}`), nil
		case req.Method == http.MethodGet:
			require.Equal(t, "/repos/owner/repo/pulls/42/reviews", req.URL.Path)
			require.Equal(t, "100", req.URL.Query().Get("per_page"))
			page := req.URL.Query().Get("page")
			pages = append(pages, page)
			if page == "1" {
				reviews := []string{`{"id":11,"state":"APPROVED","user":{"login":"VIEWER"}}`}
				for i := range 99 {
					reviews = append(
						reviews,
						fmt.Sprintf(
							`{"id":%d,"state":"APPROVED","user":{"login":"someone-else"}}`,
							i+100,
						),
					)
				}
				return jsonResponse(req, http.StatusOK, "["+strings.Join(reviews, ",")+"]"), nil
			}
			require.Equal(t, "2", page)
			return jsonResponse(req, http.StatusOK, `[
				{"id":12,"state":"APPROVED","user":{"login":"viewer"}},
				{"id":13,"state":"COMMENTED","user":{"login":"viewer"}},
				{"id":14,"state":"CHANGES_REQUESTED","user":{"login":"viewer"}},
				{"id":15,"state":"DISMISSED","user":{"login":"viewer"}},
				{"id":16,"state":"PENDING","user":{"login":"viewer"}},
				{"id":17,"state":"APPROVED","user":null}
			]`), nil
		default:
			require.Equal(t, http.MethodPut, req.Method)
			var body map[string]string
			require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
			require.Equal(t, message, body["message"])
			dismissed = append(dismissed, req.URL.Path)
			return jsonResponse(req, http.StatusOK, `{}`), nil
		}
	})
	require.NoError(t, actions.unapprovePR(approveTestPR(), message))
	require.Equal(t, []string{"1", "2"}, pages)
	require.Equal(t, []string{
		"/repos/owner/repo/pulls/42/reviews/11/dismissals",
		"/repos/owner/repo/pulls/42/reviews/12/dismissals",
	}, dismissed)
}

func TestUnapproveFailsWithoutWriting(t *testing.T) {
	for _, scenario := range []string{"blank reason", "missing login", "login failure", "reviews failure", "later page failure", "no own approval"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			actions := unapproveTestRunner(t, func(req *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, http.MethodGet, req.Method)
				if req.URL.Path == "/user" {
					switch scenario {
					case "missing login":
						return jsonResponse(req, http.StatusOK, `{}`), nil
					case "login failure":
						return jsonResponse(
							req,
							http.StatusUnauthorized,
							`{"message":"unauthorized"}`,
						), nil
					default:
						return jsonResponse(req, http.StatusOK, `{"login":"viewer"}`), nil
					}
				}
				if scenario == "reviews failure" || req.URL.Query().Get("page") == "2" {
					return jsonResponse(req, http.StatusForbidden, `{"message":"forbidden"}`), nil
				}
				if scenario == "later page failure" {
					review := `{"id":11,"state":"APPROVED","user":{"login":"viewer"}}`
					return jsonResponse(
						req,
						http.StatusOK,
						"["+strings.Repeat(review+",", 99)+review+"]",
					), nil
				}
				return jsonResponse(
					req,
					http.StatusOK,
					`[{"id":11,"state":"APPROVED","user":{"login":"someone-else"}}]`,
				), nil
			})
			message := defaultUnapproveMessage
			if scenario == "blank reason" {
				message = " \n "
			}
			err := actions.unapprovePR(approveTestPR(), message)
			if scenario == "no own approval" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario == "blank reason" {
				require.Zero(t, requests)
			}
		})
	}
}
