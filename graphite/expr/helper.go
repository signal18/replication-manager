package expr

import (
	"errors"
	"strconv"
)

var errUnknownTimeUnits = errors.New("unknown time units")

// IntervalString converts a sign and string into a number of seconds
func IntervalString(s string, defaultSign int) (int32, error) {

	sign := defaultSign

	switch s[0] {
	case '-':
		sign = -1
		s = s[1:]
	case '+':
		sign = 1
		s = s[1:]
	}

	var totalInterval int32
	for len(s) > 0 {
		var j int
		for j < len(s) && '0' <= s[j] && s[j] <= '9' {
			j++
		}
		var offsetStr string
		offsetStr, s = s[:j], s[j:]

		j = 0
		for j < len(s) && (s[j] < '0' || '9' < s[j]) {
			j++
		}
		var unitStr string
		unitStr, s = s[:j], s[j:]

		var units int
		switch unitStr {
		case "s", "sec", "secs", "second", "seconds":
			units = 1
		case "min", "mins", "minute", "minutes":
			units = 60
		case "h", "hour", "hours":
			units = 60 * 60
		case "d", "day", "days":
			units = 24 * 60 * 60
		case "w", "week", "weeks":
			units = 7 * 24 * 60 * 60
		case "mon", "month", "months":
			units = 30 * 24 * 60 * 60
		case "y", "year", "years":
			units = 365 * 24 * 60 * 60
		default:
			return 0, errUnknownTimeUnits
		}

		offset, err := strconv.Atoi(offsetStr)
		if err != nil {
			return 0, err
		}
		totalInterval += int32(sign * offset * units)
	}

	return totalInterval, nil
}

func TruthyBool(s string) bool {
	switch s {
	case "", "0", "false", "False", "no", "No":
		return false
	case "1", "true", "True", "yes", "Yes":
		return true
	}
	return false
}
