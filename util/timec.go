package util

import (
	"time"
)

// like RFC1123Z but not necessarily 2 digits for date
const GITRFC = "Mon, 2 Jan 2006 15:04:05 -0700"
const SHORT = "06-01-02 15:04"
const HUMAN = "2006-01-02"

// like Atoi for durations and error handling
func Atod(attr string, err error) (time.Duration, error) {
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(attr)
}
