// Package ui draws the receiver console: one window, its pages, and the
// widgets that show what the microphone is doing.
//
// The window is built like the face of a piece of measuring gear rather than
// like a web dashboard. Its colours come from the indicators a receiver
// actually has — jade for a link, amber for signal, coral for a warning — and
// the one loud element is the live level ladder, because the level is the only
// thing on screen that changes on its own. Everything else stays quiet: a
// graphite chassis, hairline rules, right aligned values, tabular figures.
package console

import "github.com/egoist/mygo/ui"

// palette is one appearance's worth of colours. Two exist: the studio (dark)
// and the bench (light), chosen by the desktop's setting.
type palette struct {
	chassis  ui.Color // the window's plate
	panel    ui.Color // a raised panel on it
	panelHi  ui.Color // a panel under the pointer
	sunken   ui.Color // wells: meter tracks, log areas
	line     ui.Color // hairline rules
	lineHi   ui.Color // a rule that marks a boundary
	ink      ui.Color // text
	inkDim   ui.Color // secondary text
	inkFaint ui.Color // tertiary text and units
	signal   ui.Color // the accent: signal, selection, the app's own colour
	onSignal ui.Color // text on the accent
	link     ui.Color // connected
	alarm    ui.Color // failed, low battery
	charge   ui.Color // charging
	track    ui.Color // the unlit part of a meter
}

// darkPalette is used when the desktop is dark, and it is the one the app is
// designed around: a meter reads better against a dark plate.
var darkPalette = palette{
	chassis:  ui.Hex("#15191b"),
	panel:    ui.Hex("#1c2124"),
	panelHi:  ui.Hex("#232a2d"),
	sunken:   ui.Hex("#111517"),
	line:     ui.Hex("#2d3538"),
	lineHi:   ui.Hex("#3b4448"),
	ink:      ui.Hex("#e9eceb"),
	inkDim:   ui.Hex("#9aa4a7"),
	inkFaint: ui.Hex("#788386"),
	signal:   ui.Hex("#f0a93b"),
	onSignal: ui.Hex("#14181a"),
	link:     ui.Hex("#4ad08a"),
	alarm:    ui.Hex("#ff6a4d"),
	charge:   ui.Hex("#5bb6e8"),
	track:    ui.Hex("#2b3336"),
}

// lightPalette is used when the desktop is light: a cool gray bench rather
// than a warm paper, so the instrument still reads as equipment.
var lightPalette = palette{
	chassis:  ui.Hex("#f0f2f1"),
	panel:    ui.Hex("#ffffff"),
	panelHi:  ui.Hex("#f5f7f6"),
	sunken:   ui.Hex("#e8ebea"),
	line:     ui.Hex("#d9dedd"),
	lineHi:   ui.Hex("#c2cac8"),
	ink:      ui.Hex("#1a1f22"),
	inkDim:   ui.Hex("#596366"),
	inkFaint: ui.Hex("#7c8589"),
	signal:   ui.Hex("#b26a00"),
	onSignal: ui.Hex("#ffffff"),
	link:     ui.Hex("#12805a"),
	alarm:    ui.Hex("#c0392b"),
	charge:   ui.Hex("#15629e"),
	track:    ui.Hex("#dfe4e3"),
}

// theme returns the mygo theme to install for this frame, and the palette the
// app's own elements draw with.
func theme(base *ui.Theme) (*ui.Theme, palette) {
	p := darkPalette
	if !base.Dark {
		p = lightPalette
	}
	t := *base
	t.Background = p.chassis
	t.Surface = p.panel
	t.SurfaceHover = p.panelHi
	t.SurfacePressed = p.sunken
	t.Border = p.line
	t.Text = p.ink
	t.TextMuted = p.inkDim
	t.Accent = p.signal
	t.AccentHover = p.signal.Mix(p.ink, 0.12)
	t.AccentPressed = p.signal.Mix(p.ink, 0.24)
	t.AccentText = p.onSignal
	t.Danger = p.alarm
	t.Success = p.link
	t.Warning = p.signal
	t.Selection = p.signal.Alpha(0.3)
	t.Focus = p.signal
	t.Scrollbar = p.lineHi
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 14
	return &t, p
}

// The app's type scale. One family — the platform's own, which is what draws
// Chinese and Latin with matching metrics — and a deliberate spread of sizes:
// the hero readouts carry the page, labels stay small and secondary.
const (
	sizeUnit    = 11.5 // units, footnotes
	sizeLabel   = 12.5 // setting labels, table rows
	sizeBody    = 14   // body
	sizeValue   = 15   // a value beside its label
	sizeTitle   = 16   // a panel's title
	sizePage    = 23   // the page's title
	sizeReadout = 30   // the hero numbers: gain, battery, level
)

// tabular turns on the font's tabular figures, so numbers that change as the
// status stream arrives do not make their line jump.
const tabular = "tnum"
