package ui

import (
	"os"
	"strconv"
	"strings"
	"time"
)

var cachedBootTime time.Time

// bootTime returns the system boot instant, derived from /proc/uptime and
// cached. A failure is not worth reporting: uptime is decoration, and zero
// simply renders as a large number rather than breaking the layout.
func bootTime() uint64 {
	if !cachedBootTime.IsZero() {
		return uint64(cachedBootTime.Unix())
	}
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	cachedBootTime = time.Now().Add(-time.Duration(secs * float64(time.Second)))
	return uint64(cachedBootTime.Unix())
}

// hostName returns the short hostname, or an error if it cannot be read.
func hostName() (string, error) { return os.Hostname() }
