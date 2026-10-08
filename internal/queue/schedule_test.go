package queue

import (
	"testing"
	"time"
)

func ticks(t *testing.T, expr, tz string, from time.Time, n int) []string {
	t.Helper()
	c, err := ParseCron(expr, tz)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for at := from; len(out) < n; {
		at = c.Next(at)
		out = append(out, at.Format("01-02 15:04 -07:00"))
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// US clocks fall back on 2026-11-01: 01:00-02:00 local happens twice.
func TestCronFallBackFiresEachWallClockTimeOnce(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	// A daily 01:30 job runs once that day, not twice.
	eq(t, ticks(t, "30 1 * * *", "America/New_York", time.Date(2026, 10, 31, 12, 0, 0, 0, ny), 3),
		[]string{"11-01 01:30 -04:00", "11-02 01:30 -05:00", "11-03 01:30 -05:00"})
	// Every 30 minutes: the repeated 01:00 and 01:30 don't fire again.
	eq(t, ticks(t, "*/30 * * * *", "America/New_York", time.Date(2026, 11, 1, 0, 45, 0, 0, ny), 4),
		[]string{"11-01 01:00 -04:00", "11-01 01:30 -04:00", "11-01 02:00 -05:00", "11-01 02:30 -05:00"})
}

// US clocks spring forward on 2026-03-08: 02:00-03:00 local doesn't exist.
func TestCronSpringForwardSkipsNonexistentTimes(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	// 02:30 doesn't exist on 03-08, so that day has no tick (documented on Cron).
	eq(t, ticks(t, "30 2 * * *", "America/New_York", time.Date(2026, 3, 7, 12, 0, 0, 0, ny), 2),
		[]string{"03-09 02:30 -04:00", "03-10 02:30 -04:00"})
	eq(t, ticks(t, "*/30 * * * *", "America/New_York", time.Date(2026, 3, 8, 1, 15, 0, 0, ny), 3),
		[]string{"03-08 01:30 -05:00", "03-08 03:00 -04:00", "03-08 03:30 -04:00"})
}

func TestCronKolkataDaily(t *testing.T) {
	ist, _ := time.LoadLocation("Asia/Kolkata")
	eq(t, ticks(t, "0 9 * * *", "Asia/Kolkata", time.Date(2026, 10, 8, 10, 0, 0, 0, ist), 2),
		[]string{"10-09 09:00 +05:30", "10-10 09:00 +05:30"})
}

func TestParseCronRejects(t *testing.T) {
	for _, c := range []struct{ expr, tz string }{
		{"not cron", "Asia/Kolkata"},
		{"* * * * *", "Mars/Olympus"},
		{"* * * * *", ""},
		{"* * * * *", "Local"},
		{"CRON_TZ=UTC * * * * *", "Asia/Kolkata"},
	} {
		if _, err := ParseCron(c.expr, c.tz); err == nil {
			t.Errorf("ParseCron(%q, %q) accepted", c.expr, c.tz)
		}
	}
}
