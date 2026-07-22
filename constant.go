package main

import (
	"time"

	xansi "github.com/gechr/x/ansi"
)

const nl = "\n"

// helpGap is the two-space separator between help key pairs in TUI footer bars.
const helpGap = "  "

// valueEllipsis is the Unicode ellipsis character (U+2026).
const valueEllipsis = "…"

// Filter/state string values.
const (
	valueAdded    = "added"
	valueAll      = "all"
	valueAny      = "any"
	valueAsc      = "asc"
	valueAtMe     = "@me"
	valueBehind   = "behind"
	valueBlocked  = "blocked"
	valueBullet   = "bullet"
	valueChannel  = "channel"
	valueClosed   = "closed"
	valueConflict = "conflict"
	valueCreated  = "created"
	valueDeleted  = "deleted"
	valueDesc     = "desc"
	valueHide     = "hide"
	valueMerged   = "merged"
	valueName     = "name"
	valueOpen     = "open"
	valueReady    = "ready"
	valueRejected = "rejected"
	valueRemoved  = "removed"
	valueRenamed  = "renamed"
	valueRepo     = "repo"
	valueTable    = "table"
	valueUnknown  = "unknown"
	valueUpdated  = "updated"
	valueURL      = "url"
	valueUser     = "user"
	valueUsers    = "users"

	colAuthor = "author"
	colI      = "i"
	colIdx    = "idx"
	colIndex  = "index"
	colLabels = "labels"
	colNumber = "number"
	colOwner  = "owner"
	colReason = "reason"
	colRef    = "ref"
	colState  = "state"
	colStatus = "status"
	colTitle  = "title"

	// Time-unit tokens parsed in query relative-date expressions.
	unitYear    = "year"
	unitYears   = "years"
	unitMonth   = "month"
	unitMonths  = "months"
	unitWeek    = "week"
	unitWeeks   = "weeks"
	unitDay     = "day"
	unitDays    = "days"
	unitHour    = "hour"
	unitHours   = "hours"
	unitMinute  = "minute"
	unitMinutes = "minutes"
	unitSecond  = "second"
	unitSeconds = "seconds"

	// JSON/GraphQL field names exchanged with the GitHub API.
	keyAfter = "after"
	keyBody  = "body"
	keyIDs   = "ids"

	mediaTypeGitHubDiff = "application/vnd.github.diff"

	copilotReviewer = "copilot-pull-request-reviewer[bot]"
)

// Output format string values.
const (
	outputJSON = "json"
)

// MergeStatus.String() display strings. Distinct from the GitHub API
// check-state enum values below (valueCIPending, valueCIFailure).
const (
	valueMergeCIFailed  = "ci_failed"
	valueMergeCIPending = "ci_pending"
)

// PR table-model status strings (pr_table_model.go). valueMergeCIPending
// above is shared.
const (
	valueCIFail        = "ci_fail"
	valueMergeConflict = "merge_conflict"
	valueNeedsReview   = "needs_review"
	valueReadyToMerge  = "ready_to_merge"
)

// GitHub API status values.
const (
	valueCIError         = "ERROR"
	valueCIActionNeeded  = "ACTION_REQUIRED"
	valueCICancelled     = "CANCELLED"
	valueCIExpected      = "EXPECTED"
	valueCIFailure       = "FAILURE"
	valueCIInProgress    = "IN_PROGRESS"
	valueCIPending       = "PENDING"
	valueCIQueued        = "QUEUED"
	valueCIStartupFailed = "STARTUP_FAILURE"
	valueCISuccess       = "SUCCESS"
	valueCITimedOut      = "TIMED_OUT"
	valueCIUnknown       = "UNKNOWN"
	valueMergeStateClean = "CLEAN"
	valueMergeStateDirty = "DIRTY"
	valueReviewApproved  = "APPROVED"
	valueReviewChanges   = "CHANGES_REQUESTED"
	valueReviewDismissed = "DISMISSED"
)

const (
	valueReviewFilterNone         = "none"
	valueReviewFilterRequired     = "required"
	valueReviewFilterSelfRequired = "self_required"
	valueReviewFilterApproved     = "approved"
	valueReviewFilterChanges      = "changes_requested"
)

// Defaults.
const (
	defaultLimit   = 30
	maxConcurrency = 10
	maxPerPage     = 100
	maxTitleLen    = 100
	daysPerWeek    = 7
)

// GitHub API pacing and rate-limit backoff.
const (
	headerRetryAfter         = "Retry-After"
	headerRateLimitRemaining = "X-Ratelimit-Remaining"
	headerRateLimitReset     = "X-Ratelimit-Reset"

	githubSecondaryRetryFallback = 30 * time.Second
	githubRateLimitResetSkew     = 1 * time.Second
)

// Layout: terminal width thresholds and column width estimates.
const (
	compactTimeThreshold = 120 // use compact time format below this terminal width
	columnGap            = 2   // spaces between columns (matches internal/table defaultColumnPadding)
)

// Duration multipliers in seconds.
const (
	secsPerMinute = int64(time.Minute / time.Second)
	secsPerHour   = int64(time.Hour / time.Second)
	secsPerDay    = int64(24 * time.Hour / time.Second)
	secsPerWeek   = int64(daysPerWeek) * secsPerDay
	secsPerMonth  = int64(30) * secsPerDay
	secsPerYear   = int64(365) * secsPerDay
)

// VCS options for --clone.
const (
	vcsGit = "git"
	vcsJJ  = "jj"
)

// Action result strings.
const (
	resultApproved        = "Approved"
	resultAutomerged      = "Automerge enabled"
	resultBranchUpdated   = "Branch updated"
	resultClosed          = "Closed"
	resultCommented       = "Commented"
	resultCopied          = "Copied"
	resultEnqueued        = "Enqueued"
	resultForceMerged     = "Force-merged"
	resultMarkedDraft     = "Marked draft"
	resultMarkedReady     = "Marked ready"
	resultMerged          = "Merged"
	resultOpened          = "Opened"
	resultReopened        = "Reopened"
	resultReviewRequested = "Copilot review requested"
	resultUnknown         = "Unknown"
	resultUnsubscribed    = "Unsubscribed"
)

// Flash status strings shown while an action is in progress.
const (
	statusApproving      = "Approving"
	statusApproveMerging = "Approving/merging"
	statusAutomerging    = "Automerging"
	statusCopilotReview  = "Requesting Copilot review"
	statusDiffing        = "Diffing"
	statusMarkingDraft   = "Marking draft"
	statusMarkingReady   = "Marking ready"
	statusMerging        = "Merging"
	statusReopening      = "Reopening"
	statusSlacking       = "Slacking"
	statusUnsubscribing  = "Unsubscribing"
)

// Watch mode.
const (
	watchMinInterval    = 7 * time.Second  // floor: few results
	watchMaxInterval    = 1 * time.Minute  // ceiling: many results
	watchScalePer       = 1 * time.Second  // additional delay per result
	watchIdleDecay      = 45 * time.Minute // no interaction for this long -> interval reaches watchIdleMax
	watchIdleMax        = 3 * time.Minute  // ceiling when fully idle
	detailCheckInterval = 15 * time.Second // poll interval for detail-view check refresh
)

// ansiSpinnerClear erases the spinner line and restores the cursor. Composed
// from xansi primitives so the shared package owns the literals.
const ansiSpinnerClear = xansi.ClearLine + xansi.ShowCursor

const ciStatusCompleted = "completed"

// UI layout.
const (
	editBodyMinLines = 3 // minimum body textarea height
	editChrome       = 8 // fixed rows: header + blank + "Title" label + title + blank + "Body" label + blank + help
	editTitleYOffset = 3 // header + blank + "Title" label
	editBodyYOffset  = 5 // header + blank + "Title" label + title-end + blank + "Body" label (excluding title lines)
	editWidth        = 120
	maxSelectHeight  = 50
)

// TUI constants.
const (
	tuiCursorPrefix = "❯ "

	tuiActionApprove       = "approve-pr"
	tuiActionApproveMerge  = "approve/merge"
	tuiActionClose         = "close"
	tuiActionComment       = "comment"
	tuiActionCopilotReview = "copilot-review"
	tuiActionForceMerge    = "force-merge"
	tuiActionInfo          = "info"
	tuiActionMerge         = "merge"
	tuiActionReview        = "review"
	tuiActionSendSlack     = "send-slack"
	tuiActionUnassign      = "unassign"
	tuiActionUpdateBranch  = "update-branch"

	tuiAIReviewUnsupported = "AI review is only supported in Herdr, Ghostty, iTerm2, and Kitty for now!"

	tuiConfirmInputWidth       = 70
	tuiAIReviewConfirmInputWid = 90
	tuiConfirmInputMinHeight   = 2
	tuiConfirmInputMaxHeight   = 30
	tuiConfirmPadX             = 4
	tuiConfirmPadY             = 2
	tuiScrollbarWidth          = 1
	tuiOptionsPadX             = 2
	tuiOptionsPadY             = 1

	tuiJumpTimeout    = 500 * time.Millisecond
	tuiRenderFPS      = 30
	tuiStatusFlash    = 5 * time.Second
	tuiScreenCheckInt = 1 * time.Second

	tuiNonCursorPrefix = "  "

	tuiKeybindOptions = "O"

	// Keybindings: actions.
	tuiKeybindQuit              = "q"
	tuiKeybindFilter            = "/"
	tuiKeybindTop               = "g"
	tuiKeybindBottom            = "G"
	tuiKeybindSelectAll         = "ctrl+a"
	tuiKeybindInvertSelection   = "i"
	tuiKeybindApprove           = "a"
	tuiKeybindApproveNoConfirm  = "alt+a"
	tuiKeybindCopyURL           = "alt+c"
	tuiKeybindDiff              = "d"
	tuiKeybindDraftToggle       = "D"
	tuiKeybindMerge             = "m"
	tuiKeybindApproveMerge      = "A"
	tuiKeybindForceMerge        = "M"
	tuiKeybindClose             = "C"
	tuiKeybindComment           = "c"
	tuiKeybindReview            = "r"
	tuiKeybindReviewNoConfirm   = "alt+r"
	tuiKeybindCopilotReview     = "ctrl+r"
	tuiKeybindSlack             = "s"
	tuiKeybindSlackNoConfirm    = "alt+s"
	tuiKeybindOpen              = "o"
	tuiKeybindUpdateBranch      = "U"
	tuiKeybindUnassign          = "u"
	tuiKeybindUnassignNoConfirm = "alt+u"
	tuiKeybindHelp              = "?"
	tuiKeybindToggleRefresh     = "R"
	tuiKeybindNext              = "n"
	tuiKeybindPrev              = "p"
	tuiKeybindVimDown           = "j"
	tuiKeybindVimUp             = "k"
	tuiKeybindVimLeft           = "h"
	tuiKeybindVimRight          = "l"
	tuiKeybindConfirmNo         = "n"

	tuiKeysJumpFirstLast = tuiKeybindTop + "/" + tuiKeybindBottom
	tuiKeysVimUpDown     = tuiKeybindVimUp + "/" + tuiKeybindVimDown
)

// tuiHelp* - terse lowercase labels for bottom help bars.
const (
	tuiHelpApprove      = "approve"
	tuiHelpApproveMerge = "approve/merge"
	tuiHelpAutomerge    = "automerge"
	tuiHelpClose        = "close"
	tuiHelpComment      = "comment"
	tuiHelpCopilot      = "copilot"
	tuiHelpCopy         = "copy"
	tuiHelpDiff         = "diff"
	tuiHelpDismiss      = "dismiss"
	tuiHelpFilter       = "filter"
	tuiHelpHelp         = "help"
	tuiHelpMarkDraft    = "draft"
	tuiHelpMarkReady    = "ready"
	tuiHelpMerge        = "merge"
	tuiHelpNext         = "next"
	tuiHelpOpen         = "open"
	tuiHelpOptions      = "options"
	tuiHelpPrev         = "prev"
	tuiHelpQuit         = "quit"
	tuiHelpReopen       = "reopen"
	tuiHelpReview       = "review"
	tuiHelpScroll       = "scroll"
	tuiHelpSelect       = "select"
	tuiHelpShow         = "show"
	tuiHelpSlack        = "slack"
	tuiHelpUnsubscribe  = "unsubscribe"
	tuiHelpUpdateBranch = "update branch"
)

// tuiDesc* - Title Case descriptions for the ? help overlay.
const (
	tuiDescApprove          = "Approve PRs"
	tuiDescApproveMerge     = "Approve/Merge PRs"
	tuiDescApproveNoConfirm = "Approve PRs (no confirm)"
	tuiDescClose            = "Close/Reopen PRs"
	tuiDescCopilotReview    = "Request Copilot review"
	tuiDescCopy             = "Copy URL(s)"
	tuiDescCycleSortOrder   = "Cycle sort order"
	tuiDescDiff             = "View diff"
	tuiDescDraftToggle      = "Toggle draft"
	tuiDescExtendSelection  = "Extend selection"
	tuiDescFilter           = "Filter"
	tuiDescForceMerge       = "Force-merge PRs"
	tuiDescHelp             = "Toggle this help"
	tuiDescInvertSelection  = "Invert selection"
	tuiDescJumpFirstLast    = "Jump to first/last"
	tuiDescMerge            = "Merge/Automerge PRs"
	tuiDescNavigate         = "Navigate up/down"
	tuiDescOpen             = "Open in browser"
	tuiDescOptions          = "Options"
	tuiDescQuit             = "Quit"
	tuiDescRefresh          = "Toggle auto-refresh"
	tuiDescReview           = "Launch AI review"
	tuiDescReviewNoConfirm  = "Launch AI review (no confirm)"
	tuiDescSelect           = "Toggle selection"
	tuiDescSelectAll        = "Select all/none"
	tuiDescSendSlack        = "Send to Slack"
	tuiDescSendSlackNoConf  = "Send to Slack (no confirm)"
	tuiDescShow             = "Show PR detail"
	tuiDescUnassign         = "Unassign/unsubscribe"
	tuiDescUnassignNoConf   = "Unassign (no confirm)"
	tuiDescUpdateBranch     = "Update branch"
)
