package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gechr/clog"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

var (
	// moduleRefRe matches module references like module.reponame.
	moduleRefRe = regexp.MustCompile(`module\.(\w+)`)
	// sg2Re matches the sg2_repos local variable assignment.
	sg2Re = regexp.MustCompile(`(?s)sg2_repos\s*=\s*\[(.*?)\]`)
)

// groupModule represents a parsed module block from groups_*.tf.
type groupModule struct {
	Name string
	Body *hclsyntax.Body
	Src  []byte
}

// parseGroupModules parses groups_*.tf and returns all module blocks with their name attributes.
func parseGroupModules(membershipDir string) ([]groupModule, error) {
	pattern := filepath.Join(membershipDir, "groups_*.tf")
	groupFiles, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing %s: %w", pattern, err)
	}

	var modules []groupModule

	for _, gf := range groupFiles {
		src, readErr := os.ReadFile(gf)
		if readErr != nil {
			clog.Warn().Err(readErr).Str("file", gf).Msg("skipping unreadable file")
			continue
		}

		file, diags := hclsyntax.ParseConfig(src, gf, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return nil, fmt.Errorf("failed to parse %s: %s", gf, diags.Error())
		}

		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}

		for _, block := range body.Blocks {
			if block.Type != "module" {
				continue
			}
			nameAttr, hasName := block.Body.Attributes["name"]
			if !hasName {
				continue
			}
			nameVal, nameDiags := nameAttr.Expr.Value(nil)
			if nameDiags.HasErrors() {
				continue
			}
			modules = append(modules, groupModule{
				Name: nameVal.AsString(),
				Body: block.Body,
				Src:  src,
			})
		}
	}

	return modules, nil
}

// repoModule represents a parsed repository module from tf-github.
type repoModule struct {
	Label string // HCL module label
	Name  string // .name attribute value (or Label if absent)
	Body  *hclsyntax.Body
	Src   []byte
}

// parseRepoModules parses HCL files and returns all module blocks.
func parseRepoModules(files []string) ([]repoModule, error) {
	var modules []repoModule

	for _, filePath := range files {
		src, err := os.ReadFile(filePath)
		if err != nil {
			clog.Warn().Err(err).Str("file", filePath).Msg("skipping unreadable file")
			continue
		}

		file, diags := hclsyntax.ParseConfig(src, filePath, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return nil, fmt.Errorf("failed to parse %s: %s", filePath, diags.Error())
		}

		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}

		for _, block := range body.Blocks {
			if block.Type != "module" || len(block.Labels) == 0 {
				continue
			}
			label := block.Labels[0]
			name := label
			if nameAttr, hasName := block.Body.Attributes["name"]; hasName {
				nameVal, nameDiags := nameAttr.Expr.Value(nil)
				if !nameDiags.HasErrors() {
					name = nameVal.AsString()
				}
			}
			modules = append(modules, repoModule{
				Label: label,
				Name:  name,
				Body:  block.Body,
				Src:   src,
			})
		}
	}

	return modules, nil
}

// resolveTeamMembers resolves a team name to a list of GitHub usernames.
// It uses parseGroupModules to find modules whose .name matches the team,
// extracts .members[] values via regex, then maps to GitHub usernames via users.tf.
func resolveTeamMembers(teamName string, cfg *Config) ([]string, error) {
	modules, err := parseGroupModules(cfg.TerraformMemberDir)
	if err != nil {
		return nil, err
	}

	memberRe := regexp.MustCompile(`\["([^"]+)"\]`)
	var internalUsers []string

	for _, m := range modules {
		if m.Name != teamName {
			continue
		}
		membersAttr, hasMembers := m.Body.Attributes["members"]
		if !hasMembers {
			continue
		}
		srcRange := membersAttr.SrcRange
		membersSrc := string(m.Src[srcRange.Start.Byte:srcRange.End.Byte])
		matches := memberRe.FindAllStringSubmatch(membersSrc, -1)
		for _, match := range matches {
			internalUsers = append(internalUsers, match[1])
		}
	}

	if len(internalUsers) == 0 {
		return nil, nil
	}

	// Map internal usernames to GitHub usernames via users.tf
	githubMap, err := parseUsersHCL(cfg)
	if err != nil {
		// If users.tf can't be parsed, return internal names as-is.
		return internalUsers, nil //nolint:nilerr // graceful fallback
	}

	var ghUsers []string
	for _, u := range internalUsers {
		if ghUser, ok := githubMap[u]; ok {
			ghUsers = append(ghUsers, ghUser)
		}
	}

	clog.Debug().Str("team", teamName).Strs("members", ghUsers).Msg("Resolved team members")

	return ghUsers, nil
}

var (
	usersCacheOnce   sync.Once
	usersCacheResult map[string]map[string]string
	errUsersCache    error
)

// parseUsersTerraform parses users.tf and returns per-user attribute maps.
// Each entry maps an internal name to its attributes (github_username, first_name, etc.).
// Results are cached after the first successful call.
func parseUsersTerraform(cfg *Config) (map[string]map[string]string, error) {
	usersCacheOnce.Do(func() {
		usersCacheResult, errUsersCache = parseUsersTerraformUncached(cfg)
	})
	return usersCacheResult, errUsersCache
}

func parseUsersTerraformUncached(cfg *Config) (map[string]map[string]string, error) {
	usersFile := filepath.Join(cfg.TerraformMemberDir, "users.tf")
	src, err := os.ReadFile(usersFile)
	if err != nil {
		return nil, err
	}

	file, diags := hclsyntax.ParseConfig(src, usersFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, diags
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("unexpected HCL body type in %s", usersFile)
	}

	result := make(map[string]map[string]string)

	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}
		usersAttr, hasUsers := block.Body.Attributes["users"]
		if !hasUsers {
			continue
		}

		// users is an object expression: { key = { attrs... }, ... }
		objExpr, ok := usersAttr.Expr.(*hclsyntax.ObjectConsExpr)
		if !ok {
			continue
		}

		for _, item := range objExpr.Items {
			keyVal, kDiags := item.KeyExpr.Value(nil)
			if kDiags.HasErrors() {
				continue
			}
			internalName := keyVal.AsString()

			// Each user value is another object: { first_name = "...", ... }
			userObj, ok := item.ValueExpr.(*hclsyntax.ObjectConsExpr)
			if !ok {
				continue
			}

			attrs := make(map[string]string)
			for _, uItem := range userObj.Items {
				aKey, aDiags := uItem.KeyExpr.Value(nil)
				if aDiags.HasErrors() {
					continue
				}
				aVal, vDiags := uItem.ValueExpr.Value(nil)
				if vDiags.HasErrors() || aVal.Type().FriendlyName() != "string" {
					continue
				}
				attrs[aKey.AsString()] = aVal.AsString()
			}
			result[internalName] = attrs
		}
	}

	return result, nil
}

// parseUsersHCL parses users.tf to build an internal_name -> github_username mapping.
func parseUsersHCL(cfg *Config) (map[string]string, error) {
	users, err := parseUsersTerraform(cfg)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(users))
	for internalName, attrs := range users {
		if gh := attrs["github_username"]; gh != "" {
			result[internalName] = gh
		}
	}
	return result, nil
}

// parseUsersHCLEmails parses users.tf to build a lowercased github_username -> email mapping.
func parseUsersHCLEmails(cfg *Config) (map[string]string, error) {
	users, err := parseUsersTerraform(cfg)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(users))
	for _, attrs := range users {
		gh := attrs["github_username"]
		email := attrs["email"]
		if gh != "" && email != "" {
			result[strings.ToLower(gh)] = email
		}
	}
	return result, nil
}

// parseUsersHCLNames parses users.tf to build github_username -> display name mapping.
func parseUsersHCLNames(cfg *Config) (map[string]string, error) {
	users, err := parseUsersTerraform(cfg)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(users))
	for _, attrs := range users {
		gh := attrs["github_username"]
		if gh == "" {
			continue
		}
		name := strings.TrimSpace(attrs["first_name"] + " " + attrs["last_name"])
		if name != "" {
			result[strings.ToLower(gh)] = name
		}
	}
	return result, nil
}

// resolveTopicRepos resolves a topic to a list of repo names.
// Parses main.tf and sg2.tf from the tf-github directory.
func resolveTopicRepos(topic string, cfg *Config) ([]string, error) {
	tfDir := cfg.TerraformRepositoryDir

	// Parse sg2_repos from locals (list of module references like module.reponame)
	sg2Repos := parseSG2Repos(tfDir)

	var files []string
	for _, f := range []string{"main.tf", "sg2.tf"} {
		files = append(files, filepath.Join(tfDir, f))
	}

	modules, err := parseRepoModules(files)
	if err != nil {
		return nil, err
	}

	var repos []string
	seen := make(map[string]bool)

	for _, m := range modules {
		if topicMatches(m.Body, topic, m.Label, sg2Repos, m.Src) {
			if !seen[m.Name] {
				repos = append(repos, m.Name)
				seen[m.Name] = true
			}
		}
	}

	sort.Strings(repos)

	clog.Debug().Str("topic", topic).Strs("repos", repos).Msg("Resolved repos by topic")

	return repos, nil
}

// topicMatches checks if a module matches the given topic.
// moduleKey is the HCL module label (used for sg2_repos matching).
func topicMatches(
	body *hclsyntax.Body,
	topic, moduleKey string,
	sg2Repos map[string]bool,
	src []byte,
) bool {
	// Check explicit topics attribute
	if topicsAttr, ok := body.Attributes["topics"]; ok {
		srcRange := topicsAttr.SrcRange
		topicsSrc := string(src[srcRange.Start.Byte:srcRange.End.Byte])
		if strings.Contains(topicsSrc, `"`+topic+`"`) {
			return true
		}
	}

	// Special case: "sg2" topic matches modules in the sg2_repos list
	if strings.ToLower(topic) == "sg2" {
		return sg2Repos[moduleKey]
	}

	return false
}

// parseSG2Repos parses the sg2_repos local variable from tf-github.
func parseSG2Repos(tfDir string) map[string]bool {
	result := make(map[string]bool)

	// Try to find sg2_repos in locals blocks
	for _, f := range []string{"main.tf", "sg2.tf", "locals.tf"} {
		filePath := filepath.Join(tfDir, f)
		src, err := os.ReadFile(filePath)
		if err != nil {
			clog.Warn().Err(err).Str("file", filePath).Msg("skipping unreadable file")
			continue
		}

		if m := sg2Re.FindStringSubmatch(string(src)); m != nil {
			for _, ref := range moduleRefRe.FindAllStringSubmatch(m[1], -1) {
				result[ref[1]] = true
			}
		}
	}

	return result
}
