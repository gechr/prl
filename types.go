package main

import "time"

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

// PullRequest represents a GitHub pull request.
type PullRequest struct {
	AutoMerge   bool        `json:"-"`
	Author      Author      `json:"author"`
	CreatedAt   time.Time   `json:"createdAt"`
	Labels      []Label     `json:"labels"`
	MergeStatus MergeStatus `json:"-"`
	NodeID      string      `json:"-"`
	Number      int         `json:"number"`
	Repository  Repository  `json:"repository"`
	State       string      `json:"state"`
	Title       string      `json:"title"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	URL         string      `json:"url"`
}

type Repository struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
}

type Author struct {
	Login string `json:"login"`
}

type Label struct {
	Name string `json:"name"`
}

// OutputFormat determines how results are rendered.
type OutputFormat int

const (
	OutputTable OutputFormat = iota
	OutputBullet
	OutputJSON
	OutputRepo
	OutputSlack
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
	case "slack", "s":
		return OutputSlack, true
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
	case OutputSlack:
		return "slack"
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
	StateMerged
	StateAll
)

func parsePRState(s string) (PRState, bool) {
	switch s {
	case valueOpen, "o":
		return StateOpen, true
	case valueClosed, "c":
		return StateClosed, true
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

func parseCIStatus(s string) (CIStatus, bool) {
	switch s {
	case "success", "s":
		return CISuccess, true
	case "failure", "f":
		return CIFailure, true
	case "pending", "p":
		return CIPending, true
	default:
		return 0, false
	}
}

func (c CIStatus) String() string {
	switch c {
	case CISuccess:
		return "success"
	case CIFailure:
		return "failure"
	case CIPending:
		return "pending"
	case CINone:
		return ""
	}
	return ""
}
