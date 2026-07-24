package jiggler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestTrajectoryInvariants(t *testing.T) {
	from, to := Point{X: 120, Y: 180}, Point{X: 1320, Y: 710}
	points := Trajectory(from, to, 1600, 900, rand.New(rand.NewSource(17017)))
	if len(points) < 10 {
		t.Fatalf("points = %d", len(points))
	}
	total := 0
	pathLength := 0.0
	previous := from
	for index, point := range points {
		if point.DelayMS <= 0 {
			t.Fatalf("point %d delay = %d", index, point.DelayMS)
		}
		total += point.DelayMS
		if point.X < 2 || point.X > 1597 || point.Y < 2 || point.Y > 897 {
			t.Fatalf("point %d outside display: %+v", index, point)
		}
		pathLength += math.Hypot(float64(point.X-previous.X), float64(point.Y-previous.Y))
		previous = Point{X: point.X, Y: point.Y}
	}
	last := points[len(points)-1]
	if last.X != to.X || last.Y != to.Y {
		t.Fatalf("last = (%d,%d), want %+v", last.X, last.Y, to)
	}
	if total < 250 || total > 2200 {
		t.Fatalf("duration = %dms", total)
	}
	straight := distance(from, to)
	if pathLength < straight || pathLength > 1.35*straight {
		t.Fatalf("path length = %.2f, straight = %.2f", pathLength, straight)
	}
}

func TestTrajectoryVelocityHasBallisticPeak(t *testing.T) {
	from, to := Point{X: 100, Y: 400}, Point{X: 1400, Y: 400}
	points := Trajectory(from, to, 1600, 900, rand.New(rand.NewSource(42)))
	speeds := make([]float64, len(points))
	previous := from
	peakIndex := 0
	for index, point := range points {
		speeds[index] = math.Hypot(float64(point.X-previous.X), float64(point.Y-previous.Y)) /
			float64(point.DelayMS)
		if speeds[index] > speeds[peakIndex] {
			peakIndex = index
		}
		previous = Point{X: point.X, Y: point.Y}
	}
	if peakIndex < len(points)/5 || peakIndex > len(points)*4/5 {
		t.Fatalf("velocity peak at %d of %d", peakIndex, len(points))
	}
	window := max(2, len(points)/10)
	average := func(values []float64) float64 {
		sum := 0.0
		for _, value := range values {
			sum += value
		}
		return sum / float64(len(values))
	}
	middle := speeds[len(points)/2-window/2 : len(points)/2+window/2]
	if average(middle) <= average(speeds[:window]) || average(middle) <= average(speeds[len(speeds)-window:]) {
		t.Fatalf("velocity is not bell-shaped: start %.2f middle %.2f end %.2f",
			average(speeds[:window]), average(middle), average(speeds[len(speeds)-window:]))
	}
}

func TestTrajectorySeededOutputIsStable(t *testing.T) {
	build := func() []TimedPoint {
		return Trajectory(Point{X: 50, Y: 70}, Point{X: 700, Y: 500}, 800, 600,
			rand.New(rand.NewSource(20260723)))
	}
	first, second := build(), build()
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("same seed produced different trajectories")
	}
	got := fmt.Sprintf("%x", sha256.Sum256(firstJSON))
	const want = "e21157e65e811c42e9c5088574db30779ed47536f264e6682e5d97aff7b613b0"
	if got != want {
		t.Fatalf("seeded trajectory digest = %s, want %s", got, want)
	}
}
