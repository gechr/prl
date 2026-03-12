package main

import "time"

// Filter/state string values.
const (
	valueAll     = "all"
	valueAny     = "any"
	valueAtMe    = "@me"
	valueBlocked = "blocked"
	valueClosed  = "closed"
	valueCreated = "created"
	valueMerged  = "merged"
	valueName    = "name"
	valueOpen    = "open"
	valueReady   = "ready"
	valueRepo    = "repo"
	valueTable   = "table"
	valueUnknown = "unknown"
	valueUpdated = "updated"
	valueURL     = "url"

	colTitle = "title"
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

// Action result strings returned by mergeOrAutomerge.
const resultAutomerged = "Automerge"

// Watch mode.
const (
	watchMinInterval = 3 * time.Second                // floor: few results
	watchMaxInterval = 30 * time.Second               // ceiling: many results
	watchScalePer    = 500 * time.Millisecond         // additional delay per result
	ansiClearScreen  = "\033[2J\033[H"                // clear screen + move cursor to top-left
	ansiHideCursor   = "\033[?25l"                    // hide cursor
	ansiShowCursor   = "\033[?25h"                    // show cursor
	ansiAltScreenOn  = "\033[?1049h"                  // switch to alternate screen buffer
	ansiAltScreenOff = "\033[?1049l"                  // switch back to main screen buffer
	ansiMoveTo1x1    = "\033[1;1H"                    // move cursor to row 1, col 1
	ansiClearLine    = "\x1b[2K\r"                    // erase current line and return cursor to col 0
	ansiSpinnerClear = ansiClearLine + ansiShowCursor // erase spinner line and restore cursor
)

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

	tuiActionApprove = "approve-pr"
	tuiActionInfo    = "info"

	tuiClaudeReviewUnsupported = "Claude review is only supported in Ghostty and iTerm2 for now!"

	tuiConfirmPadX = 4
	tuiConfirmPadY = 2

	tuiJumpTimeout = 500 * time.Millisecond
	tuiStatusFlash = 5 * time.Second

	tuiKeyAltA         = "alt+a"
	tuiKeyCtrlB        = "ctrl+b"
	tuiKeyCtrlC        = "ctrl+c"
	tuiKeyCtrlD        = "ctrl+d"
	tuiKeyCtrlF        = "ctrl+f"
	tuiKeyDown         = "down"
	tuiKeyEnter        = "enter"
	tuiKeyEsc          = "esc"
	tuiKeyLeft         = "left"
	tuiKeyPgDown       = "pgdown"
	tuiKeyPgUp         = "pgup"
	tuiKeyRight        = "right"
	tuiKeyShiftDown    = "shift+down"
	tuiKeySpace        = "space"
	tuiKeyShiftUp      = "shift+up"
	tuiKeyTab          = "tab"
	tuiKeyUp           = "up"
	tuiNonCursorPrefix = "  "
)
