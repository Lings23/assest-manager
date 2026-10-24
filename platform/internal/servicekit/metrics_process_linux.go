package servicekit

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func readProcessStats() processStats {
	result := processStats{}
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		result.cpuSeconds = float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
		result.cpuAvailable = true
	}
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				result.rssBytes = pages * int64(os.Getpagesize())
				result.rssAvailable = true
			}
		}
	}
	return result
}
