package jobs

import (
	"slices"
	"testing"
	"time"
)

func TestParseExamples(t *testing.T) {
	tests := []struct {
		input  string
		bucket string
		days   []time.Weekday
	}{
		{"weekday morning", "morning", []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}},
		{"fri night", "night", []time.Weekday{time.Friday}},
		{"mon,wed,fri", "anytime", []time.Weekday{time.Monday, time.Wednesday, time.Friday}},
		{"tue, thu morning", "morning", []time.Weekday{time.Tuesday, time.Thursday}},
		{"late-night", "late-night", []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := Parse(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Bucket != test.bucket {
				t.Fatalf("bucket = %q", got.Bucket)
			}
			for day := time.Sunday; day <= time.Saturday; day++ {
				if got.Days[day] != slices.Contains(test.days, day) {
					t.Fatalf("day %s = %v", day, got.Days[day])
				}
			}
		})
	}
}

func TestParseRejectsUnknownAndMalformed(t *testing.T) {
	for _, input := range []string{"", "funday", "weekday brunch", "mon,,fri", "mon mon morning", "mon,mon"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded", input)
		}
	}
}

func TestMatchesBoundaries(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	selector, _ := Parse("weekday morning")
	for _, test := range []struct {
		at   time.Time
		want bool
	}{
		{time.Date(2026, 7, 20, 7, 59, 59, 0, location), false},
		{time.Date(2026, 7, 20, 8, 0, 0, 0, location), true},
		{time.Date(2026, 7, 20, 11, 59, 59, 0, location), true},
		{time.Date(2026, 7, 20, 12, 0, 0, 0, location), false},
		{time.Date(2026, 7, 19, 9, 0, 0, 0, location), false},
	} {
		if got := selector.Matches(test.at); got != test.want {
			t.Errorf("Matches(%s) = %v, want %v", test.at, got, test.want)
		}
	}
}

func TestBucketWindowAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	selector, _ := Parse("everyday early-morning")
	for _, input := range []time.Time{
		time.Date(2026, 3, 8, 1, 30, 0, 0, location),
		time.Date(2026, 11, 1, 1, 30, 0, 0, location),
	} {
		start, end := selector.BucketWindow(input)
		if start.Hour() != 5 || end.Hour() != 8 || start.Location() != location || end.Location() != location {
			t.Fatalf("window at %s = %s–%s", input, start, end)
		}
	}
	after, _ := Parse("mon morning")
	start, _ := after.BucketWindow(time.Date(2026, 7, 20, 13, 0, 0, 0, location))
	if start.Weekday() != time.Monday || start.Day() != 27 {
		t.Fatalf("next window starts %s", start)
	}
}

func TestActiveBuckets(t *testing.T) {
	got := ActiveBuckets(time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC))
	want := []string{"morning", "anytime", "wed", "weekday", "everyday"}
	if !slices.Equal(got, want) {
		t.Fatalf("ActiveBuckets = %v, want %v", got, want)
	}
	got = ActiveBuckets(time.Date(2026, 7, 19, 23, 0, 0, 0, time.UTC))
	if got[0] != "night" || got[3] != "weekend" || got[2] != "sun" {
		t.Fatalf("weekend buckets = %v", got)
	}
}
