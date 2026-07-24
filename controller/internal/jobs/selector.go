package jobs

import (
	"fmt"
	"strings"
	"time"
)

var dayTokens = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday,
	"sat": time.Saturday,
}

var bucketHours = map[string][2]int{
	"early-morning": {5, 8},
	"morning":       {8, 12},
	"afternoon":     {12, 17},
	"evening":       {17, 21},
	"night":         {21, 24},
	"late-night":    {0, 5},
	"anytime":       {0, 24},
}

// Selector is a parsed day set and wall-clock bucket.
type Selector struct {
	Days   [7]bool
	Bucket string
}

// Parse validates and parses a human time selector.
func Parse(input string) (Selector, error) {
	normalized := strings.ToLower(strings.TrimSpace(input))
	normalized = strings.Join(strings.Fields(normalized), " ")
	normalized = strings.ReplaceAll(normalized, ", ", ",")
	if normalized == "" {
		return Selector{}, fmt.Errorf("empty selector")
	}
	parts := strings.Split(normalized, " ")
	if len(parts) > 2 {
		return Selector{}, fmt.Errorf("invalid selector %q", input)
	}
	var daysPart, bucketPart string
	if _, ok := bucketHours[parts[len(parts)-1]]; ok {
		bucketPart = parts[len(parts)-1]
		if len(parts) == 2 {
			daysPart = parts[0]
		}
	} else {
		if len(parts) != 1 {
			return Selector{}, fmt.Errorf("unknown bucket %q", parts[len(parts)-1])
		}
		daysPart = parts[0]
	}
	if daysPart == "" {
		daysPart = "everyday"
	}
	if bucketPart == "" {
		bucketPart = "anytime"
	}
	result := Selector{Bucket: bucketPart}
	switch daysPart {
	case "everyday":
		for day := range result.Days {
			result.Days[day] = true
		}
	case "weekday":
		for _, day := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday} {
			result.Days[day] = true
		}
	case "weekend":
		result.Days[time.Saturday], result.Days[time.Sunday] = true, true
	default:
		tokens := strings.Split(daysPart, ",")
		if len(tokens) == 0 {
			return Selector{}, fmt.Errorf("invalid days %q", daysPart)
		}
		for _, token := range tokens {
			day, ok := dayTokens[token]
			if !ok {
				return Selector{}, fmt.Errorf("unknown day %q", token)
			}
			if result.Days[day] {
				return Selector{}, fmt.Errorf("duplicate day %q", token)
			}
			result.Days[day] = true
		}
	}
	return result, nil
}

// Matches reports whether t is inside the selector.
func (s Selector) Matches(t time.Time) bool {
	if !s.Days[t.Weekday()] {
		return false
	}
	hours := bucketHours[s.Bucket]
	minute := t.Hour()*60 + t.Minute()
	return minute >= hours[0]*60 && minute < hours[1]*60
}

// BucketWindow returns the current or next concrete occurrence.
func (s Selector) BucketWindow(t time.Time) (time.Time, time.Time) {
	hours, ok := bucketHours[s.Bucket]
	if !ok {
		return time.Time{}, time.Time{}
	}
	for offset := range 8 {
		day := t.AddDate(0, 0, offset)
		if !s.Days[day.Weekday()] {
			continue
		}
		start := time.Date(day.Year(), day.Month(), day.Day(), hours[0], 0, 0, 0, t.Location())
		end := time.Date(day.Year(), day.Month(), day.Day(), hours[1], 0, 0, 0, t.Location())
		if offset > 0 || t.Before(end) {
			return start, end
		}
	}
	return time.Time{}, time.Time{}
}

// ActiveBuckets returns the active bucket, calendar, and universal tokens.
func ActiveBuckets(t time.Time) []string {
	bucket := "anytime"
	for _, candidate := range []string{"late-night", "early-morning", "morning", "afternoon", "evening", "night"} {
		hours := bucketHours[candidate]
		if t.Hour() >= hours[0] && t.Hour() < hours[1] {
			bucket = candidate
			break
		}
	}
	day := strings.ToLower(t.Weekday().String()[:3])
	kind := "weekday"
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		kind = "weekend"
	}
	return []string{bucket, "anytime", day, kind, "everyday"}
}
