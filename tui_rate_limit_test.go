package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTUINeedsMergeStatus(t *testing.T) {
	require.False(t, tuiNeedsMergeStatus(&CLI{}))
	require.True(t, tuiNeedsMergeStatus(&CLI{State: valueReady}))
	require.True(t, tuiNeedsMergeStatus(&CLI{CI: ciStatusPending}))
}
