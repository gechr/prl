package main

import (
	"os"
	"strings"
)

// IconMode determines which icon set is used.
type IconMode int

const (
	IconAuto IconMode = iota
	IconUnicode
	IconNerd
)

func parseIconMode(s string) (IconMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case valueAuto:
		return IconAuto, true
	case valueUnicode:
		return IconUnicode, true
	case valueNerd:
		return IconNerd, true
	default:
		return 0, false
	}
}

func (m IconMode) String() string {
	switch m {
	case IconAuto:
		return valueAuto
	case IconUnicode:
		return valueUnicode
	case IconNerd:
		return valueNerd
	default:
		return valueUnknown
	}
}

func resolveIconMode(m IconMode) IconMode {
	switch m {
	case IconAuto:
		if os.Getenv("NERD_FONTS") != "" {
			return IconNerd
		}
		return IconUnicode
	case IconUnicode, IconNerd:
		return m
	default:
		return IconUnicode
	}
}

type iconSet struct {
	Approved          string
	Rejected          string
	Dismissed         string
	Commented         string
	Copilot           string
	CIInProgress      string
	CISkipped         string
	CITimedOut        string
	CIActionRequired  string
	CINeutral         string
	CIStale           string
	CIUnknown         string
	Check             string
	Cross             string
	Warning           string
	PillLeft          string
	PillRight         string
	StatusMerged      string
	StatusDraft       string
	StatusClosed      string
	StatusReady       string
	StatusCIPending   string
	StatusCIFailed    string
	StatusNeedsReview string
	StatusConflict    string
	StatusUnknown     string
}

var iconsUnicode = iconSet{
	Approved:          "✅",
	Rejected:          "❌",
	Dismissed:         "🥀",
	Commented:         "💬",
	Copilot:           "🤖",
	CIInProgress:      "🔄",
	CISkipped:         "⏭️",
	CITimedOut:        "⏱️",
	CIActionRequired:  "⚠️",
	CINeutral:         "➖",
	CIStale:           "💤",
	CIUnknown:         "❓",
	Check:             "✔︎",
	Cross:             "✘",
	Warning:           "⚠",
	PillLeft:          "",
	PillRight:         "",
	StatusMerged:      "",
	StatusDraft:       "",
	StatusClosed:      "",
	StatusReady:       "",
	StatusCIPending:   "",
	StatusCIFailed:    "",
	StatusNeedsReview: "",
	StatusConflict:    "",
	StatusUnknown:     "",
}

var iconsNerd = iconSet{
	Approved:          "\u2714",
	Rejected:          "\u2718",
	Dismissed:         "\uf468", // nf-oct-circle_slash
	Commented:         "\uf41f", // nf-oct-comment
	Copilot:           "\uf4b8", // nf-oct-copilot
	CIInProgress:      "\uf46a", // nf-oct-sync
	CISkipped:         "\uf517", // nf-oct-skip
	CITimedOut:        "\uf43a", // nf-oct-clock
	CIActionRequired:  "\uf421", // nf-oct-alert
	CINeutral:         "\uf48b", // nf-oct-dash
	CIStale:           "\uf4ee", // nf-oct-moon
	CIUnknown:         "\uf420", // nf-oct-question
	Check:             "\u2714",
	Cross:             "\u2718",
	Warning:           "\uf421", // nf-oct-alert
	PillLeft:          "\ue0b6", // nf-ple-left_half_circle_thick
	PillRight:         "\ue0b4", // nf-ple-right_half_circle_thick
	StatusMerged:      "\uf419", // nf-oct-git_merge
	StatusDraft:       "\uf4dd", // nf-oct-git_pull_request_draft
	StatusClosed:      "\uf4dc", // nf-oct-git_pull_request_closed
	StatusReady:       "\uf407", // nf-oct-git_pull_request
	StatusCIPending:   "\uf43a", // nf-oct-clock
	StatusCIFailed:    "\u2718",
	StatusNeedsReview: "\uf441", // nf-oct-eye
	StatusConflict:    "\uf421", // nf-oct-alert
	StatusUnknown:     "\uf420", // nf-oct-question
}

var activeIcons = iconsUnicode

func useIcons(icons iconSet) {
	activeIcons = icons
}

func iconsFor(mode IconMode) iconSet {
	switch mode {
	case IconAuto:
		return iconsFor(resolveIconMode(mode))
	case IconUnicode:
		return iconsUnicode
	case IconNerd:
		return iconsNerd
	default:
		return iconsUnicode
	}
}
