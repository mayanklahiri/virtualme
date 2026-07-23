package mail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// QueueEntry describes one queued dma message.
type QueueEntry struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	AgeSec int64  `json:"ageSec"`
}

// Queue lists and groups dma spool files by queue identifier.
func Queue(directory string, now time.Time) ([]QueueEntry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	type accumulated struct {
		size int64
		mod  time.Time
	}
	grouped := make(map[string]accumulated)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "flush" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		id := entry.Name()
		if len(id) > 1 && (id[0] == 'M' || id[0] == 'Q') {
			id = id[1:]
		}
		item := grouped[id]
		item.size += info.Size()
		if item.mod.IsZero() || info.ModTime().Before(item.mod) {
			item.mod = info.ModTime()
		}
		grouped[id] = item
	}
	result := make([]QueueEntry, 0, len(grouped))
	for id, item := range grouped {
		age := now.Sub(item.mod).Seconds()
		if age < 0 {
			age = 0
		}
		result = append(result, QueueEntry{ID: id, Size: item.size, AgeSec: int64(age)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
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
