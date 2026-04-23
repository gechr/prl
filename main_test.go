package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldDeferInitialWatchEnrichment(t *testing.T) {
	require.True(t, shouldDeferInitialWatchEnrichment(&CLI{}, true))
	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{}, false))
	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{Quick: true}, true))

	output := outputJSON
	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{Output: &output}, true))

	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{State: valueReady}, true))
	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{CI: ciStatusPending}, true))
	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{
		ClosedBy: CSVFlag{Values: []string{"alice"}},
	}, true))
	require.False(t, shouldDeferInitialWatchEnrichment(&CLI{
		MergedBy: CSVFlag{Values: []string{"alice"}},
	}, true))
}
