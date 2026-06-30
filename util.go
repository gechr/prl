package main

import "runtime"

func isDarwin() bool {
	return runtime.GOOS == "darwin"
}
