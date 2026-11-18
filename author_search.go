package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

var literalAuthor = regexp.MustCompile(`^author:([A-Za-z0-9_-]+)$`)

func isUnsearchableUser(err error) bool {
	httpErr, ok := errors.AsType[*api.HTTPError](err)
	if !ok || httpErr.StatusCode != http.StatusUnprocessableEntity {
		return false
	}
	for _, detail := range httpErr.Errors {
		if detail.Resource == "Search" && detail.Field == "q" &&
			strings.Contains(detail.Message, "listed users cannot be searched") {
			return true
		}
	}
	return false
}

const (
	authorWindowHalves  = 2
	maxAuthorCandidates = 1000
)

type searchWindow struct {
	first, last time.Time // Inclusive, at GitHub's one-second precision.
}

func parseSearchTimestamp(value string) (time.Time, bool) {
	for _, layout := range []string{time.DateOnly, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func authorSearchWindow(qualifier, sortField string, now time.Time) (searchWindow, bool) {
	start, end, isRange := strings.Cut(qualifier, "..")
	lower := strings.TrimLeft(start, ">=")
	first, ok := parseSearchTimestamp(lower)
	if !ok {
		return searchWindow{}, false
	}
	last := now.UTC().Truncate(time.Second)
	// A PR created in an old window can still have been updated today.
	if sortField != valueCreated {
		return searchWindow{first: first, last: last}, true
	}
	if !isRange && !strings.HasPrefix(start, ">") {
		end = lower
	}
	if upper, ok := parseSearchTimestamp(end); ok {
		if len(end) == len(time.DateOnly) {
			upper = upper.AddDate(0, 0, 1).Add(-time.Second)
		}
		if upper.Before(last) {
			last = upper
		}
	}
	return searchWindow{first: first, last: last}, true
}

func (w searchWindow) qualifier(field string) string {
	return field + ":" + w.first.Format(time.RFC3339) + ".." + w.last.Format(time.RFC3339)
}

// A 422 can refer to any account in the query. Only offer a private-profile
// fallback when the login resolves and GitHub rejects an author-only search.
func fallbackAuthorSearch(
	rest *api.RESTClient,
	getGQL func() (*api.GraphQLClient, error),
	params *SearchParams,
	limit int,
	original error,
) ([]PullRequest, error) {
	if !isUnsearchableUser(original) {
		return nil, original
	}
	var author string
	var scope bool
	var terms []string
	for _, term := range params.queryTerms {
		if term.grouped && term.key == groupAuthor {
			m := literalAuthor.FindStringSubmatch(term.query)
			if author != "" || m == nil {
				return nil, original
			}
			author = m[1]
			continue
		}
		if term.grouped && (term.key == groupRepo || term.key == groupOwner) &&
			!strings.HasPrefix(term.query, "-") {
			scope = true
		}
		terms = append(terms, term.query)
	}
	if author == "" {
		return nil, original
	}
	var user struct{ Login string }
	if err := rest.Get("users/"+author, &user); err != nil {
		if httpErr, ok := errors.AsType[*api.HTTPError](
			err,
		); ok &&
			httpErr.StatusCode == http.StatusNotFound {
			return nil, original
		}
		return nil, fmt.Errorf("checking author: %w", err)
	}
	if !strings.EqualFold(user.Login, author) {
		return nil, original
	}
	probe := "search/issues?advanced_search=true&per_page=1&q=" + url.QueryEscape(
		"is:pr author:"+author,
	)
	err := rest.Get(probe, &searchResponse{})
	if err == nil {
		return nil, original
	}
	if !isUnsearchableUser(err) {
		return nil, fmt.Errorf("checking author search: %w", err)
	}

	window, bounded := authorSearchWindow(params.createdQualifier, params.Sort, time.Now())
	if !scope || !bounded {
		var needed []string
		if !bounded {
			needed = append(needed, "a time window such as `--created=7d`")
		}
		if !scope {
			needed = append(needed, "an owner (-O) or repository (-R)")
		}
		return nil, fmt.Errorf(
			"GitHub rejected the author filter for %q (the profile may be private) - try again with %s",
			author,
			strings.Join(needed, " and "),
		)
	}
	query := strings.Join(terms, " ")
	if getGQL != nil && limit > 0 && limit <= maxPerPage {
		gql, err := getGQL()
		if err == nil {
			var prs []PullRequest
			prs, err = compactAuthorSearch(
				gql,
				query,
				author,
				params.Sort,
				params.Order,
				limit,
				window,
			)
			if err == nil && len(prs) > 0 {
				return prs, nil
			}
		}
		clog.Debug().Err(err).Msg("Using REST for date-bounded author search")
	}
	return scanAuthorSearch(
		rest,
		query,
		author,
		params.Sort,
		params.Order,
		limit,
		window,
	)
}

// Search disjoint windows in API sort order. A zero limit requests an exact count.
func scanAuthorSearch(
	rest *api.RESTClient,
	query, author, sort, order string,
	limit int,
	window searchWindow,
) ([]PullRequest, error) {
	var matches []PullRequest
	stack := []searchWindow{window}
	for len(stack) > 0 {
		w := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if w.first.After(w.last) {
			continue
		}
		scoped := query
		if w != window {
			scoped = "(" + query + ") AND " + w.qualifier(sort)
		}
		rows, split, err := scanAuthorWindow(
			rest,
			scoped,
			author,
			sort,
			order,
			max(0, limit-len(matches)),
		)
		if err != nil {
			return nil, err
		}
		if !split {
			matches = append(matches, rows...)
			if limit > 0 && len(matches) >= limit {
				break
			}
			continue
		}
		if w.first.Equal(w.last) {
			return nil, errors.New(
				"more than 1,000 candidates share one timestamp. Specify a repository",
			)
		}
		parts := w.partitions(authorWindowHalves, order)
		stack = append(stack, parts[1], parts[0])
	}
	return matches, nil
}

func scanAuthorWindow(
	rest *api.RESTClient,
	query, author, sort, order string,
	limit int,
) ([]PullRequest, bool, error) {
	values := url.Values{
		"q":               {query},
		"advanced_search": {"true"},
		"sort":            {sort},
		"order":           {order},
		"per_page":        {strconv.Itoa(maxPerPage)},
	}
	clog.Debug().Msg("Filtering date-bounded search results by author")
	var matches []PullRequest
	// Fetch the first page alone. It often satisfies small queries and tells us
	// how many more pages GitHub can return. Consume prefetched pages in order
	// so the API sort and limit still select the same PRs.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const maxInFlight = 3
	pending := make(map[int]<-chan authorSearchPage)
	lastPage, nextRequest := 1, 1
	for page := 1; page <= lastPage; page++ {
		for nextRequest <= lastPage && len(pending) < maxInFlight {
			values.Set("page", strconv.Itoa(nextRequest))
			pending[nextRequest] = fetchAuthorSearchPage(ctx, rest, values.Encode())
			nextRequest++
		}
		result := <-pending[page]
		delete(pending, page)
		if result.err != nil {
			return nil, false, fmt.Errorf("date-bounded search: %w", result.err)
		}
		resp := result.response
		if resp.Incomplete {
			return nil, false, errors.New(
				"GitHub returned incomplete search results. Narrow --created or specify a repository",
			)
		}
		for _, item := range resp.Items {
			if strings.EqualFold(item.User.Login, author) {
				matches = append(matches, toPullRequest(item))
				if limit > 0 && len(matches) == limit {
					return matches, false, nil
				}
			}
		}
		if resp.TotalCount > maxAuthorCandidates {
			return nil, true, nil
		}
		if resp.TotalCount <= page*maxPerPage || len(resp.Items) < maxPerPage {
			break
		}
		lastPage = (resp.TotalCount + maxPerPage - 1) / maxPerPage
	}
	return matches, false, nil
}

type authorSearchPage struct {
	response searchResponse
	err      error
}

func fetchAuthorSearchPage(
	ctx context.Context,
	rest *api.RESTClient,
	query string,
) <-chan authorSearchPage {
	done := make(chan authorSearchPage, 1)
	go func() {
		var page authorSearchPage
		get := func() error {
			return rest.DoWithContext(
				ctx,
				http.MethodGet,
				"search/issues?"+query,
				nil,
				&page.response,
			)
		}
		page.err = get()
		if delay, ok := authorSearchRetryDelay(page.err); ok {
			clog.Debug().Str("delay", delay.String()).Msg("Waiting for GitHub search quota")
			select {
			case <-ctx.Done():
				page.err = ctx.Err()
			case <-time.After(delay):
				page.err = get()
			}
		}
		done <- page
	}()
	return done
}

func authorSearchRetryDelay(err error) (time.Duration, bool) {
	e, ok := errors.AsType[*api.HTTPError](err)
	if !ok || (e.StatusCode != http.StatusTooManyRequests && e.StatusCode != http.StatusForbidden) {
		return 0, false
	}
	if d, ok := parseRetryAfter(e.Headers.Get(headerRetryAfter)); ok {
		return d, true
	}
	if d, ok := parseRateLimitReset(e.Headers); ok {
		return d, true
	}
	if e.StatusCode == http.StatusTooManyRequests ||
		strings.Contains(strings.ToLower(e.Message), "rate limit") {
		return time.Minute, true
	}
	return 0, false
}
