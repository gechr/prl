package main

import "time"

// Filter/state string values.
const (
	valueAll     = "all"
	valueAtMe    = "@me"
	valueAny     = "any"
	valueClosed  = "closed"
	valueCreated = "created"
	valueMerged  = "merged"
	valueName    = "name"
	valueBlocked = "blocked"
	valueOpen    = "open"
	valueReady   = "ready"
	valueRepo    = "repo"
	valueTable   = "table"
	valueUnknown = "unknown"
	valueUpdated = "updated"
	valueURL     = "url"
)

// Defaults.
const (
	defaultLimit   = 30
	maxConcurrency = 10
	maxPerPage     = 100
	maxTitleLen    = 80
	daysPerWeek    = 7
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

// Watch mode.
const watchInterval = 10 * time.Second

// UI layout.
const (
	editBodyMinLines = 3 // minimum body textarea height
	editChrome       = 8 // fixed rows: header + blank + "Title" label + title + blank + "Body" label + blank + help
	editTitleYOffset = 3 // header + blank + "Title" label
	editBodyYOffset  = 5 // header + blank + "Title" label + title-end + blank + "Body" label (excluding title lines)
	editWidth        = 120
	maxSelectHeight  = 50
)
