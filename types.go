package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Bot author constants.
const (
	BotName   = "Bot"   // Display name indicating a bot in config authors.
	BotSuffix = "[bot]" // Suffix for GitHub bot accounts.
)

// MergeStatus represents the CI/review readiness of a PR.
type MergeStatus int

const (
	MergeStatusUnknown   MergeStatus = iota
	MergeStatusReady                 // CI passing + approved
	MergeStatusBlocked               // CI passing, awaiting review/approval
	MergeStatusCIPending             // CI in progress
	MergeStatusCIFailed              // CI failed or errored
	MergeStatusConflict              // merge conflicts with base branch
	MergeStatusCIError               // a workflow failed to start; no check failed
	MergeStatusBehind                // CI passing + approved, but the base requires an up-to-date head
)

func (m MergeStatus) String() string {
	switch m {
	case MergeStatusUnknown:
		return valueUnknown
	case MergeStatusReady:
		return valueReady
	case MergeStatusBlocked:
		return valueBlocked
	case MergeStatusCIPending:
		return valueMergeCIPending
	case MergeStatusCIFailed:
		return valueMergeCIFailed
	case MergeStatusConflict:
		return valueConflict
	case MergeStatusCIError:
		return valueMergeCIError
	case MergeStatusBehind:
		return valueBehind
	}
	return "unknown"
}

func (m MergeStatus) MarshalJSON() ([]byte, error) { //nolint:unparam // satisfies json.Marshaler
	return []byte(`"` + m.String() + `"`), nil
}

// resolvedStatus is the fully resolved display status of a PR,
// combining State, MergeStatus, and IsDraft into a single value.
type resolvedStatus uint8

const (
	resolvedClosed      resolvedStatus = iota
	resolvedMerged                     // merged
	resolvedDraft                      // open + draft
	resolvedDraftCIFail                // open + draft + CI failed
	resolvedReady                      // open + CI pass + approved
	resolvedCIPending                  // open + CI in progress
	resolvedCIFailed                   // open + CI failed
	resolvedCIError                    // open + workflow failed to start
	resolvedBlocked                    // open + awaiting review
	resolvedConflict                   // open + merge conflicts
	resolvedBehind                     // open + CI pass + approved, but out of date
	resolvedUnknown                    // unknown state
)

// resolvePRStatus maps a PullRequest to its display status.
func resolvePRStatus(pr PullRequest) resolvedStatus {
	state := strings.ToLower(pr.State)
	if state == valueClosed {
		return resolvedClosed
	}
	if state == valueMerged {
		return resolvedMerged
	}
	if state != valueOpen {
		return resolvedUnknown
	}
	if pr.IsDraft {
		if pr.MergeStatus == MergeStatusCIFailed {
			return resolvedDraftCIFail
		}
		return resolvedDraft
	}
	switch pr.MergeStatus {
	case MergeStatusReady:
		return resolvedReady
	case MergeStatusCIPending:
		return resolvedCIPending
	case MergeStatusCIFailed:
		return resolvedCIFailed
	case MergeStatusCIError:
		return resolvedCIError
	case MergeStatusBlocked:
		return resolvedBlocked
	case MergeStatusConflict:
		return resolvedConflict
	case MergeStatusBehind:
		return resolvedBehind
	case MergeStatusUnknown:
		return resolvedUnknown
	}
	return resolvedUnknown
}

type Author struct {
	Login string `json:"login"`
}

type Label struct {
	Name string `json:"name"`
}

// PullRequest represents a GitHub pull request.
type PullRequest struct {
	Automerge      bool        `json:"automerge"`
	Author         Author      `json:"author"`
	CreatedAt      time.Time   `json:"createdAt"`
	IsDraft        bool        `json:"draft"`
	Labels         []Label     `json:"labels"`
	MergeStatus    MergeStatus `json:"mergeStatus"`
	NodeID         string      `json:"-"`
	Number         int         `json:"number"`
	HeadSHA        string      `json:"-"`
	HeadRefName    string      `json:"-"`
	BaseRefName    string      `json:"-"`
	HeadRepoNWO    string      `json:"-"`
	Stack          *PRStack    `json:"-"`
	StackPosition  int         `json:"-"`
	ReviewDecision string      `json:"-"`
	Repository     Repository  `json:"repository"`
	State          string      `json:"state"`
	Title          string      `json:"title"`
	TitleRaw       string      `json:"-"`
	UpdatedAt      time.Time   `json:"updatedAt"`
	URL            string      `json:"url"`

	automergeLoaded bool
	// behind is whether the base branch requires the head to be brought up
	// to date before merging.
	behind               bool
	reviewDecisionLoaded bool
	viewerApproved       bool
	viewerApprovalLoaded bool
	viewerIsAuthor       bool
}

// refSingleOwner is set when all results belong to a single owner,
// causing Ref to omit the owner prefix for brevity.
var refSingleOwner string

// Ref returns a short GitHub-style reference.
// When a single owner is active, returns "repo#123"; otherwise "owner/repo#123".
func (pr PullRequest) Ref() string {
	name := pr.Repository.NameWithOwner
	if refSingleOwner != "" {
		name = pr.Repository.Name
	}
	return fmt.Sprintf("%s#%d", name, pr.Number)
}

type Repository struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
}

// OutputFormat determines how results are rendered.
type OutputFormat int

const (
	OutputTable OutputFormat = iota
	OutputBullet
	OutputJSON
	OutputRepo
	OutputURL
)

func parseOutputFormat(s string) (OutputFormat, bool) {
	switch s {
	case valueTable, "t":
		return OutputTable, true
	case valueURL, "u":
		return OutputURL, true
	case valueBullet, "b":
		return OutputBullet, true
	case outputJSON, "j":
		return OutputJSON, true
	case valueRepo, "r":
		return OutputRepo, true
	default:
		return 0, false
	}
}

func (f OutputFormat) String() string {
	switch f {
	case OutputTable:
		return valueTable
	case OutputURL:
		return valueURL
	case OutputBullet:
		return "bullet"
	case OutputJSON:
		return outputJSON
	case OutputRepo:
		return valueRepo
	default:
		return valueUnknown
	}
}

// SortField determines the sort order for results.
type SortField int

const (
	SortName SortField = iota
	SortCreated
	SortUpdated
)

func parseSortField(s string) (SortField, bool) {
	switch s {
	case valueName, "n":
		return SortName, true
	case valueCreated, "c":
		return SortCreated, true
	case valueUpdated, "u":
		return SortUpdated, true
	default:
		return 0, false
	}
}

func (f SortField) String() string {
	switch f {
	case SortName:
		return valueName
	case SortCreated:
		return valueCreated
	case SortUpdated:
		return valueUpdated
	default:
		return valueUnknown
	}
}

// PRState filters pull requests by state.
type PRState int

const (
	StateOpen PRState = iota
	StateClosed
	StateReady
	StateMerged
	StateAll
)

func parsePRState(s string) (PRState, bool) {
	switch s {
	case valueOpen, "o":
		return StateOpen, true
	case valueClosed, "c":
		return StateClosed, true
	case valueReady, "r":
		return StateReady, true
	case valueMerged, "m":
		return StateMerged, true
	case valueAll, valueAny, "a":
		return StateAll, true
	default:
		return 0, false
	}
}

func (s PRState) String() string {
	switch s {
	case StateOpen:
		return valueOpen
	case StateClosed:
		return valueClosed
	case StateMerged:
		return valueMerged
	case StateAll:
		return valueAll
	case StateReady:
		return valueReady
	default:
		return valueUnknown
	}
}

// CIStatus represents CI check status.
type CIStatus int

const (
	CINone CIStatus = iota
	CISuccess
	CIFailure
	CIPending
)

const (
	ciConclusionActionRequired = "action_required"
	ciConclusionCancelled      = "cancelled"
	ciConclusionNeutral        = "neutral"
	ciConclusionSkipped        = "skipped"
	ciConclusionStale          = "stale"
	ciConclusionTimedOut       = "timed_out"
	ciStatusFailure            = "failure"
	ciStatusPending            = "pending"
	ciStatusSuccess            = "success"
	ciFilterNotFailing         = "!" + ciStatusFailure
)

func parseCIStatus(s string) (CIStatus, bool) {
	switch s {
	case ciStatusSuccess, "s", "pass", "passed":
		return CISuccess, true
	case ciStatusFailure, "f", "fail", "failed":
		return CIFailure, true
	case ciStatusPending, "p":
		return CIPending, true
	default:
		return 0, false
	}
}

func (c CIStatus) String() string {
	switch c {
	case CISuccess:
		return ciStatusSuccess
	case CIFailure:
		return ciStatusFailure
	case CIPending:
		return ciStatusPending
	case CINone:
		return ""
	}
	return ""
}

// ciStatuses lists the filterable CI statuses in canonical order.
var ciStatuses = [...]CIStatus{CISuccess, CIFailure, CIPending}

// CIFilter selects PRs by CI status. A PR must match one of the included
// statuses (when any are set) and none of the excluded ones.
type CIFilter struct {
	include []CIStatus
	exclude []CIStatus
}

// parseCIFilter parses a comma-separated list of CI statuses, each optionally
// negated with a leading "!" or "-" (e.g. "!failure" or "success,pending").
// Every token is canonicalised before a status that is both included and
// excluded cancels out. Empty tokens, bare negators and unknown statuses are
// rejected.
func parseCIFilter(s string) (CIFilter, bool) {
	if strings.TrimSpace(s) == "" {
		return CIFilter{}, true
	}
	var include, exclude [len(ciStatuses) + 1]bool
	for token := range strings.SplitSeq(s, ",") {
		token = strings.TrimSpace(token)
		value := strings.TrimLeft(token, "-!")
		if value == "" {
			return CIFilter{}, false
		}
		status, ok := parseCIStatus(value)
		if !ok {
			return CIFilter{}, false
		}
		if value != token {
			exclude[status] = true
		} else {
			include[status] = true
		}
	}
	var f CIFilter
	for _, status := range ciStatuses {
		if include[status] && exclude[status] {
			continue
		}
		if include[status] {
			f.include = append(f.include, status)
		}
		if exclude[status] {
			f.exclude = append(f.exclude, status)
		}
	}
	return f, true
}

// Active reports whether the filter constrains anything.
func (f CIFilter) Active() bool {
	return len(f.include) > 0 || len(f.exclude) > 0
}

// Matches reports whether a PR's enriched MergeStatus satisfies the filter.
// PRs without a CI status (unknown, conflict) only pass exclusion-only filters.
func (f CIFilter) Matches(pr PullRequest) bool {
	if len(f.include) > 0 && !slices.ContainsFunc(f.include, func(ci CIStatus) bool {
		return matchesCI(pr, ci)
	}) {
		return false
	}
	return !slices.ContainsFunc(f.exclude, func(ci CIStatus) bool {
		return matchesCI(pr, ci)
	})
}

// String returns the canonical form: included statuses, then negated ones.
func (f CIFilter) String() string {
	return f.join(func(ci CIStatus) string { return ci.String() })
}

// join renders each status with name and negates excluded ones with "!".
func (f CIFilter) join(name func(CIStatus) string) string {
	parts := make([]string, 0, len(f.include)+len(f.exclude))
	for _, ci := range f.include {
		parts = append(parts, name(ci))
	}
	for _, ci := range f.exclude {
		parts = append(parts, "!"+name(ci))
	}
	return strings.Join(parts, ",")
}
