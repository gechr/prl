package human

import (
	"fmt"
	"time"
)

// Time arithmetic constants.
const (
	SecondsPerMinute = 60

	MinutesPerHour = 60

	HoursPerDay = 24

	DaysPerWeek  = 7
	DaysPerMonth = 30
	DaysPerYear  = 365

	WeeksPerMonth = 4
	WeeksPerYear  = 52

	MonthsPerYear = 12
)

// FormatTimeAgo formats a time as a human-readable relative string (plain text).
func FormatTimeAgo(t time.Time) string {
	return FormatTimeAgoFrom(t, time.Now().UTC())
}

// FormatTimeAgoFrom formats a time relative to the given reference time now.
func FormatTimeAgoFrom(t, now time.Time) string {
	v := decompose(t, now)
	return formatDuration(
		v.seconds,
		v.minutes,
		v.hours,
		v.days,
		v.weeks,
		v.months,
		v.years,
		v.future,
	)
}

// FormatTimeAgoCompact formats a time as a compact relative string (e.g. "15m ago").
func FormatTimeAgoCompact(t time.Time) string {
	return FormatTimeAgoCompactFrom(t, time.Now().UTC())
}

// FormatTimeAgoCompactFrom formats a time as a compact relative string relative to now.
func FormatTimeAgoCompactFrom(t, now time.Time) string {
	v := decompose(t, now)
	return formatDurationCompact(
		v.seconds,
		v.minutes,
		v.hours,
		v.days,
		v.weeks,
		v.months,
		v.years,
		v.future,
	)
}

type decomposed struct {
	seconds, minutes, hours, days, weeks, months, years int
	future                                              bool
}

func decompose(t, now time.Time) decomposed {
	d := now.Sub(t)
	future := d < 0
	if future {
		d = -d
	}
	hours := int(d.Hours())
	days := hours / HoursPerDay
	return decomposed{
		seconds: int(d.Seconds()),
		minutes: int(d.Minutes()),
		hours:   hours,
		days:    days,
		weeks:   days / DaysPerWeek,
		months:  days / DaysPerMonth,
		years:   days / DaysPerYear,
		future:  future,
	}
}

func formatDurationCompact(
	seconds, minutes, hours, days, weeks, months, years int,
	future bool,
) string {
	wrap := func(n int, unit string) string {
		if future {
			return fmt.Sprintf("in %d%s", n, unit)
		}
		return fmt.Sprintf("%d%s ago", n, unit)
	}

	switch {
	case seconds < SecondsPerMinute:
		return "now"
	case minutes < MinutesPerHour:
		return wrap(minutes, "m")
	case hours < HoursPerDay:
		return wrap(hours, "h")
	case days < DaysPerWeek:
		return wrap(days, "d")
	case weeks <= WeeksPerMonth:
		return wrap(weeks, "w")
	case months < MonthsPerYear:
		return wrap(months, "mo")
	default:
		return wrap(years, "y")
	}
}

func formatDuration(seconds, minutes, hours, days, weeks, months, years int, future bool) string {
	wrap := func(n int, unit string) string {
		if future {
			return fmt.Sprintf("in %d %s", n, unit)
		}
		return fmt.Sprintf("%d %s ago", n, unit)
	}
	wrapOne := func(unit string) string {
		if future {
			return "in 1 " + unit
		}
		return "1 " + unit + " ago"
	}

	switch {
	case seconds < SecondsPerMinute:
		return "now"
	case minutes == 1:
		return wrapOne("minute")
	case minutes < MinutesPerHour:
		return wrap(minutes, "minutes")
	case hours == 1:
		return wrapOne("hour")
	case hours < HoursPerDay:
		return wrap(hours, "hours")
	case days == 1:
		return wrapOne("day")
	case days < DaysPerWeek:
		return wrap(days, "days")
	case weeks == 1:
		return wrapOne("week")
	case weeks <= WeeksPerMonth:
		return wrap(weeks, "weeks")
	case months == 1:
		return wrapOne("month")
	case months < MonthsPerYear:
		return wrap(months, "months")
	case years == 1:
		return wrapOne("year")
	default:
		return wrap(years, "years")
	}
}
