package main

import (
	"strings"
	"testing"

	clib "github.com/gechr/clib/cli/kong"
	"github.com/gechr/clib/complete"
	"github.com/stretchr/testify/require"
)

func TestFishCompletionKeepsPluginOrderForAuthorFlag(t *testing.T) {
	var cli CLI
	flags, err := clib.Reflect(&cli)
	require.NoError(t, err)

	gen := complete.NewGenerator("prl").FromFlags(flags)

	var buf strings.Builder
	err = gen.Print(&buf, "fish")
	require.NoError(t, err)

	require.Contains(
		t,
		buf.String(),
		`complete -c prl -s a -l author -k -x -a "(prl --@complete=author)" -d "Author"`,
	)
}
