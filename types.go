package main

import (
	"fmt"
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
)

func (m MergeStatus) String() string {
	switch m {
	case MergeStatusUnknown:
		return "unknown"
	case MergeStatusReady:
		return "ready"
	case MergeStatusBlocked:
		return "blocked"
	case MergeStatusCIPending:
		return "ci_pending"
	case MergeStatusCIFailed:
		return "ci_failed"
	}
	return "unknown"
}

func (m MergeStatus) MarshalJSON() ([]byte, error) { //nolint:unparam // satisfies json.Marshaler
	return []byte(`"` + m.String() + `"`), nil
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
	ReviewDecision string      `json:"-"`
	Repository     Repository  `json:"repository"`
	State          string      `json:"state"`
	Title          string      `json:"title"`
	TitleRaw       string      `json:"-"`
	UpdatedAt      time.Time   `json:"updatedAt"`
	URL            string      `json:"url"`

	automergeLoaded      bool
	reviewDecisionLoaded bool
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
	case "bullet", "b":
		return OutputBullet, true
	case "json", "j":
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
		return "json"
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
	ciStatusSuccess = "success"
	ciStatusFailure = "failure"
	ciStatusPending = "pending"
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
