package console

import (
	"fmt"
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Every page of every demo situation must draw, and the rail must switch
// pages: the frames after a switch run the page transitions, and a tester
// panics on keys two siblings share, so this guards the views' keys.
func TestPagesRender(t *testing.T) {
	for _, kind := range []DemoKind{DemoLive, DemoV1, DemoMobile, DemoNoInterface, DemoNoDriver} {
		t.Run(situationName(kind), func(t *testing.T) {
			app := New(NewDemo(kind))
			tester := ui.NewTester(app.View, 1180, 820)
			tester.SetScale(1)
			tester.SetDark(true)
			tester.Frame()
			for _, step := range []struct {
				label string
				page  int
			}{
				{"设置", 1}, {"驱动", 2}, {"状态", 0}, {"驱动", 2},
			} {
				if err := tester.Click(step.label); err != nil {
					t.Fatalf("click %q: %v", step.label, err)
				}
				tester.Frame()
				tester.Frame()
				if app.page != step.page {
					t.Fatalf("click %q selected page %d, want %d", step.label, app.page, step.page)
				}
			}
		})
	}
}

func situationName(kind DemoKind) string {
	switch kind {
	case DemoLive:
		return "live"
	case DemoV1:
		return "v1"
	case DemoMobile:
		return "mobile"
	case DemoNoInterface:
		return "no-interface"
	default:
		return "no-driver"
	}
}

// The battery row belongs to devices that actually report a gauge: the
// standard receiver does, while the mobile receiver (DMMR02) has no battery
// and passes no telemetry through — its page must state that rather than
// show a placeholder.
func TestBatteryRowsFollowTheGauge(t *testing.T) {
	render := func(kind DemoKind) *ui.Tester {
		tester := ui.NewTester(New(NewDemo(kind)).View, 1180, 900)
		tester.SetScale(1)
		tester.SetDark(true)
		tester.Frame()
		return tester
	}

	mobile := render(DemoMobile)
	if mobile.HasText("电量未知") {
		t.Error("the mobile receiver page still shows a battery placeholder")
	}
	if !mobile.HasText("由 USB-C 供电") {
		t.Error("the mobile receiver page does not say the receiver is USB-C powered")
	}

	live := render(DemoLive)
	if live.HasText("电量未知") {
		t.Error("the standard receiver page shows a battery placeholder")
	}
	if !live.HasText("83%") {
		t.Error("the standard receiver page does not show the receiver battery")
	}
	if !live.HasText("在盒中充电") {
		t.Error("the standard receiver page does not show the charging transmitter")
	}
}

// The panels of the status page rows must end on one line: a card that stops
// short of its neighbours reads as a mistake, so the row stretches them to a
// common height and the identity rows sit at its bottom.
func TestStatusCardsShareOneHeight(t *testing.T) {
	for _, kind := range []DemoKind{DemoLive, DemoMobile, DemoV1} {
		t.Run(situationName(kind), func(t *testing.T) {
			app := New(NewDemo(kind))
			tester := ui.NewTester(app.View, 1180, 820)
			tester.SetScale(1)
			tester.SetDark(true)
			tester.Frame()
			img := tester.Image()

			// On v2 the receiver card joins the row; on v1 it is a strip
			// of its own above the two transmitter cards.
			titles := []string{"发射器 1", "发射器 2"}
			if kind != DemoV1 {
				titles = append([]string{"接收器"}, titles...)
			}
			bottoms := make([]int, 0, len(titles))
			for _, title := range titles {
				rect, ok := tester.Find(title)
				if !ok {
					t.Fatalf("no card titled %q", title)
				}
				bottoms = append(bottoms, cardBottom(img, rect.X+rect.W/2))
			}
			for i := 1; i < len(bottoms); i++ {
				if bottoms[i] != bottoms[0] {
					t.Errorf("card bottoms = %v, want one line", bottoms)
				}
			}
		})
	}
}

// cardBottom is the lowest row of the column x that still belongs to a card,
// found by the panel's background and border colours, not the chassis behind
// it.
func cardBottom(img *image.RGBA, x float32) int {
	panel := keyOfColour(darkPalette.panel)
	line := keyOfColour(darkPalette.line)
	bottom := -1
	for y := 0; y < img.Bounds().Dy(); y++ {
		if k := keyOfPixel(img, int(x), y); k == panel || k == line {
			bottom = y
		}
	}
	return bottom
}

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
			levelLadder(c, p, fraction, true)
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
