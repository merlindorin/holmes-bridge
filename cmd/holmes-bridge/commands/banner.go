package commands

import (
	"io"
	"os"
)

// banner is where human-facing startup output goes: stderr, so it stays
// separate from anything a caller might be parsing on stdout, and beside the
// structured logs rather than inside them.
func banner() io.Writer { return os.Stderr }
