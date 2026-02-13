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
	valueOpen    = "open"
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

// UI layout.
const maxSelectHeight = 50
