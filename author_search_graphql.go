package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Keep each candidate page cheap. Only the matches need full PR data.
const compactAuthorPage = `query AuthorCandidates($q:String!,$after:String){
	search(type:ISSUE_ADVANCED,query:$q,first:100,after:$after){
		issueCount pageInfo{hasNextPage endCursor}
		nodes{... on PullRequest{id author{login}}}
	}
}`

// Divide whole seconds into disjoint ranges in API sort order.
func (w searchWindow) partitions(count int, order string) []searchWindow {
	start, end := w.first.Unix(), w.last.Unix()+1
	if start >= end {
		return nil
	}
	n := min(int64(count), end-start)
	windows := make([]searchWindow, n)
	for i := range n {
		first := start + i*(end-start)/n
		last := start + (i+1)*(end-start)/n - 1
		windows[i] = searchWindow{time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()}
	}
	if order == valueDesc {
		slices.Reverse(windows)
	}
	return windows
}

type authorCandidateScan struct {
	gql                        *api.GraphQLClient
	query, author, sort, order string
}

func (s authorCandidateScan) ids(ctx context.Context, w searchWindow, limit int) ([]string, error) {
	query := fmt.Sprintf("(%s) AND %s sort:%s-%s", s.query, w.qualifier(s.sort), s.sort, s.order)
	var after *string
	var loaded int
	var ids []string
	for {
		var resp struct {
			Search struct {
				IssueCount int
				PageInfo   graphQLPageInfo
				Nodes      []struct {
					ID     string
					Author struct{ Login string }
				}
			}
		}
		if err := s.gql.DoWithContext(
			ctx,
			compactAuthorPage,
			map[string]any{"q": query, "after": after},
			&resp,
		); err != nil {
			return nil, err
		}
		page := resp.Search
		loaded += len(page.Nodes)
		for _, n := range page.Nodes {
			if strings.EqualFold(n.Author.Login, s.author) {
				ids = append(ids, n.ID)
				if len(ids) == limit {
					return ids, nil
				}
			}
		}
		if page.IssueCount > maxAuthorCandidates {
			parts := w.partitions(authorWindowHalves, s.order)
			if len(parts) < authorWindowHalves {
				return nil, errors.New("author search exceeds the window limit")
			}
			ids = nil
			for _, part := range parts {
				matches, err := s.ids(ctx, part, limit-len(ids))
				if err != nil {
					return nil, err
				}
				ids = append(ids, matches...)
				if len(ids) == limit {
					break
				}
			}
			return ids, nil
		}
		if !page.PageInfo.HasNextPage {
			if loaded < page.IssueCount {
				return nil, errors.New("incomplete author candidate page")
			}
			return ids, nil
		}
		next := page.PageInfo.EndCursor
		if next == "" || (after != nil && *after == next) {
			return nil, errors.New("author search cursor did not advance")
		}
		after = &next
	}
}

func compactAuthorSearch(
	gql *api.GraphQLClient,
	query, author, sort, order string,
	limit int,
	w searchWindow,
) ([]PullRequest, error) {
	const workers = 4
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scan := authorCandidateScan{gql, query, author, sort, order}
	type result struct {
		ids []string
		err error
	}
	var pending []chan result
	for _, part := range w.partitions(workers, order) {
		done := make(chan result, 1)
		pending = append(pending, done)
		go func() {
			ids, err := scan.ids(ctx, part, limit)
			done <- result{ids, err}
		}()
	}
	var ids []string
	for _, done := range pending {
		r := <-done
		if r.err != nil {
			return nil, r.err
		}
		ids = append(ids, r.ids[:min(len(r.ids), limit-len(ids))]...)
		if len(ids) == limit {
			break
		}
	}
	cancel()
	if len(ids) == 0 {
		return nil, nil
	}
	var resp struct{ Nodes []*graphQLSearchPRNode }
	if err := gql.Do(
		"query AuthorResults($ids:[ID!]!){nodes(ids:$ids){...prFields}} "+prFieldsFragment,
		map[string]any{"ids": ids},
		&resp,
	); err != nil {
		return nil, err
	}
	if len(resp.Nodes) != len(ids) {
		return nil, errors.New("incomplete author results")
	}
	prs := make([]PullRequest, len(ids))
	for i, node := range resp.Nodes {
		if node == nil || node.ID != ids[i] {
			return nil, errors.New("missing author result")
		}
		prs[i] = toPullRequestGraphQL(*node)
	}
	return prs, nil
}
