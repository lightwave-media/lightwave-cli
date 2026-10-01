package cron

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Interval is one launchd StartCalendarInterval entry. A nil field is a
// wildcard, as launchd reads an absent key.
type Interval struct {
	Minute  *int
	Hour    *int
	Day     *int
	Month   *int
	Weekday *int
}

// maxIntervals bounds what one schedule may expand to. launchd takes a list of
// calendar dicts, so `*/5 * * * *` is twelve entries; a schedule that expands
// past this is refused rather than rendered as hundreds of keys.
const maxIntervals = 96

var aliases = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
}

// The five cron fields, in order.
const (
	fieldMinute = iota
	fieldHour
	fieldDay
	fieldMonth
	fieldWeekday
	cronFields
)

// fieldRange is each cron field's legal values.
var fieldRange = [cronFields][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// Intervals translates a 5-field cron expression into launchd calendar
// intervals: the cross product of every listed minute, hour, day, month and
// weekday, where `*` leaves the field out. Supported per field: `*`, `N`,
// `N,M`, `*/S` and `A-B`. Anything else is refused with the reason, never
// approximated: a job that fires at the wrong time is worse than one sync will
// not render.
func Intervals(schedule string) ([]Interval, error) {
	expr := strings.TrimSpace(schedule)
	if alias, ok := aliases[expr]; ok {
		expr = alias
	}

	fields := strings.Fields(expr)
	if len(fields) != cronFields {
		return nil, fmt.Errorf("schedule %q: want 5 cron fields or @hourly|@daily|@weekly|@monthly", schedule)
	}

	values := make([][]int, cronFields)

	for i, field := range fields {
		v, err := expand(field, fieldRange[i][0], fieldRange[i][1])
		if err != nil {
			return nil, fmt.Errorf("schedule %q field %d: %w", schedule, i+1, err)
		}

		values[i] = v // nil means wildcard
	}

	// cron fires when EITHER a restricted day-of-month or day-of-week matches;
	// one launchd dict with both keys requires BOTH. Rendering it would fire
	// less often than the stamp declares, so it is refused.
	if values[fieldDay] != nil && values[fieldWeekday] != nil {
		return nil, fmt.Errorf("schedule %q restricts both day-of-month and day-of-week, which launchd cannot express", schedule)
	}

	intervals := []Interval{{}}

	for i, v := range values {
		if v == nil {
			continue
		}

		var next []Interval

		for _, base := range intervals {
			for _, n := range v {
				iv := base
				n := n

				switch i {
				case fieldMinute:
					iv.Minute = &n
				case fieldHour:
					iv.Hour = &n
				case fieldDay:
					iv.Day = &n
				case fieldMonth:
					iv.Month = &n
				case fieldWeekday:
					iv.Weekday = &n
				}

				next = append(next, iv)
			}
		}

		if len(next) > maxIntervals {
			return nil, fmt.Errorf("schedule %q expands to more than %d launchd intervals", schedule, maxIntervals)
		}

		intervals = next
	}

	return intervals, nil
}

// expand returns a field's values, or nil for a bare wildcard.
func expand(field string, lo, hi int) ([]int, error) {
	if field == "*" {
		return nil, nil
	}

	set := map[int]bool{}

	for _, part := range strings.Split(field, ",") {
		start, end, step := lo, hi, 1

		rng := part
		if base, s, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad step %q", part)
			}

			step, rng = n, base
		}

		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")

			var err1, err2 error

			start, err1 = strconv.Atoi(a)
			end, err2 = strconv.Atoi(b)

			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad range %q", part)
			}
		default:
			n, err := strconv.Atoi(rng)
			if err != nil {
				return nil, fmt.Errorf("unsupported %q", part)
			}

			start, end = n, n
		}

		if start < lo || end > hi || start > end {
			return nil, fmt.Errorf("%q outside %d-%d", part, lo, hi)
		}

		for n := start; n <= end; n += step {
			set[n] = true
		}
	}

	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}

	sort.Ints(out)

	return out, nil
}

// matches reports whether t (in UTC) is a fire time of intervals.
func matches(intervals []Interval, t time.Time) bool {
	t = t.UTC()

	for _, iv := range intervals {
		if (iv.Minute == nil || *iv.Minute == t.Minute()) &&
			(iv.Hour == nil || *iv.Hour == t.Hour()) &&
			(iv.Day == nil || *iv.Day == t.Day()) &&
			(iv.Month == nil || *iv.Month == int(t.Month())) &&
			(iv.Weekday == nil || *iv.Weekday == int(t.Weekday())) {
			return true
		}
	}

	return false
}

// LastFire is the most recent scheduled fire slot at or before now, in UTC to
// the minute: the window an agent_session's idempotency key names, so every
// fire of one slot, a launchd retry or a manual `lw cron run`, is one task.
// Searches back at most 31 days.
func LastFire(schedule string, now time.Time) (time.Time, error) {
	intervals, err := Intervals(schedule)
	if err != nil {
		return time.Time{}, err
	}

	t := now.UTC().Truncate(time.Minute)
	for range 31 * 24 * 60 {
		if matches(intervals, t) {
			return t, nil
		}

		t = t.Add(-time.Minute)
	}

	return time.Time{}, fmt.Errorf("schedule %q has no fire time in the last 31 days", schedule)
}
