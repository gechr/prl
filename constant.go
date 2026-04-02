package main

import "time"

// valueEllipsis is the Unicode ellipsis character (U+2026).
const valueEllipsis = "…"

// Filter/state string values.
const (
	valueAll      = "all"
	valueAny      = "any"
	valueAtMe     = "@me"
	valueBehind   = "behind"
	valueBlocked  = "blocked"
	valueClosed   = "closed"
	valueCreated  = "created"
	valueMerged   = "merged"
	valueName     = "name"
	valueOpen     = "open"
	valueReady    = "ready"
	valueRejected = "rejected"
	valueRepo     = "repo"
	valueTable    = "table"
	valueUnknown  = "unknown"
	valueUpdated  = "updated"
	valueURL      = "url"

	colTitle = "title"

	copilotReviewer   = "copilot-pull-request-reviewer[bot]"
	sg2WizardReviewer = "sg2-wizard[bot]"
)

// GitHub API status values.
const (
	valueCIError         = "ERROR"
	valueCIExpected      = "EXPECTED"
	valueCIFailure       = "FAILURE"
	valueCIPending       = "PENDING"
	valueCISuccess       = "SUCCESS"
	valueReviewApproved  = "APPROVED"
	valueReviewChanges   = "CHANGES_REQUESTED"
	valueReviewDismissed = "DISMISSED"
)

const (
	valueReviewFilterNone     = "none"
	valueReviewFilterRequired = "required"
	valueReviewFilterApproved = "approved"
	valueReviewFilterChanges  = "changes_requested"
)

// Defaults.
const (
	defaultLimit   = 30
	maxConcurrency = 10
	maxPerPage     = 100
	maxTitleLen    = 100
	daysPerWeek    = 7
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
	resultAutomerged      = "Automerge"
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
	watchMinInterval    = 3 * time.Second                // floor: few results
	watchMaxInterval    = 30 * time.Second               // ceiling: many results
	watchScalePer       = 500 * time.Millisecond         // additional delay per result
	watchIdleDecay      = 1 * time.Hour                  // no interaction for this long → interval reaches watchIdleMax
	watchIdleMax        = 60 * time.Second               // ceiling when fully idle
	detailCheckInterval = 10 * time.Second               // poll interval for detail-view check refresh
	ansiClearScreen     = "\033[2J\033[H"                // clear screen + move cursor to top-left
	ansiHideCursor      = "\033[?25l"                    // hide cursor
	ansiShowCursor      = "\033[?25h"                    // show cursor
	ansiAltScreenOn     = "\033[?1049h"                  // switch to alternate screen buffer
	ansiAltScreenOff    = "\033[?1049l"                  // switch back to main screen buffer
	ansiDECXCPR         = "\033[?6n"                     // request extended cursor position (unambiguous)
	ansiMoveTo1x1       = "\033[1;1H"                    // move cursor to row 1, col 1
	ansiClearLine       = "\x1b[2K\r"                    // erase current line and return cursor to col 0
	ansiSpinnerClear    = ansiClearLine + ansiShowCursor // erase spinner line and restore cursor
)

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

	tuiActionApprove      = "approve-pr"
	tuiActionApproveMerge = "approve/merge"
	tuiActionClose        = "close"
	tuiActionComment      = "comment"
	tuiActionForceMerge   = "force-merge"
	tuiActionInfo         = "info"
	tuiActionMerge        = "merge"
	tuiActionReview       = "review"
	tuiActionSendSlack    = "send-slack"
	tuiActionUnassign     = "unassign"
	tuiActionUpdateBranch = "update-branch"

	tuiAIReviewUnsupported = "AI review is only supported in Ghostty and iTerm2 for now!"

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
	tuiStatusFlash    = 5 * time.Second
	tuiScreenCheckInt = 1 * time.Second

	tuiNonCursorPrefix = "  "

	tuiModAlt   = "alt+"
	tuiModCtrl  = "ctrl+"
	tuiModShift = "shift+"

	tuiKeyCtrlB       = tuiModCtrl + "b"
	tuiKeyCtrlC       = tuiModCtrl + "c"
	tuiKeyCtrlD       = tuiModCtrl + "d"
	tuiKeyCtrlF       = tuiModCtrl + "f"
	tuiKeyDown        = "down"
	tuiKeyEnter       = "enter"
	tuiKeyEsc         = "esc"
	tuiKeyLeft        = "left"
	tuiKeybindOptions = "O"
	tuiKeyRight       = "right"
	tuiKeySpace       = "space"
	tuiKeyTab         = "tab"
	tuiKeyShiftTab    = tuiModShift + tuiKeyTab
	tuiKeyUp          = "up"

	// Keybindings: actions.
	tuiKeybindQuit                = "q"
	tuiKeybindFilter              = "/"
	tuiKeybindTop                 = "g"
	tuiKeybindBottom              = "G"
	tuiKeybindSelectAll           = tuiModCtrl + "a"
	tuiKeybindInvertSelection     = "i"
	tuiKeybindApprove             = "a"
	tuiKeybindApproveAlias        = "y"
	tuiKeybindApproveNoConfirm    = tuiModAlt + "a"
	tuiKeybindCopyURL             = tuiModAlt + "c"
	tuiKeybindDiff                = "d"
	tuiKeybindDraftToggle         = "D"
	tuiKeybindMerge               = "m"
	tuiKeybindApproveMerge        = "A"
	tuiKeybindForceMerge          = "M"
	tuiKeybindClose               = "C"
	tuiKeybindComment             = "c"
	tuiKeybindReview              = "r"
	tuiKeybindReviewNoConfirm     = tuiModAlt + "r"
	tuiKeybindCopilotReview       = tuiModCtrl + "r"
	tuiKeybindSlack               = "s"
	tuiKeybindSlackNoConfirm      = tuiModAlt + "s"
	tuiKeybindOpen                = "o"
	tuiKeybindUpdateBranch        = "U"
	tuiKeybindUnassign            = "u"
	tuiKeybindUnassignNoConfirm   = tuiModAlt + "u"
	tuiKeybindHelp                = "?"
	tuiKeybindToggleRefresh       = "R"
	tuiKeybindNext                = "n"
	tuiKeybindPrev                = "p"
	tuiKeybindVimDown             = "j"
	tuiKeybindVimUp               = "k"
	tuiKeybindVimLeft             = "h"
	tuiKeybindVimRight            = "l"
	tuiKeybindExtendSelectionDown = tuiModShift + "down"
	tuiKeybindExtendSelectionUp   = tuiModShift + "up"
	tuiKeybindConfirmSubmit       = tuiModAlt + "enter"
	tuiKeybindConfirmYes          = "y"
	tuiKeybindConfirmNo           = "n"

	tuiKeysArrows        = "↑↓"
	tuiKeysArrowsUpDown  = tuiModShift + tuiKeysArrows
	tuiKeysJumpFirstLast = tuiKeybindTop + "/" + tuiKeybindBottom
	tuiKeysVimUpDown     = tuiKeybindVimUp + "/" + tuiKeybindVimDown
)

// tuiHelp* - terse lowercase labels for bottom help bars.
const (
	tuiHelpApprove       = "approve"
	tuiHelpApproveMerge  = "approve/merge"
	tuiHelpAutomerge     = "automerge"
	tuiHelpClose         = "close"
	tuiHelpComment       = "comment"
	tuiHelpCopilotReview = "copilot review"
	tuiHelpCopy          = "copy"
	tuiHelpDiff          = "diff"
	tuiHelpDismiss       = "dismiss"
	tuiHelpFilter        = "filter"
	tuiHelpHelp          = "help"
	tuiHelpMarkDraft     = "mark draft"
	tuiHelpMarkReady     = "mark ready"
	tuiHelpMerge         = "merge"
	tuiHelpNext          = "next"
	tuiHelpOpen          = "open"
	tuiHelpOptions       = "options"
	tuiHelpPrev          = "prev"
	tuiHelpQuit          = "quit"
	tuiHelpReopen        = "reopen"
	tuiHelpReview        = "review"
	tuiHelpScroll        = "scroll"
	tuiHelpSelect        = "select"
	tuiHelpShow          = "show"
	tuiHelpSlack         = "slack"
	tuiHelpUnsubscribe   = "unsubscribe"
	tuiHelpUpdateBranch  = "update branch"
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
