package main

import "testing"

func TestPprofListenAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "false", input: "false", want: ""},
		{name: "zero", input: "0", want: ""},
		{name: "default_true", input: "true", want: defaultPprofAddr},
		{name: "default_one", input: "1", want: defaultPprofAddr},
		{name: "explicit_addr", input: "127.0.0.1:7000", want: "127.0.0.1:7000"},
		{name: "trimmed_addr", input: " 127.0.0.1:7001 ", want: "127.0.0.1:7001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pprofListenAddr(tt.input); got != tt.want {
				t.Fatalf("pprofListenAddr(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
