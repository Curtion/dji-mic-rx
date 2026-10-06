package console

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// hairline draws a rule across its parent, thinner than the toolkit's Divider
// so a panel can hold several without looking striped.
func hairline(c *ui.Context, p palette) {
	ui.Box(c).Height(1).FillWidth().Background(p.line)
}

// ledDot is an indicator light: the app's smallest unit of state, borrowed
// from the receiver's own front panel.
func ledDot(c *ui.Context, color ui.Color, lit bool, size float32) *ui.Element {
	dot := ui.Box(c).Size(size, size).Radius(size)
	if !lit {
		return dot.Background(dim(color, 0.22))
	}
	return dot.Background(color)
}

// dim fades a colour, for the unlit state of a light that is off.
func dim(color ui.Color, amount float32) ui.Color {
	return color.Alpha(amount)
}

// pill is a short status label: a word in the colour of the state it names,
// on a wash of the same colour. It never uses all caps, so it stays readable
// in Chinese.
func pill(c *ui.Context, p palette, text string, color ui.Color) *ui.Element {
	return ui.Row(c).Padding(3, 8).Radius(999).Background(color.Alpha(0.14)).Children(func() {
		ui.Text(c, text).FontSize(sizeUnit).FontWeight(600).TextColor(color).SingleLine()
	})
}

// batteryGlyph draws a battery the way the device's own indicator works: a
// shell with a fill, plus a bolt while charging. When the receiver does not
// report a level, the shell is drawn empty and the value is "未知" — an
// honest blank rather than a guess.
func batteryGlyph(c *ui.Context, p palette, pct int, known bool, charging bool) *ui.Element {
	color := p.link
	if known {
		switch {
		case pct <= 15:
			color = p.alarm
		case pct <= 40:
			color = p.signal
		}
	}
	return ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		body := ui.Box(c).Width(46).Height(18).Padding(2).Radius(4).
			Border(1, p.lineHi).Background(p.sunken)
		body.Children(func() {
			if known && pct > 0 {
				ui.Box(c).WidthPercent(float32(pct)).FillHeight().
					Background(color).Radius(2)
			}
			if charging {
				ui.Icon(c, iconBolt).FontSize(11).TextColor(p.ink).
					Attach(ui.AnchorCenter, ui.AnchorCenter)
			}
		})
		ui.Box(c).Width(3).Height(8).Radius(1).Background(p.lineHi)
	})
}

// levelBands are the three colours a level ladder lights up in, in the order
// they appear along it: quiet, loud, and too loud.
func levelBands(p palette) [3]ui.Color {
	return [3]ui.Color{p.link, p.signal, p.alarm}
}

// ladderSegments is how many steps the ladder is drawn in. Twenty-odd steps
// read as a meter rather than a progress bar, which is what this is.
const ladderSegments = 24

// levelLadder draws the live audio level as a segmented ladder with a peak
// hold, the loudest element in the window because it is the only one that
// moves on its own.
//
// The level is in the device's own units, so the ladder is scaled to the
// range the receiver reports rather than pretending to be dBFS; the number
// beside it is the raw reading.
func levelLadder(c *ui.Context, p palette, fraction, peak float64, live bool) *ui.Element {
	bands := levelBands(p)
	lit := 0
	if live {
		lit = ladderLit(fraction)
	}
	peakStep := -1
	if live && peak > 0 {
		peakStep = int(math.Round(peak * float64(ladderSegments)))
	}
	return ui.Row(c).Gap(2).Children(func() {
		for i := 0; i < ladderSegments; i++ {
			band := 0
			switch {
			case i >= ladderSegments*88/100:
				band = 2
			case i >= ladderSegments*70/100:
				band = 1
			}
			color := p.track
			switch {
			case i < lit:
				color = bands[band]
			case i == peakStep-1:
				// The peak marker: a single step held at its loudest.
				color = bands[band].Mix(p.ink, 0.5)
			}
			ui.Box(c).Width(5).Height(12).Radius(1.5).Background(color)
		}
	})
}

// ladderLit is how many steps of the ladder a level lights. It is a function
// of its own so the meter's arithmetic can be checked without a window: the
// quietest sound lights one step rather than none, which is what tells a
// person the microphone is picking something up at all.
func ladderLit(fraction float64) int {
	if fraction <= 0 {
		return 0
	}
	lit := int(math.Round(fraction * float64(ladderSegments)))
	if lit < 1 {
		lit = 1
	}
	if lit > ladderSegments {
		lit = ladderSegments
	}
	return lit
}

// specRow is one line of a device's specification: a quiet label on the left,
// its value right aligned so a column of values can be read down.
func specRow(c *ui.Context, p palette, label, value string, mono bool) *ui.Element {
	return ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(sizeLabel).TextColor(p.inkDim).SingleLine()
		ui.Spacer(c)
		valueText := ui.Text(c, value).FontSize(sizeLabel).TextColor(p.ink).
			SingleLine().TextAlign(ui.End).FontFeatures(tabular)
		if mono {
			valueText.Font("monospace").Selectable()
		}
	})
}

// card is a panel: a plate on the chassis with a title and its contents.
func card(c *ui.Context, p palette, title, detail string, body func()) *ui.Element {
	return ui.Column(c).Padding(16).Gap(12).Radius(10).
		Background(p.panel).Border(1, p.line).Children(func() {
		if title != "" || detail != "" {
			ui.Column(c).Gap(2).Children(func() {
				if title != "" {
					ui.Text(c, title).FontSize(sizeTitle).FontWeight(600).TextColor(p.ink)
				}
				if detail != "" {
					ui.Text(c, detail).FontSize(sizeLabel).TextColor(p.inkDim)
				}
			})
		}
		body()
	})
}

// fieldRow is a labelled line with a control at its right: one setting, or one
// piece of information.
func fieldRow(c *ui.Context, p palette, label, detail string, control func()) *ui.Element {
	return ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, label).FontSize(sizeBody).TextColor(p.ink)
			if detail != "" {
				ui.Text(c, detail).FontSize(sizeLabel).TextColor(p.inkDim)
			}
		})
		ui.Row(c).Shrink(0).Children(control)
	})
}

// unavailableNote marks a setting this firmware or this model has no command
// for, so a missing control is explained rather than simply absent.
func unavailableNote(c *ui.Context, p palette, reason string) *ui.Element {
	return ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconInfo).FontSize(13).TextColor(p.inkFaint)
		ui.Text(c, reason).FontSize(sizeLabel).TextColor(p.inkFaint).SingleLine()
	})
}

// metric is a hero readout: a large tabular number with its unit beside it and
// a small label under it. It is used for the two values worth reading from
// across a room — the gain dial's position and a battery's charge.
func metric(c *ui.Context, p palette, value, unit, label string, color ui.Color) *ui.Element {
	return ui.Column(c).Gap(2).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.End).Children(func() {
			ui.Text(c, value).FontSize(sizeReadout).FontWeight(600).
				TextColor(color).FontFeatures(tabular).LineHeight(1.05).SingleLine()
			if unit != "" {
				ui.Text(c, unit).FontSize(sizeLabel).TextColor(p.inkDim).
					Padding(0, 0, 4, 0).SingleLine()
			}
		})
		ui.Text(c, label).FontSize(sizeUnit).TextColor(p.inkFaint).SingleLine()
	})
}

// keyHint renders the keyboard shortcut beside a page title, as a small
// caption rather than a badge.
func keyHint(c *ui.Context, p palette, text string) *ui.Element {
	return ui.Text(c, text).FontSize(sizeUnit).TextColor(p.inkFaint).SingleLine()
}

// percentText renders a battery reading.
func percentText(pct int, known bool) string {
	if !known {
		return "未知"
	}
	return fmt.Sprintf("%d%%", pct)
}

// gainText renders the receiver's dial position, which steps in 6 dB steps
// across five positions.
func gainText(db int) string {
	if db > 0 {
		return fmt.Sprintf("+%d", db)
	}
	return fmt.Sprintf("%d", db)
}
