package console

import (
	"fmt"
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The level ladder is the window's one moving part, so its geometry is worth
// pinning down: a fixed number of steps that do not stretch to whatever width
// the layout offers, with the number of lit steps following the level it was
// given. The test reads the pixels the toolkit drew, so it checks what the
// window shows rather than what the code meant.
func TestLevelLadderGeometry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fraction  float64
		wantLit   int
		wantSteps int
	}{
		{"silence", 0, 0, ladderSegments},
		{"barely audible", 0.01, 1, ladderSegments},
		{"speech", 0.62, 15, ladderSegments},
		{"loud", 0.99, 24, ladderSegments},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ladderLit(tc.fraction); got != tc.wantLit {
				t.Errorf("ladderLit(%.2f) = %d, want %d", tc.fraction, got, tc.wantLit)
			}
			lit, steps := renderLadder(t, tc.fraction)
			if steps != tc.wantSteps {
				t.Errorf("drew %d steps, want %d", steps, tc.wantSteps)
			}
			if lit != tc.wantLit {
				t.Errorf("lit %d steps for %.2f, want %d", lit, tc.fraction, tc.wantLit)
			}
		})
	}
}

// renderLadder draws one ladder on the dark palette and reports how many
// steps were drawn and how many of them were lit. Steps are runs of pixels
// that are not the background between them, and each step is judged by its
// centre pixel, which the toolkit draws in the plain colour rather than in an
// antialiased blend.
func renderLadder(t *testing.T, fraction float64) (lit, steps int) {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		th, p := theme(c.Theme())
		c.SetTheme(th)
		ui.Column(c).Padding(10).Children(func() {
			levelLadder(c, p, fraction, 0, true)
		})
	}, 600, 60)
	tester.SetScale(1)
	tester.SetDark(true)
	tester.Frame()

	img := tester.Image()
	background := keyOfColour(darkPalette.chassis)
	track := keyOfColour(darkPalette.track)

	for y := 0; y < img.Bounds().Dy(); y++ {
		runSteps, runLit := 0, 0
		start, end := 0, 0
		for x := 0; x <= img.Bounds().Dx(); x++ {
			isBackground := x == img.Bounds().Dx() || keyOfPixel(img, x, y) == background
			if isBackground {
				if end > start {
					runSteps++
					if keyOfPixel(img, (start+end)/2, y) != track {
						runLit++
					}
				}
				start, end = x+1, x+1
				continue
			}
			end = x + 1
		}
		if runSteps == ladderSegments {
			return runLit, runSteps
		}
	}
	return 0, 0
}

func keyOfPixel(img *image.RGBA, x, y int) string {
	r, g, b, _ := img.At(x, y).RGBA()
	return fmt.Sprintf("%02x%02x%02x", r>>8, g>>8, b>>8)
}

// keyOfColour compares colours by their channels only: the toolkit's Color
// holds alpha in its own scale, which the key has no business comparing.
func keyOfColour(c ui.Color) string {
	return fmt.Sprintf("%02x%02x%02x", uint8(c.R), uint8(c.G), uint8(c.B))
}
