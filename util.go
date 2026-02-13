package main

import (
	"slices"
	"strings"

	"github.com/maruel/natural"
)

// natsort sorts a slice in-place using natural ordering of the string key.
func natsort[S ~[]E, E ~string](s S) {
	slices.SortFunc(s, func(a, b E) int {
		return natural.Compare(string(a), string(b))
	})
}

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

// pluralize returns singular or plural form based on count.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
