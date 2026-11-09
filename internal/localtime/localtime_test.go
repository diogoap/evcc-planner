package localtime

import (
	"testing"
	"time"
)

func ams(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestAtNormalAndDST(t *testing.T) {
	loc := ams(t)
	cases := []struct {
		name, date, hhmm string
		want             string // RFC3339
	}{
		{"winter", "2026-11-10", "08:15", "2026-11-10T08:15:00+01:00"},
		{"summer", "2026-07-01", "08:15", "2026-07-01T08:15:00+02:00"},
		{"spring gap moves forward", "2026-03-29", "02:30", "2026-03-29T03:30:00+02:00"},
		{"autumn ambiguity picks first", "2026-10-25", "02:30", "2026-10-25T02:30:00+02:00"},
		{"after autumn change", "2026-10-25", "08:15", "2026-10-25T08:15:00+01:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := At(loc, c.date, c.hhmm)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := time.Parse(time.RFC3339, c.want)
			if !got.Equal(want) {
				t.Fatalf("At(%s %s) = %s, want %s", c.date, c.hhmm, got.Format(time.RFC3339), c.want)
			}
		})
	}
}

func TestAtRejectsBadInput(t *testing.T) {
	loc := ams(t)
	for _, in := range [][2]string{{"2026-13-01", "08:00"}, {"2026-11-10", "25:00"}, {"10-11-2026", "08:00"}} {
		if _, err := At(loc, in[0], in[1]); err == nil {
			t.Errorf("At(%q, %q) succeeded, want error", in[0], in[1])
		}
	}
}

func TestAddDaysAcrossDST(t *testing.T) {
	loc := ams(t)
	sat := time.Date(2026, 10, 24, 0, 0, 0, 0, loc)
	sun := AddDays(sat, 1)
	mon := AddDays(sat, 2)
	if got := mon.Sub(sun); got != 25*time.Hour {
		t.Fatalf("autumn Sunday lasts %s, want 25h", got)
	}
	if sun.Hour() != 0 || mon.Hour() != 0 {
		t.Fatalf("AddDays should land on local midnight, got %s and %s", sun, mon)
	}
	spring := AddDays(time.Date(2026, 3, 29, 0, 0, 0, 0, loc), 1)
	if got := spring.Sub(time.Date(2026, 3, 29, 0, 0, 0, 0, loc)); got != 23*time.Hour {
		t.Fatalf("spring Sunday lasts %s, want 23h", got)
	}
}

func TestISOWeekday(t *testing.T) {
	loc := ams(t)
	if got := ISOWeekday(time.Date(2026, 11, 9, 12, 0, 0, 0, loc)); got != 1 {
		t.Fatalf("Monday = %d, want 1", got)
	}
	if got := ISOWeekday(time.Date(2026, 11, 15, 12, 0, 0, 0, loc)); got != 7 {
		t.Fatalf("Sunday = %d, want 7", got)
	}
}
