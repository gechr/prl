package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/primer/table"
)

// PRStack is GitHub's native stack identity; position increases towards the top.
type PRStack struct {
	ID          string `json:"id"`
	BaseRefName string `json:"baseRefName"`
}

// hydrateStackMetadata supplies the cheap branch/stack fields REST search lacks.
// GraphQL search already returns these, so the normal list path needs no extra call.
func hydrateStackMetadata(gql *api.GraphQLClient, prs []PullRequest) error {
	var missing []int
	for i, pr := range prs {
		if pr.HeadRefName == "" && pr.NodeID != "" {
			missing = append(missing, i)
		}
	}
	for start := 0; start < len(missing); start += hydrateChunkSize {
		chunk := missing[start:min(start+hydrateChunkSize, len(missing))]
		ids := make([]string, len(chunk))
		for i, idx := range chunk {
			ids[i] = prs[idx].NodeID
		}
		var result struct {
			Nodes []graphQLSearchPRNode `json:"nodes"`
		}
		if err := gql.Do(`query StackMetadata($ids: [ID!]!) {
			nodes(ids: $ids) { ... on PullRequest {
				id headRefName baseRefName headRepository { name nameWithOwner }
				stack { id baseRefName } stackEntry { position }
			} }
		}`, map[string]any{"ids": ids}, &result); err != nil {
			return fmt.Errorf("querying stack metadata: %w", err)
		}
		byID := make(map[string]graphQLSearchPRNode, len(result.Nodes))
		for _, node := range result.Nodes {
			byID[node.ID] = node
		}
		for _, idx := range chunk {
			if node, ok := byID[prs[idx].NodeID]; ok {
				prs[idx].HeadRefName = node.HeadRefName
				prs[idx].BaseRefName = node.BaseRefName
				if node.HeadRepository != nil {
					prs[idx].HeadRepoNWO = node.HeadRepository.NameWithOwner
				}
				prs[idx].Stack = node.Stack
				if node.StackEntry != nil {
					prs[idx].StackPosition = node.StackEntry.Position
				}
			}
		}
	}
	return nil
}

// groupStackRows sorts representatives only, then expands each stack top-first.
// Child sort values never contribute to a stack's position in the list.
func groupStackRows(rows []TableRow, columns []Column, sortColumn string, asc bool) []TableRow {
	groups := stackGroups(rows)
	representatives := make([]TableRow, len(groups))
	byKey := make(map[prKey][]TableRow, len(groups))
	for i, group := range groups {
		representatives[i] = group[0]
		byKey[makePRKey(group[0].Item.PR)] = group
	}
	if sortColumn != "" {
		representatives = table.SortRows(representatives, columns, sortColumn, asc)
	}
	result := make([]TableRow, 0, len(rows))
	for _, representative := range representatives {
		group := byKey[makePRKey(representative.Item.PR)]
		if len(group) > 1 {
			key := stackGroupKey(group)
			base := group[len(group)-1].Item.PR.BaseRefName
			if stack := group[0].Item.PR.Stack; stack != nil {
				base = stack.BaseRefName
			}
			header := fmt.Sprintf("%s · %s · %d PRs onto %s",
				group[0].Item.Repo, stackLabel(group), len(group), base)
			for i := range group {
				group[i].Item.StackKey = key
				group[i].Item.StackHeader = header
			}
		}
		result = append(result, group...)
	}
	return result
}

func stackGroups(rows []TableRow) [][]TableRow {
	// Native stacks survive rebasing, retargeting, and missing intermediate PRs.
	native := make(map[string][]int)
	for i, row := range rows {
		if stack := row.Item.PR.Stack; stack != nil && stack.ID != "" {
			native[row.Item.RepoNWO+":"+stack.ID] = append(native[row.Item.RepoNWO+":"+stack.ID], i)
		}
	}

	// Legacy chains must be unambiguous and stay within the target repository.
	heads := make(map[string][]int)
	for i, row := range rows {
		pr := row.Item.PR
		if pr.HeadRefName != "" && pr.HeadRepoNWO == pr.Repository.NameWithOwner &&
			pr.Stack == nil {
			key := pr.Repository.NameWithOwner + ":" + pr.HeadRefName
			heads[key] = append(heads[key], i)
		}
	}
	parents := make(map[int]int)
	children := make(map[int][]int)
	for i, row := range rows {
		pr := row.Item.PR
		if pr.Stack != nil || pr.BaseRefName == "" {
			continue
		}
		matches := heads[pr.Repository.NameWithOwner+":"+pr.BaseRefName]
		if len(matches) == 1 && matches[0] != i {
			parents[i] = matches[0]
			children[matches[0]] = append(children[matches[0]], i)
		}
	}
	for child, parent := range parents {
		if len(children[parent]) != 1 {
			delete(parents, child)
		}
	}

	assigned := make(map[int][]int)
	for _, indices := range native {
		slices.SortStableFunc(indices, func(a, b int) int {
			return rows[b].Item.PR.StackPosition - rows[a].Item.PR.StackPosition
		})
		for _, idx := range indices {
			assigned[idx] = indices
		}
	}
	for i := range rows {
		if _, ok := assigned[i]; ok {
			continue
		}
		if len(children[i]) == 1 {
			if _, linked := parents[children[i][0]]; linked {
				continue // start at the top, so the chain is already in display order
			}
		}
		chain := []int{i}
		seen := map[int]bool{i: true}
		for current := i; ; {
			parent, ok := parents[current]
			if !ok || seen[parent] {
				break
			}
			chain = append(chain, parent)
			seen[parent] = true
			current = parent
		}
		for _, idx := range chain {
			assigned[idx] = chain
		}
	}

	// Emit at the representative's original position, preserving the default sort.
	var groups [][]TableRow
	for i := range rows {
		indices, ok := assigned[i]
		if !ok {
			indices = []int{i} // malformed cycles remain individual PRs
		}
		if indices[0] != i {
			continue
		}
		group := make([]TableRow, len(indices))
		for j, idx := range indices {
			group[j] = rows[idx]
		}
		groups = append(groups, group)
	}
	return groups
}

func stackLabel(rows []TableRow) string {
	// Use a shared descriptive prefix, avoiding a bare author/namespace prefix.
	prefix := []rune(rows[0].Item.PR.HeadRefName)
	for _, row := range rows[1:] {
		head := []rune(row.Item.PR.HeadRefName)
		common := 0
		for common < len(prefix) && common < len(head) && prefix[common] == head[common] {
			common++
		}
		prefix = prefix[:common]
	}
	label := strings.TrimRight(string(prefix), "/-_")
	if label == "" ||
		!strings.Contains(label, "/") && strings.HasPrefix(rows[0].Item.PR.HeadRefName, label+"/") {
		label = rows[len(rows)-1].Item.PR.HeadRefName
	}
	return label
}

func stackGroupKey(rows []TableRow) prKey {
	pr := rows[0].Item.PR
	if pr.Stack != nil && pr.Stack.ID != "" {
		return prKey(pr.Repository.NameWithOwner + ":stack:" + pr.Stack.ID)
	}
	return prKey(pr.Repository.NameWithOwner + ":branch:" + rows[len(rows)-1].Item.PR.HeadRefName)
}
