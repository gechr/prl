package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	"github.com/gechr/forge"
)

const (
	// prRefBatchSize caps how many pull requests one GraphQL request fetches.
	prRefBatchSize = 50
	// prRefVars is the number of GraphQL variables per pull request
	// (owner, repo and number).
	prRefVars = 3
)

// prRef identifies a single pull request by repository and number.
type prRef struct {
	Owner  string
	Repo   string
	Number int
}

func (r prRef) String() string {
	return r.Owner + "/" + r.Repo + "#" + strconv.Itoa(r.Number)
}

// URL returns the GitHub web URL of the pull request.
func (r prRef) URL() string {
	return "https://" + forge.HostGitHub + "/" + r.Owner + "/" + r.Repo + "/pull/" +
		strconv.Itoa(r.Number)
}

// parsePRRef parses a pull request URL (https://github.com/owner/repo/pull/1)
// or shorthand (owner/repo#1, owner/repo/pull/1). It reports false when value
// does not name a pull request.
func parsePRRef(value string) (prRef, bool, error) {
	ref, err := forge.Parse(value)
	if err != nil || ref.PullRequest == "" || ref.Owner == "" || ref.Name == "" {
		return prRef{}, false, nil //nolint:nilerr // not a pull request reference
	}
	if ref.Host != "" && ref.Host != forge.HostGitHub {
		return prRef{}, false, fmt.Errorf("pull request URL must refer to GitHub: %s", value)
	}
	number, err := strconv.Atoi(ref.PullRequest)
	if err != nil || number <= 0 {
		return prRef{}, false, fmt.Errorf("invalid pull request number: %s", value)
	}
	return prRef{Owner: ref.Owner, Repo: ref.Name, Number: number}, true, nil
}

// parsePRRefs returns the pull requests named by the positional arguments.
// Arguments may hold several references separated by whitespace or commas,
// so a pasted list works whether or not it is quoted. It returns nil when
// none of the arguments is a pull request reference, and an error when pull
// request references are mixed with free-text search terms.
func parsePRRefs(args []string) ([]prRef, error) {
	var refs []prRef
	var terms []string
	seen := make(map[prRef]bool)
	for _, arg := range args {
		for field := range strings.FieldsFuncSeq(arg, isPRRefSeparator) {
			ref, ok, err := parsePRRef(field)
			if err != nil {
				return nil, err
			}
			if !ok {
				terms = append(terms, field)
				continue
			}
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	if len(terms) > 0 {
		return nil, fmt.Errorf(
			"cannot mix pull request URLs with search terms: %s",
			strings.Join(terms, " "),
		)
	}
	return refs, nil
}

func isPRRefSeparator(r rune) bool {
	return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// fetchPRsByRef fetches the given pull requests directly, bypassing search.
func fetchPRsByRef(gql *api.GraphQLClient, refs []prRef) ([]PullRequest, error) {
	prs := make([]PullRequest, 0, len(refs))
	for start := 0; start < len(refs); start += prRefBatchSize {
		batch := refs[start:min(start+prRefBatchSize, len(refs))]
		fetched, err := fetchPRRefBatch(gql, batch)
		if err != nil {
			return nil, err
		}
		prs = append(prs, fetched...)
	}
	clog.Debug().Int("results", len(prs)).Msg("Fetched pull requests by reference")
	return prs, nil
}

func fetchPRRefBatch(gql *api.GraphQLClient, refs []prRef) ([]PullRequest, error) {
	var params, fields strings.Builder
	vars := make(map[string]any, len(refs)*prRefVars)
	for i, ref := range refs {
		fmt.Fprintf(&params, "$o%d: String!, $r%d: String!, $n%d: Int!, ", i, i, i)
		fmt.Fprintf(
			&fields,
			"pr%d: repository(owner: $o%d, name: $r%d) { pullRequest(number: $n%d) { ...prFields } }\n",
			i,
			i,
			i,
			i,
		)
		vars["o"+strconv.Itoa(i)] = ref.Owner
		vars["r"+strconv.Itoa(i)] = ref.Repo
		vars["n"+strconv.Itoa(i)] = ref.Number
	}
	query := "query PullRequestsByRef(" + strings.TrimSuffix(params.String(), ", ") + ") {\n" +
		fields.String() + "}\n" + prFieldsFragment

	var resp map[string]*struct {
		PullRequest *graphQLSearchPRNode `json:"pullRequest"`
	}
	if err := gql.Do(query, vars, &resp); err != nil {
		return nil, fmt.Errorf("fetching pull requests: %w", err)
	}

	prs := make([]PullRequest, 0, len(refs))
	for i, ref := range refs {
		repo := resp["pr"+strconv.Itoa(i)]
		if repo == nil || repo.PullRequest == nil {
			return nil, fmt.Errorf("pull request not found: %s", ref)
		}
		prs = append(prs, toPullRequestGraphQL(*repo.PullRequest))
	}
	return prs, nil
}

// prFieldsFragment selects the same fields as the GraphQL search query so
// fetched pull requests go through the same conversion and enrichment.
const prFieldsFragment = `fragment prFields on PullRequest {
	id
	number
	title
	url
	state
	isDraft
	createdAt
	updatedAt
	mergedAt
	headRefOid
	headRefName
	baseRefName
	headRepository { name nameWithOwner }
	stack { id baseRefName }
	stackEntry { position }
	autoMergeRequest { enabledAt }
	author { login }
	repository { name nameWithOwner }
	labels(first: 100) { nodes { name } }
}`
