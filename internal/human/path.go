package human

import (
	"os"
	"strings"
)

// ContractHome replaces the user's home directory prefix with ~.
func ContractHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if path == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(path, home+"/"); ok {
			return "~/" + rest
		}
	}
	return path
}
