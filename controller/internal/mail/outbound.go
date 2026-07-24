package mail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes the sendmail-compatible submission command.
type Runner interface {
	Run(context.Context, string, []string, []byte) error
}

// ExecRunner executes real operating-system commands.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, path string, args []string, input []byte) error {
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	command.Stderr = &output
	err := command.Run()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(output.String()), err)
	}
	return nil
}

// Submit pipes a message to a sendmail-compatible binary.
func Submit(ctx context.Context, runner Runner, path, envelopeFrom string, recipients []string, message []byte) error {
	args := []string{"-i", "-f", envelopeFrom}
	args = append(args, recipients...)
	return runner.Run(ctx, path, args, message)
}

// TestImage returns a deterministic 320x180 PNG with a gradient and sine curve.
func TestImage() ([]byte, error) {
	const width, height = 320, 180
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			ratio := float64(x+y) / float64(width+height-2)
			canvas.SetRGBA(x, y, color.RGBA{
				R: uint8(35 + 55*ratio),
				G: uint8(68 + 105*ratio),
				B: uint8(155 + 75*ratio),
				A: 255,
			})
		}
	}
	for x := range width {
		y := height/2 + int(math.Sin(float64(x)*2*math.Pi/80)*42)
		for offset := -2; offset <= 2; offset++ {
			if point := y + offset; point >= 0 && point < height {
				canvas.SetRGBA(x, point, color.RGBA{R: 250, G: 211, B: 92, A: 255})
			}
		}
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// KeyPath returns the persistent DKIM key location.
func KeyPath(dataDir string) string { return filepath.Join(dataDir, "mail", "dkim.key") }
