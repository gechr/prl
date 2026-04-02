package term

import (
	"os"

	"golang.org/x/term"
)

// Is returns true if the given file is a terminal.
// Returns false for nil files.
func Is(f *os.File) bool {
	if f == nil {
		return false
	}
	//nolint:gosec // file descriptor fits in int
	return term.IsTerminal(int(f.Fd()))
}

// Width returns the width of the terminal connected to f.
// Returns 0 if f is nil or not a terminal.
func Width(f *os.File) int {
	w, _ := Size(f)
	return w
}

// Size returns the width and height of the terminal connected to f.
// Returns zeros if f is nil or not a terminal.
func Size(f *os.File) (int, int) {
	if f == nil {
		return 0, 0
	}
	//nolint:gosec // file descriptor fits in int
	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0
	}
	return w, h
}
