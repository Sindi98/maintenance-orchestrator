// Package window evaluates maintenance windows. A window is a 5-field cron
// expression marking each opening instant plus a duration for how long it stays
// open, evaluated in an optional IANA timezone (default UTC). The package is
// pure (no cluster I/O) and therefore directly unit-testable.
package window

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
)

// maxLookaheadMinutes bounds NextOpen scanning to roughly one year.
const maxLookaheadMinutes = 366 * 24 * 60

type schedule struct {
	minutes map[int]bool
	hours   map[int]bool
	doms    map[int]bool
	months  map[int]bool
	dows    map[int]bool
	domStar bool
	dowStar bool
}

func parseField(field string, min, max int) (map[int]bool, error) {
	out := make(map[int]bool)
	for _, part := range strings.Split(field, ",") {
		step := 1
		rangePart := part
		hasStep := false
		if i := strings.Index(part, "/"); i >= 0 {
			hasStep = true
			rangePart = part[:i]
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s <= 0 {
				return nil, fmt.Errorf("invalid step in field %q", part)
			}
			step = s
		}

		var lo, hi int
		switch {
		case rangePart == "*":
			lo, hi = min, max
		case strings.Contains(rangePart, "-"):
			bounds := strings.SplitN(rangePart, "-", 2)
			a, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, fmt.Errorf("invalid range start in %q", part)
			}
			b, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, fmt.Errorf("invalid range end in %q", part)
			}
			lo, hi = a, b
		default:
			v, err := strconv.Atoi(rangePart)
			if err != nil {
				return nil, fmt.Errorf("invalid value %q", part)
			}
			// Standard cron: a bare value with a step ("N/step", e.g. "9/2")
			// means "from N to the field maximum, stepping by step"; without a
			// step it matches only N. Treating "N/step" as the single value N
			// would silently drop every later occurrence.
			lo = v
			hi = v
			if hasStep {
				hi = max
			}
		}

		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("field value %q out of range [%d-%d]", part, min, max)
		}
		for i := lo; i <= hi; i += step {
			out[i] = true
		}
	}
	return out, nil
}

func parseCron(expr string) (*schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d in %q", len(fields), expr)
	}

	s := &schedule{}
	var err error
	if s.minutes, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}
	if s.hours, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}
	if s.doms, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}
	if s.months, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}
	if s.dows, err = parseField(fields[4], 0, 7); err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}
	if s.dows[7] { // cron allows 7 as Sunday
		s.dows[0] = true
	}
	s.domStar = fields[2] == "*"
	s.dowStar = fields[4] == "*"
	return s, nil
}

func (s *schedule) matches(t time.Time) bool {
	if !s.minutes[t.Minute()] || !s.hours[t.Hour()] || !s.months[int(t.Month())] {
		return false
	}
	domMatch := s.doms[t.Day()]
	dowMatch := s.dows[int(t.Weekday())]

	// Standard cron day-of-month / day-of-week semantics: when both are
	// restricted, a match on either is sufficient; otherwise use whichever is
	// restricted.
	switch {
	case !s.domStar && !s.dowStar:
		return domMatch || dowMatch
	case !s.domStar:
		return domMatch
	case !s.dowStar:
		return dowMatch
	default:
		return true
	}
}

func location(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(tz)
}

// IsOpen reports whether now falls inside an opening of the window, i.e. there
// exists a cron firing instant t with t <= now < t+duration.
func IsOpen(w v1alpha1.Window, now time.Time) (bool, error) {
	s, err := parseCron(w.Cron)
	if err != nil {
		return false, err
	}
	loc, err := location(w.TimeZone)
	if err != nil {
		return false, fmt.Errorf("invalid timeZone %q: %w", w.TimeZone, err)
	}
	dur := w.Duration.Duration
	if dur <= 0 {
		return false, fmt.Errorf("window duration must be > 0")
	}

	nowLoc := now.In(loc)
	cursor := nowLoc.Truncate(time.Minute)
	steps := int(dur/time.Minute) + 1
	for i := 0; i <= steps; i++ {
		t := cursor.Add(-time.Duration(i) * time.Minute)
		if s.matches(t) && t.Add(dur).After(nowLoc) {
			return true, nil
		}
	}
	return false, nil
}

// NextOpen returns the next instant strictly after now at which the window opens.
func NextOpen(w v1alpha1.Window, now time.Time) (time.Time, error) {
	s, err := parseCron(w.Cron)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := location(w.TimeZone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timeZone %q: %w", w.TimeZone, err)
	}

	t := now.In(loc).Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < maxLookaheadMinutes; i++ {
		if s.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no window opening found within one year for cron %q", w.Cron)
}

// AnyOpen reports whether now falls inside any of the given windows. An empty
// list means "always open" and returns true.
func AnyOpen(windows []v1alpha1.Window, now time.Time) (bool, error) {
	if len(windows) == 0 {
		return true, nil
	}
	for i := range windows {
		open, err := IsOpen(windows[i], now)
		if err != nil {
			return false, err
		}
		if open {
			return true, nil
		}
	}
	return false, nil
}

// NextOpenAny returns the soonest opening across the given windows.
func NextOpenAny(windows []v1alpha1.Window, now time.Time) (time.Time, error) {
	var best time.Time
	found := false
	for i := range windows {
		next, err := NextOpen(windows[i], now)
		if err != nil {
			return time.Time{}, err
		}
		if !found || next.Before(best) {
			best = next
			found = true
		}
	}
	if !found {
		return time.Time{}, fmt.Errorf("no windows provided")
	}
	return best, nil
}
