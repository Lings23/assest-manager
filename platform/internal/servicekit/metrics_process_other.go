//go:build !linux

package servicekit

// Docker services run on Linux. Unsupported hosts report explicit availability
// rather than substituting Go Sys or a misleading zero process RSS/CPU value.
func readProcessStats() processStats { return processStats{} }
