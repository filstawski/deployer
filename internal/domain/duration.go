package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var dayPrefix = regexp.MustCompile(`^(\d+)([dw])(.*)$`)

// ParseDuration parses Go durations ("90m", "4h30m") and also accepts whole
// days and weeks ("2d", "1w", "1d12h"), which suit preview lifetimes.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var total time.Duration
	if m := dayPrefix.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n > 3650 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		unit := 24 * time.Hour
		if m[2] == "w" {
			unit *= 7
		}
		total = time.Duration(n) * unit
		s = m[3]
		if s == "" {
			return total, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (examples: 30m, 4h, 2d)", s)
	}
	return total + d, nil
}

// FormatDuration renders a duration compactly for people: "2d", "1d 4h",
// "45m", "30s". Precision is reduced to the two most significant units.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}
	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	minutes := int(d % time.Hour / time.Minute)
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
