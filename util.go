package main

import (
	"slices"

	"github.com/maruel/natural"
)

// natsort sorts a slice in-place using natural ordering of the string key.
func natsort[S ~[]E, E ~string](s S) {
	slices.SortFunc(s, func(a, b E) int {
		return natural.Compare(string(a), string(b))
	})
}

// pluralize returns singular or plural form based on count.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
