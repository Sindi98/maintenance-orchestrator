package window_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Sindi98/maintenance-orchestrator/api/v1alpha1"
	"github.com/Sindi98/maintenance-orchestrator/internal/window"
)

func mkWindow(cron string, dur time.Duration, tz string) v1alpha1.Window {
	return v1alpha1.Window{Cron: cron, Duration: metav1.Duration{Duration: dur}, TimeZone: tz}
}

func mustTime(t *testing.T, layout, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

// TestIsOpenStepHours is the regression test for the "N/step" cron bug: an
// open-ended step such as "9/2" must match 9,11,13,...,23, not just hour 9.
func TestIsOpenStepHours(t *testing.T) {
	// Every 2 hours starting at 09:00, each opening lasting 30 minutes.
	w := mkWindow("0 9/2 * * *", 30*time.Minute, "UTC")

	open := []string{"2026-06-30T09:10:00Z", "2026-06-30T11:00:00Z", "2026-06-30T23:15:00Z"}
	for _, ts := range open {
		now := mustTime(t, time.RFC3339, ts)
		got, err := window.IsOpen(w, now)
		if err != nil {
			t.Fatalf("IsOpen(%s): %v", ts, err)
		}
		if !got {
			t.Errorf("IsOpen(%s) = false, want true (N/step must expand to every step)", ts)
		}
	}

	closed := []string{"2026-06-30T10:00:00Z", "2026-06-30T09:45:00Z", "2026-06-30T08:59:00Z"}
	for _, ts := range closed {
		now := mustTime(t, time.RFC3339, ts)
		got, err := window.IsOpen(w, now)
		if err != nil {
			t.Fatalf("IsOpen(%s): %v", ts, err)
		}
		if got {
			t.Errorf("IsOpen(%s) = true, want false", ts)
		}
	}
}

// TestIsOpenStepMinutes covers the minute field, where "0/15" must mean
// 0,15,30,45 rather than only minute 0.
func TestIsOpenStepMinutes(t *testing.T) {
	w := mkWindow("0/15 * * * *", time.Minute, "UTC")
	for _, m := range []string{"00", "15", "30", "45"} {
		now := mustTime(t, time.RFC3339, "2026-06-30T10:"+m+":00Z")
		got, err := window.IsOpen(w, now)
		if err != nil {
			t.Fatalf("IsOpen: %v", err)
		}
		if !got {
			t.Errorf("IsOpen at minute %s = false, want true", m)
		}
	}
	// Minute 7 is not a multiple of 15.
	now := mustTime(t, time.RFC3339, "2026-06-30T10:07:00Z")
	if got, _ := window.IsOpen(w, now); got {
		t.Error("IsOpen at minute 07 = true, want false")
	}
}

// TestStepRangeAndStarStillWork guards the unchanged step forms.
func TestStepRangeAndStarStillWork(t *testing.T) {
	// "*/2" hours -> even hours.
	w := mkWindow("0 */2 * * *", time.Minute, "UTC")
	if got, _ := window.IsOpen(w, mustTime(t, time.RFC3339, "2026-06-30T08:00:00Z")); !got {
		t.Error("*/2 should match hour 8")
	}
	if got, _ := window.IsOpen(w, mustTime(t, time.RFC3339, "2026-06-30T09:00:00Z")); got {
		t.Error("*/2 should not match hour 9")
	}
	// "8-16/4" hours -> 8,12,16.
	w2 := mkWindow("0 8-16/4 * * *", time.Minute, "UTC")
	for _, h := range []string{"08", "12", "16"} {
		if got, _ := window.IsOpen(w2, mustTime(t, time.RFC3339, "2026-06-30T"+h+":00:00Z")); !got {
			t.Errorf("8-16/4 should match hour %s", h)
		}
	}
	if got, _ := window.IsOpen(w2, mustTime(t, time.RFC3339, "2026-06-30T10:00:00Z")); got {
		t.Error("8-16/4 should not match hour 10")
	}
}

// TestNextOpenStep confirms NextOpen honours the step too.
func TestNextOpenStep(t *testing.T) {
	w := mkWindow("0 9/2 * * *", 30*time.Minute, "UTC")
	now := mustTime(t, time.RFC3339, "2026-06-30T10:00:00Z")
	next, err := window.NextOpen(w, now)
	if err != nil {
		t.Fatalf("NextOpen: %v", err)
	}
	// The next opening after 10:00 is 11:00, not the next day's 09:00.
	if next.Hour() != 11 || next.Minute() != 0 {
		t.Errorf("NextOpen = %s, want 11:00", next.Format(time.RFC3339))
	}
}

func TestBareValueWithoutStepMatchesOnlyThatValue(t *testing.T) {
	// "9" (no step) must match only hour 9.
	w := mkWindow("0 9 * * *", time.Hour, "UTC")
	if got, _ := window.IsOpen(w, mustTime(t, time.RFC3339, "2026-06-30T11:00:00Z")); got {
		t.Error("bare hour 9 should not match hour 11")
	}
	if got, _ := window.IsOpen(w, mustTime(t, time.RFC3339, "2026-06-30T09:30:00Z")); !got {
		t.Error("bare hour 9 should match 09:30 within the 1h window")
	}
}
