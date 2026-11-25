package eventstream_test

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/gechr/prl/internal/eventstream"
	"github.com/stretchr/testify/require"
)

func TestFragmentedEventStream(t *testing.T) {
	const stream = "\ufeff: keepalive\r\n" +
		"event: example\rdata: first\r\ndata: second\n\n" +
		"event: incomplete\ndata: discarded"
	reader := eventstream.NewFrameReader(iotest.OneByteReader(strings.NewReader(stream)))
	var comments []string
	reader.OnComment = func(value string) { comments = append(comments, value) }
	frame, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, eventstream.Frame{Event: "example", Data: "first\nsecond"}, frame)
	require.Equal(t, []string{"keepalive"}, comments)
	_, err = reader.Next()
	require.ErrorIs(t, err, io.EOF)
}
