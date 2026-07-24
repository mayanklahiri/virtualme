package jiggler

import (
	"math"
	"math/rand"
)

const (
	targetWidth = 40.0
	fittsA      = 120.0
	fittsB      = 160.0
)

// Point is an integer display coordinate.
type Point struct {
	X int
	Y int
}

// TimedPoint is one coordinate and the delay since the preceding coordinate.
type TimedPoint struct {
	X       int
	Y       int
	DelayMS int
}

type vector struct {
	x float64
	y float64
}

func rotate(value vector, angle float64) vector {
	sine, cosine := math.Sincos(angle)
	return vector{x: value.x*cosine - value.y*sine, y: value.x*sine + value.y*cosine}
}

func distance(from, to Point) float64 {
	return math.Hypot(float64(to.X-from.X), float64(to.Y-from.Y))
}

func clampPoint(point Point, width, height int) Point {
	return Point{
		X: max(2, min(width-3, point.X)),
		Y: max(2, min(height-3, point.Y)),
	}
}

func movementTime(from, to Point, rng *rand.Rand) int {
	dist := distance(from, to)
	value := (fittsA + fittsB*math.Log2(dist/targetWidth+1)) * math.Exp(rng.NormFloat64()*0.08)
	return int(math.Round(max(250, min(2200, value))))
}

func sampleDelay(rng *rand.Rand) int {
	// Erlang(shape=8, scale=15/8 ms), bounded to the 12–18 ms hardware cadence.
	sum := 0.0
	for range 8 {
		sum += -math.Log(max(rng.Float64(), math.SmallestNonzeroFloat64))
	}
	return int(math.Round(max(12, min(18, sum*15.0/8.0))))
}

func minimumJerk(tau float64) float64 {
	t2 := tau * tau
	t3 := t2 * tau
	return 10*t3 - 15*t3*tau + 6*t3*t2
}

func minimumJerkDerivative(tau float64) float64 {
	oneMinus := 1 - tau
	return 30 * tau * tau * oneMinus * oneMinus
}

func bezier(start, c1, c2, end vector, t float64) vector {
	u := 1 - t
	return vector{
		x: u*u*u*start.x + 3*u*u*t*c1.x + 3*u*t*t*c2.x + t*t*t*end.x,
		y: u*u*u*start.y + 3*u*u*t*c1.y + 3*u*t*t*c2.y + t*t*t*end.y,
	}
}

func bezierDerivative(start, c1, c2, end vector, t float64) vector {
	u := 1 - t
	return vector{
		x: 3*u*u*(c1.x-start.x) + 6*u*t*(c2.x-c1.x) + 3*t*t*(end.x-c2.x),
		y: 3*u*u*(c1.y-start.y) + 6*u*t*(c2.y-c1.y) + 3*t*t*(end.y-c2.y),
	}
}

func appendSegment(points []TimedPoint, from, to Point, duration, width, height int, curve float64, rng *rand.Rand) []TimedPoint {
	start := vector{x: float64(from.X), y: float64(from.Y)}
	end := vector{x: float64(to.X), y: float64(to.Y)}
	delta := vector{x: end.x - start.x, y: end.y - start.y}
	handedness := 1.0
	if rng.Intn(2) == 0 {
		handedness = -1
	}
	theta1 := handedness * (8 + rng.Float64()*20) * math.Pi / 180 * curve
	theta2 := handedness * (4 + rng.Float64()*10) * math.Pi / 180 * curve
	first := rotate(delta, theta1)
	second := rotate(delta, theta2)
	c1 := vector{x: start.x + .30*first.x, y: start.y + .30*first.y}
	c2 := vector{x: start.x + .72*second.x, y: start.y + .72*second.y}
	frequency := 8 + rng.Float64()*4
	phase := rng.Float64() * 2 * math.Pi
	elapsed := 0
	for elapsed < duration {
		delay := sampleDelay(rng)
		if elapsed+delay > duration {
			delay = duration - elapsed
		}
		elapsed += delay
		tau := float64(elapsed) / float64(duration)
		position := minimumJerk(tau)
		value := bezier(start, c1, c2, end, position)
		if elapsed != duration {
			derivative := bezierDerivative(start, c1, c2, end, position)
			length := math.Hypot(derivative.x, derivative.y)
			if length > 0 {
				normal := vector{x: -derivative.y / length, y: derivative.x / length}
				speed := length * minimumJerkDerivative(tau) * 1000 / float64(duration)
				noise := min(3, .012*speed) * rng.NormFloat64()
				amplitude := 1.2
				if tau >= .15 && tau <= .85 {
					amplitude *= .3
				}
				tremor := amplitude * math.Sin(2*math.Pi*frequency*float64(elapsed)/1000+phase)
				value.x += normal.x * (noise + tremor)
				value.y += normal.y * (noise + tremor)
			}
		}
		point := clampPoint(Point{X: int(math.Round(value.x)), Y: int(math.Round(value.y))}, width, height)
		points = append(points, TimedPoint{X: point.X, Y: point.Y, DelayMS: delay})
	}
	return points
}

// Trajectory synthesizes a deterministic-for-rng humanlike mouse movement.
func Trajectory(from, to Point, width, height int, rng *rand.Rand) []TimedPoint {
	from = clampPoint(from, width, height)
	to = clampPoint(to, width, height)
	duration := movementTime(from, to, rng)
	ballisticTarget := to
	overshoots := rng.Float64() < .7 && distance(from, to) > 1
	if overshoots {
		delta := vector{x: float64(to.X - from.X), y: float64(to.Y - from.Y)}
		length := math.Hypot(delta.x, delta.y)
		amount := length * (.04 + rng.Float64()*.08)
		angle := (rng.Float64()*20 - 10) * math.Pi / 180
		unit := rotate(vector{x: delta.x / length, y: delta.y / length}, angle)
		ballisticTarget = clampPoint(Point{
			X: int(math.Round(float64(to.X) + unit.x*amount)),
			Y: int(math.Round(float64(to.Y) + unit.y*amount)),
		}, width, height)
	}

	points := appendSegment(nil, from, ballisticTarget, duration, width, height, 1, rng)
	if overshoots {
		correctionTarget := to
		secondCorrection := rng.Float64() < .25
		if secondCorrection {
			angle := rng.Float64() * 2 * math.Pi
			radius := 1 + rng.Float64()*3
			correctionTarget = clampPoint(Point{
				X: int(math.Round(float64(to.X) + math.Cos(angle)*radius)),
				Y: int(math.Round(float64(to.Y) + math.Sin(angle)*radius)),
			}, width, height)
		}
		duration2 := 90 + rng.Intn(91)
		points = appendSegment(points, ballisticTarget, correctionTarget, duration2, width, height, .35, rng)
		if secondCorrection {
			points = appendSegment(points, correctionTarget, to, 55+rng.Intn(36), width, height, .15, rng)
		}
	}
	last := &points[len(points)-1]
	last.X, last.Y = to.X, to.Y
	return points
}
