package ui

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestScreenshot renders the dashboard with colour forced on and writes it out
// as ANSI, which is where the README's screenshot comes from.
//
// The image is that text rendered with a monospace font that has the braille and
// box-drawing blocks, so the screenshot is the real view rather than a drawing
// of it. The blank braille cell is invisible in a terminal; some fonts draw its
// empty dot positions as a grid of squares, so whatever renders the dump has to
// skip it.
//
//	SENSORZ_SHOT=1 go test ./internal/ui -run TestScreenshot
func TestScreenshot(t *testing.T) {
	if os.Getenv("SENSORZ_SHOT") == "" {
		t.Skip("set SENSORZ_SHOT to render the screenshot")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := buildLive(t, 120, 40, 4)
	os.WriteFile("/tmp/view.ansi", []byte(m.View()), 0o644)
}
