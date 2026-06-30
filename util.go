package main

import (
	"runtime"
	"strings"
)

// deduplicate returns items in first-seen order with duplicates removed.
// When ignoreCase is true, string values are deduplicated case-insensitively.
func deduplicate[T comparable](items []T, ignoreCase bool) []T {
	seen := make(map[any]bool, len(items))
	unique := make([]T, 0, len(items))
	for _, item := range items {
		key := any(item)
		if ignoreCase {
			if s, ok := any(item).(string); ok {
				key = strings.ToLower(s)
			}
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = true
		unique = append(unique, item)
	}
	return unique
}

func isDarwin() bool {
	return runtime.GOOS == "darwin"
}

// pluralize returns singular or plural form based on count.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func splitCSV(s string) []string {
	var parts []string
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
