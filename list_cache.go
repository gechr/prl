package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
)

const (
	listResultCacheVersion   = 2
	listResultCacheDirPerm   = 0o755
	listResultCacheFilePerm  = 0o600
	tuiListResultCacheMaxAge = 15 * time.Minute
)

type listResultCacheFile struct {
	Version int                 `json:"version"`
	Key     string              `json:"key"`
	SavedAt time.Time           `json:"savedAt"`
	PRs     []cachedPullRequest `json:"prs"`
}

type cachedPullRequest struct {
	Automerge            bool       `json:"automerge"`
	AutomergeLoaded      bool       `json:"automergeLoaded"`
	Author               Author     `json:"author"`
	CreatedAt            time.Time  `json:"createdAt"`
	HeadSHA              string     `json:"headSha"`
	IsDraft              bool       `json:"draft"`
	Labels               []Label    `json:"labels"`
	MergeStatus          int        `json:"mergeStatus"`
	NodeID               string     `json:"nodeId"`
	Number               int        `json:"number"`
	Repository           Repository `json:"repository"`
	ReviewDecision       string     `json:"reviewDecision"`
	ReviewDecisionLoaded bool       `json:"reviewDecisionLoaded"`
	State                string     `json:"state"`
	Title                string     `json:"title"`
	TitleRaw             string     `json:"titleRaw"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	URL                  string     `json:"url"`
	ViewerApprovalLoaded bool       `json:"viewerApprovalLoaded"`
	ViewerApproved       bool       `json:"viewerApproved"`
	ViewerIsAuthor       bool       `json:"viewerIsAuthor"`
}

type listResultCacheIdentity struct {
	Version int                           `json:"version"`
	Auth    string                        `json:"auth"`
	Search  listResultCacheSearchIdentity `json:"search"`
	Local   listResultCacheLocalIdentity  `json:"local"`
}

type listResultCacheSearchIdentity struct {
	Query      string `json:"query"`
	Sort       string `json:"sort"`
	Order      string `json:"order"`
	PerPage    int    `json:"perPage"`
	TotalLimit int    `json:"totalLimit"`
}

type listResultCacheLocalIdentity struct {
	CI          string   `json:"ci"`
	ClosedBy    []string `json:"closedBy"`
	Drift       string   `json:"drift"`
	Draft       string   `json:"draft"`
	MergedBy    []string `json:"mergedBy"`
	NoBot       bool     `json:"noBot"`
	Output      string   `json:"output"`
	Quick       bool     `json:"quick"`
	Review      string   `json:"review"`
	State       string   `json:"state"`
	ViewerNeeds bool     `json:"viewerNeeds"`
}

func cachedPR(pr PullRequest) cachedPullRequest {
	return cachedPullRequest{
		Automerge:            pr.Automerge,
		AutomergeLoaded:      pr.automergeLoaded,
		Author:               pr.Author,
		CreatedAt:            pr.CreatedAt,
		HeadSHA:              pr.HeadSHA,
		IsDraft:              pr.IsDraft,
		Labels:               append([]Label(nil), pr.Labels...),
		MergeStatus:          int(pr.MergeStatus),
		NodeID:               pr.NodeID,
		Number:               pr.Number,
		Repository:           pr.Repository,
		ReviewDecision:       pr.ReviewDecision,
		ReviewDecisionLoaded: pr.reviewDecisionLoaded,
		State:                pr.State,
		Title:                pr.Title,
		TitleRaw:             pr.TitleRaw,
		UpdatedAt:            pr.UpdatedAt,
		URL:                  pr.URL,
		ViewerApprovalLoaded: pr.viewerApprovalLoaded,
		ViewerApproved:       pr.viewerApproved,
		ViewerIsAuthor:       pr.viewerIsAuthor,
	}
}

func (pr cachedPullRequest) pullRequest() PullRequest {
	return PullRequest{
		Automerge:            pr.Automerge,
		Author:               pr.Author,
		CreatedAt:            pr.CreatedAt,
		HeadSHA:              pr.HeadSHA,
		IsDraft:              pr.IsDraft,
		Labels:               append([]Label(nil), pr.Labels...),
		MergeStatus:          MergeStatus(pr.MergeStatus),
		NodeID:               pr.NodeID,
		Number:               pr.Number,
		Repository:           pr.Repository,
		ReviewDecision:       pr.ReviewDecision,
		State:                pr.State,
		Title:                pr.Title,
		TitleRaw:             pr.TitleRaw,
		UpdatedAt:            pr.UpdatedAt,
		URL:                  pr.URL,
		automergeLoaded:      pr.AutomergeLoaded,
		reviewDecisionLoaded: pr.ReviewDecisionLoaded,
		viewerApprovalLoaded: pr.ViewerApprovalLoaded,
		viewerApproved:       pr.ViewerApproved,
		viewerIsAuthor:       pr.ViewerIsAuthor,
	}
}

func saveListResultCache(cli *CLI, params *SearchParams, prs []PullRequest) error {
	path, key, err := listResultCachePath(cli, params)
	if err != nil {
		return err
	}
	if err = xos.EnsureDir(filepath.Dir(path), listResultCacheDirPerm); err != nil {
		return fmt.Errorf("creating list cache directory: %w", err)
	}
	cached := make([]cachedPullRequest, len(prs))
	for i, pr := range prs {
		cached[i] = cachedPR(pr)
	}
	data, err := json.Marshal(listResultCacheFile{
		Version: listResultCacheVersion,
		Key:     key,
		SavedAt: time.Now(),
		PRs:     cached,
	})
	if err != nil {
		return fmt.Errorf("marshalling list cache: %w", err)
	}
	return xos.AtomicWrite(path, data, listResultCacheFilePerm)
}

func loadListResultCache(
	cli *CLI,
	params *SearchParams,
	maxAge time.Duration,
) ([]PullRequest, bool, error) {
	path, key, err := listResultCachePath(cli, params)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading list cache: %w", err)
	}
	var cache listResultCacheFile
	if err = json.Unmarshal(data, &cache); err != nil {
		return nil, false, fmt.Errorf("parsing list cache: %w", err)
	}
	if cache.Version != listResultCacheVersion || cache.Key != key {
		return nil, false, nil
	}
	if maxAge > 0 && (cache.SavedAt.IsZero() || time.Since(cache.SavedAt) > maxAge) {
		return nil, false, nil
	}
	prs := make([]PullRequest, len(cache.PRs))
	for i, pr := range cache.PRs {
		prs[i] = pr.pullRequest()
	}
	return prs, true, nil
}

func listResultCachePath(cli *CLI, params *SearchParams) (string, string, error) {
	key, err := listResultCacheKey(cli, params)
	if err != nil {
		return "", "", err
	}
	dir, err := shell.CacheDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "prl", "lists", key+".json"), key, nil
}

func listResultCacheKey(cli *CLI, params *SearchParams) (string, error) {
	if cli == nil || params == nil {
		return "", fmt.Errorf("list cache requires cli and search params")
	}
	draft := ""
	if cli.Draft != nil {
		draft = fmt.Sprintf("%t", *cli.Draft)
	}
	output := ""
	if cli.Output != nil {
		output = *cli.Output
	}
	identity := listResultCacheIdentity{
		Version: listResultCacheVersion,
		Auth:    hashString(githubToken()),
		Search: listResultCacheSearchIdentity{
			Query:      params.Query,
			Sort:       params.Sort,
			Order:      params.Order,
			PerPage:    params.PerPage,
			TotalLimit: params.TotalLimit,
		},
		Local: listResultCacheLocalIdentity{
			CI:          cli.CI,
			ClosedBy:    append([]string(nil), cli.ClosedBy.Values...),
			Drift:       cli.Drift,
			Draft:       draft,
			MergedBy:    append([]string(nil), cli.MergedBy.Values...),
			NoBot:       cli.NoBot,
			Output:      output,
			Quick:       cli.Quick,
			Review:      cli.Review,
			State:       cli.State,
			ViewerNeeds: cli.ReviewSelfRequired(),
		},
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("marshalling list cache key: %w", err)
	}
	return hashString(string(data)), nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
