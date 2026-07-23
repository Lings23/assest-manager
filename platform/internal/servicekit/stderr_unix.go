//go:build !windows

package servicekit

import "os"

func writeStderr(p []byte) (int, error) { return os.Stderr.Write(p) }
