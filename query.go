package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gechr/x/human"
	xslices "github.com/gechr/x/slices"
)

var (
	isoDateRe  = regexp.MustCompile(`^([<>=]{0,2})(\d{4}-\d{2}-\d{2}.*)$`)
	operatorRe = regexp.MustCompile(`^([<>]=?)(.+)$`)
)

// flipOperator inverts comparison operators for relative dates.
// "more than 2 weeks ago" (>) means the date is further in the past (<).
func flipOperator(op string) string {
	switch op {
	case ">":
		return "<"
	case "<":
		return ">"
	case ">=":
		return "<="
	case "<=":
		return ">="
	default:
		return op
	}
}

// parseDate converts human-readable durations to ISO 8601 dates for GitHub's search API.
func parseDate(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}

	// Passthrough: already ISO format
	if isoDateRe.MatchString(input) {
		return input, nil
	}

	// Extract operator prefix
	op := ""
	value := input
	if m := operatorRe.FindStringSubmatch(input); m != nil {
		op = m[1]
		value = m[2]
	}

	// Special keywords
	now := time.Now().UTC()
	switch strings.ToLower(value) {
	case "today":
		return ">=" + now.Format("2006-01-02"), nil
	case "yesterday":
		y := now.AddDate(0, 0, -1)
		return op + y.Format("2006-01-02"), nil
	}

	// Compound relative duration parsing (e.g. "2weeks", "1y6mo", "1d12h")
	s := strings.ToLower(value)

	var years, months, days int
	var dur time.Duration
	useDateTime := false
	parsed := false
	var prevMultiplier int64

	for len(s) > 0 {
		seg := driftSegmentRe.FindStringSubmatch(s)
		if seg == nil {
			return "", fmt.Errorf("invalid date specification: %s", input)
		}

		n, err := strconv.Atoi(seg[1])
		if err != nil {
			return "", fmt.Errorf("invalid date specification: %s", input)
		}

		unit := seg[2]
		mult := driftUnitMultiplier(unit)

		if parsed && mult >= prevMultiplier {
			return "", fmt.Errorf(
				"invalid date specification: units must be in descending order: %s",
				input,
			)
		}

		switch unit {
		case "y", unitYear, unitYears:
			years += n
		case "mo", unitMonth, unitMonths:
			months += n
		case "w", unitWeek, unitWeeks:
			days += n * daysPerWeek
		case "d", unitDay, unitDays:
			days += n
		case "h", "hr", "hrs", unitHour, unitHours:
			dur += time.Duration(n) * time.Hour
			useDateTime = true
		case "m", "min", "mins", unitMinute, unitMinutes:
			dur += time.Duration(n) * time.Minute
			useDateTime = true
		case "s", "sec", "secs", unitSecond, unitSeconds:
			dur += time.Duration(n) * time.Second
			useDateTime = true
		}

		prevMultiplier = mult
		parsed = true
		s = s[len(seg[0]):]
	}

	if !parsed {
		return "", fmt.Errorf("invalid date specification: %s", input)
	}

	t := now.AddDate(-years, -months, -days)
	if dur > 0 {
		t = t.Add(-dur)
	}

	// Flip operator for relative dates
	if op != "" {
		op = flipOperator(op)
	} else {
		op = ">="
	}

	if useDateTime {
		return op + t.Format("2006-01-02T15:04:05Z"), nil
	}
	return op + t.Format("2006-01-02"), nil
}

var (
	driftOpRe      = regexp.MustCompile(`^([<>]=?|={1,2})`)
	driftSegmentRe = regexp.MustCompile(
		`^(\d+)\s*(years|year|y|months|month|mo|weeks|week|w|days|day|d|hours|hour|hrs|hr|h|minutes|minute|mins|min|m|seconds|second|secs|sec|s)`,
	)
)

// driftUnitMultiplier returns the multiplier in seconds for a drift unit.
func driftUnitMultiplier(unit string) int64 {
	switch unit {
	case "y", unitYear, unitYears:
		return secsPerYear
	case "mo", unitMonth, unitMonths:
		return secsPerMonth
	case "w", unitWeek, unitWeeks:
		return secsPerWeek
	case "d", unitDay, unitDays:
		return secsPerDay
	case "h", "hr", "hrs", unitHour, unitHours:
		return secsPerHour
	case "m", "min", "mins", unitMinute, unitMinutes:
		return secsPerMinute
	case "s", "sec", "secs", unitSecond, unitSeconds:
		return 1
	default:
		return 0
	}
}

// parseDrift parses a drift duration specification into operator and seconds.
// It supports compound durations like "5y2m" or "1w3d" where units must be
// in descending order. A bare integer with no unit is treated as raw seconds
// but only as a standalone value.
func parseDrift(input string) (string, int64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", 0, fmt.Errorf("empty drift specification")
	}

	s := strings.ToLower(input)

	// Strip optional operator prefix.
	op := "<=" // default
	if m := driftOpRe.FindString(s); m != "" {
		op = m
		s = s[len(m):]
	}

	if s == "" {
		return "", 0, fmt.Errorf("invalid drift specification: %s", input)
	}

	// Try bare integer (raw seconds) - only valid as standalone value.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return "", 0, fmt.Errorf("invalid drift specification: negative value: %s", input)
		}
		return op, n, nil
	}

	// Loop-based compound segment parsing.
	var total int64
	var prevMultiplier int64
	parsed := false

	for len(s) > 0 {
		m := driftSegmentRe.FindStringSubmatch(s)
		if m == nil {
			return "", 0, fmt.Errorf("invalid drift specification: %s", input)
		}

		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return "", 0, fmt.Errorf("invalid drift number: %s", m[1])
		}

		mult := driftUnitMultiplier(m[2])

		if parsed && mult >= prevMultiplier {
			return "", 0, fmt.Errorf(
				"invalid drift specification: units must be in descending order: %s",
				input,
			)
		}

		total += n * mult
		prevMultiplier = mult
		parsed = true
		s = s[len(m[0]):]
	}

	if !parsed {
		return "", 0, fmt.Errorf("invalid drift specification: %s", input)
	}

	return op, total, nil
}

// formatDrift returns a human-readable description of a drift filter.
func formatDrift(op string, threshold int64) string {
	dur := formatDuration(threshold)
	switch op {
	case "<=":
		return "updated within " + dur + " of creation"
	case "<":
		return "updated less than " + dur + " after creation"
	case ">=":
		return "updated at least " + dur + " after creation"
	case ">":
		return "updated more than " + dur + " after creation"
	case "=", "==":
		if threshold == 0 {
			return "never updated after creation"
		}
		return "updated exactly " + dur + " after creation"
	default:
		return op + " " + dur
	}
}

// formatDuration converts seconds into the largest fitting human-readable unit.
func formatDuration(seconds int64) string {
	if seconds == 0 {
		return "0 seconds"
	}
	type unit struct {
		divisor int64
		single  string
		plural  string
	}
	units := []unit{
		{secsPerYear, unitYear, unitYears},
		{secsPerMonth, unitMonth, unitMonths},
		{secsPerWeek, unitWeek, unitWeeks},
		{secsPerDay, unitDay, unitDays},
		{secsPerHour, unitHour, unitHours},
		{secsPerMinute, unitMinute, unitMinutes},
		{1, unitSecond, unitSeconds},
	}
	for _, u := range units {
		if seconds%u.divisor == 0 {
			return human.Pluralize(int(seconds/u.divisor), u.single, u.plural)
		}
	}
	return fmt.Sprintf("%d seconds", seconds)
}

// hasNegatedValue reports whether any value has a leading "-" or "!" negation prefix.
func hasNegatedValue(values []string) bool {
	for _, v := range values {
		if rest := strings.TrimLeft(v, "-!"); rest != "" && rest != v {
			return true
		}
	}
	return false
}

// splitNegated partitions values into positive and negated groups. A value with
// a leading "-" or "!" is negated, with the prefix stripped from the result.
// Values present in both groups cancel each other.
func splitNegated(values []string) ([]string, []string) {
	var positive, negative []string
	for _, v := range values {
		if rest := strings.TrimLeft(v, "-!"); rest != "" && rest != v {
			negative = append(negative, rest)
		} else {
			positive = append(positive, v)
		}
	}
	return removeOpposingValues(positive, negative)
}

// removeOpposingValues removes values present in both groups. An explicit
// positive and negative occurrence cancel each other before qualifiers are built.
func removeOpposingValues(positive, negative []string) ([]string, []string) {
	positive = xslices.UniqueFold(positive)
	negative = xslices.UniqueFold(negative)
	if len(positive) == 0 || len(negative) == 0 {
		return positive, negative
	}

	positiveSet := make(map[string]bool, len(positive))
	for _, value := range positive {
		positiveSet[strings.ToLower(value)] = true
	}
	negativeSet := make(map[string]bool, len(negative))
	for _, value := range negative {
		negativeSet[strings.ToLower(value)] = true
	}

	positive = xslices.Filter(positive, func(value string) bool {
		return !negativeSet[strings.ToLower(value)]
	})
	negative = xslices.Filter(negative, func(value string) bool {
		return !positiveSet[strings.ToLower(value)]
	})
	return positive, negative
}

// resolveTeamMembers resolves each team slug to GitHub usernames via the plugin
// (falling back to config teams), returning an error if any team has no members.
func resolveTeamMembers(plug *Plugin, cfg *Config, teams []string) ([]string, error) {
	var members []string
	for _, team := range teams {
		m, err := plug.ResolveTeam(team, cfg)
		if err != nil {
			return nil, fmt.Errorf("resolving team %q: %w", team, err)
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("no members found for team %q", team)
		}
		members = append(members, m...)
	}
	return members, nil
}

// buildFilterQualifiers builds GitHub search qualifiers for a multi-value filter,
// splitting values (via splitNegated) into a positive OR-qualifier and individual
// negated "-qualifier:value" qualifiers (e.g. alice,!bob -> qualifier:alice -qualifier:bob).
func buildFilterQualifiers(qualifier string, values []string) []string {
	positive, negative := splitNegated(values)
	var out []string
	if q := buildORQualifier(qualifier, positive); q != "" {
		out = append(out, q)
	}
	for _, v := range negative {
		out = append(out, "-"+qualifier+":"+v)
	}
	return out
}

// buildORQualifier constructs a GitHub search OR expression for multi-value qualifiers.
// Single value: "qualifier:value"
// Multiple values: "(qualifier:v1 OR qualifier:v2 ... qualifier:vN)"
func buildORQualifier(qualifier string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		return qualifier + ":" + values[0]
	}
	parts := xslices.Map(values, func(v string) string { return qualifier + ":" + v })
	return "(" + strings.Join(parts, " OR ") + ")"
}

// buildOwnerQualifier constructs a GitHub search owner expression.
// GitHub's search API accepts user:<owner> for both personal accounts and orgs.
func buildOwnerQualifier(values []string) string {
	if len(values) == 0 {
		return ""
	}

	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, "user:"+v)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// buildExcludedOwnerQualifiers constructs negated GitHub search qualifiers that
// exclude both organization-owned and user-owned repositories.
func buildExcludedOwnerQualifiers(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	const ownerQualifierKinds = 2

	parts := make([]string, 0, len(values)*ownerQualifierKinds)
	for _, v := range values {
		parts = append(parts, "-org:"+v, "-user:"+v)
	}
	return parts
}
