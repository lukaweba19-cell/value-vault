//go:build !windows

package server

import (
	"fmt"
	"os"
)

// ShowErrorDialog is the non-Windows fallback: there is no message box, so
// the fatal startup problem goes to stderr.
func ShowErrorDialog(title, msg string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg)
}
