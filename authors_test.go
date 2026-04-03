package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorResolverUsesBuiltInAliases(t *testing.T) {
	resolver, err := NewAuthorResolver(&Config{})
	require.NoError(t, err)

	require.Equal(t, "Copilot", resolver.Resolve(copilotReviewer))
}

func TestAuthorResolverConfigOverridesBuiltInAliases(t *testing.T) {
	resolver, err := NewAuthorResolver(&Config{
		Authors: map[string]string{
			copilotReviewer: "Custom Copilot",
		},
	})
	require.NoError(t, err)

	require.Equal(t, "Custom Copilot", resolver.Resolve(copilotReviewer))
}
