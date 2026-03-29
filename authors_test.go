package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorResolverUsesBuiltInAliases(t *testing.T) {
	resolver := NewAuthorResolver(&Config{})

	require.Equal(t, "Copilot", resolver.Resolve(copilotReviewer))
}

func TestAuthorResolverConfigOverridesBuiltInAliases(t *testing.T) {
	resolver := NewAuthorResolver(&Config{
		Authors: map[string]string{
			copilotReviewer: "Custom Copilot",
		},
	})

	require.Equal(t, "Custom Copilot", resolver.Resolve(copilotReviewer))
}
