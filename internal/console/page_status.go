package console

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// statusPage is what the window is for: is the microphone on, how loud is it,
// and how much charge is left. Each protocol gets the page its data asks for:
// v1 a receiver strip over the two transmitters, v2 three instrument panels,
// so no field is ever drawn that the firmware does not report.
func (a *App) statusPage(c *ui.Context, p palette, snap session.Snapshot) {
	dialect, known := dialectOf(snap)
	if !snap.Connected || !known {
		a.waitingCard(c, p, snap)
		return
	}
	if dialect == duml.V1 {
		a.statusV1(c, p, snap)
		return
	}
	a.statusV2(c, p, snap)
}

// statusV2 arranges the three devices as one row of equal panels: the cards
// stretch to a common height, so their edges and identity rows line up.
func (a *App) statusV2(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
		a.receiverCard(c, p, snap).Grow(1)
		a.transmitterCard(c, p, snap, 0).Grow(1)
		a.transmitterCard(c, p, snap, 1).Grow(1)
	})
}

// statusV1 gives the page to the transmitters: the v1 receiver reports no
// charge and no dial position, so it is a strip of facts, and the two
// transmitter cards below share one height.
func (a *App) statusV1(c *ui.Context, p palette, snap session.Snapshot) {
	a.receiverStrip(c, p, snap)
	ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
		a.transmitterCard(c, p, snap, 0).Grow(1)
		a.transmitterCard(c, p, snap, 1).Grow(1)
	})
}

// receiverStrip is the v1 receiver: its facts in a row, without the gauges
// its firmware does not report.
func (a *App) receiverStrip(c *ui.Context, p palette, snap session.Snapshot) *ui.Element {
	return card(c, p, "接收器", "USB-C 连电脑", func() {
		a.linkLine(c, p, snap.Connected, a.deviceName(snap))
		hairline(c, p)
		ui.Row(c).Gap(28).Wrap().Children(func() {
			stripStat(c, p, "序列号", blank(snap.State.RX.Serial), true)
			stripStat(c, p, "固件", blank(snap.State.RX.Firmware), true)
			stripStat(c, p, "已开机的发射器", fmt.Sprintf("%d / 2", snap.State.TXCount()), false)
		})
		ui.Text(c, "电量与增益旋钮位置不在 v1 的协议里：电量看机身与发射器的指示灯，增益看旋钮的刻度。").
			FontSize(sizeUnit).TextColor(p.inkFaint).LineHeight(1.5)
	})
}

// receiverCard shows the v2 receiver: the link, the gain dial's position, its
// charge when it has a battery, and its identity.
func (a *App) receiverCard(c *ui.Context, p palette, snap session.Snapshot) *ui.Element {
	return card(c, p, "接收器", "USB-C 连电脑", func() {
		a.linkLine(c, p, snap.Connected, a.deviceName(snap))

		if snap.State.RX.HasGain {
			metric(c, p, gainText(snap.State.RX.GainDial), "dB", "增益旋钮位置", p.ink)
			a.dialScale(c, p, snap.State.RX.GainDial)
		} else {
			metric(c, p, "—", "", "增益旋钮位置", p.inkFaint)
		}

		hairline(c, p)
		if pct, known := snap.State.RX.BatteryPercent(); known {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				batteryGlyph(c, p, pct, true, snap.State.RX.Charging)
				ui.Text(c, percentText(pct, true)).FontSize(sizeValue).
					TextColor(p.ink).FontFeatures(tabular)
				if snap.State.RX.Charging {
					pill(c, p, "充电中", p.charge)
				}
				ui.Spacer(c)
			})
		} else {
			// No gauge means no battery to show: the DJI Mic series mobile
			// receiver (DMMR02) is powered by USB-C instead.
			ui.Text(c, "由 USB-C 供电，无内置电池").FontSize(sizeLabel).TextColor(p.inkDim).SingleLine()
		}

		ui.Spacer(c)
		hairline(c, p)
		ui.Column(c).Gap(6).Children(func() {
			specRow(c, p, "序列号", blank(snap.State.RX.Serial), true)
			specRow(c, p, "固件", blank(snap.State.RX.Firmware), true)
			specRow(c, p, "已开机的发射器", fmt.Sprintf("%d / 2", snap.State.TXCount()), false)
		})
	})
}

// dialScale draws the five positions of the receiver's gain dial, the one
// control on the hardware the app can only read. The lit mark is where the
// dial now points, and it glides between detents as the knob turns.
func (a *App) dialScale(c *ui.Context, p palette, db int) {
	detents := []int{-12, -6, 0, 6, 12}
	ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
		for _, detent := range detents {
			lit := detent == db
			height := float32(9)
			if lit {
				height = 17
			}
			color := p.track
			if lit {
				color = p.signal
			}
			ui.Column(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(4).Height(height).Radius(2).Background(color).
					Key(fmt.Sprintf("detent%d", detent)).
					Transition(ui.ElementTransition{Size: true, Colors: true, Duration: 160 * time.Millisecond})
				ui.Text(c, gainText(detent)).FontSize(sizeUnit).
					TextColor(colorIf(lit, p.ink, p.inkFaint)).FontFeatures(tabular)
			})
		}
		ui.Spacer(c)
	})
}

// transmitterCard shows one transmitter: its link, its charge where one is
// reported, its live level, and the settings that belong to it alone.
func (a *App) transmitterCard(c *ui.Context, p palette, snap session.Snapshot, index int) *ui.Element {
	tx := snap.State.TX[index]
	title := fmt.Sprintf("发射器 %d", index+1)
	return card(c, p, title, "别在衣领上的那支", func() {
		a.linkLine(c, p, tx.Present, tx.Name)

		// The charge row exists only where there is a gauge: v2 firmware
		// reports one, but behind the mobile receiver (DMMR02) it stays
		// clear, so no placeholder is drawn.
		if snap.State.Dialect == duml.V2 {
			if pct, known := tx.BatteryPercent(); known {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					batteryGlyph(c, p, pct, true, tx.Charging)
					ui.Text(c, percentText(pct, true)).FontSize(sizeValue).
						TextColor(p.ink).FontFeatures(tabular)
					if tx.Charging {
						pill(c, p, "在盒中充电", p.charge)
					}
					ui.Spacer(c)
				})
			}
		}

		hairline(c, p)
		a.levelRow(c, p, snap, index)

		// Voice tone is the transmitter's own setting, so its availability
		// is decided by the transmitter's product name, not the receiver's.
		if tone, ok := duml.SettingByID("voice-tone"); ok && tone.Available(snap.State.Dialect, tx.Name) {
			hairline(c, p)
			a.voiceToneRow(c, p, tx, index, tone)
		}

		ui.Spacer(c)
		hairline(c, p)
		ui.Column(c).Gap(6).Children(func() {
			specRow(c, p, "序列号", blank(tx.Serial), true)
			specRow(c, p, "固件", blank(tx.Firmware), true)
		})
	})
}

// levelRow is the live input level: the app's one moving part.
func (a *App) levelRow(c *ui.Context, p palette, snap session.Snapshot, index int) {
	tx := snap.State.TX[index]
	fraction, known := duml.LevelFraction(tx.Level)
	if !tx.Present {
		known = false
	}
	ui.Column(c).Gap(7).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "输入电平").FontSize(sizeLabel).TextColor(p.inkDim)
			ui.Spacer(c)
			ui.Text(c, levelText(tx, known)).FontSize(sizeValue).TextColor(p.ink).
				FontFeatures(tabular)
		})
		levelLadder(c, p, fraction, known)
		if !tx.Present {
			ui.Text(c, "发射器未连接：长按它的电源键开机，指示灯转绿后即连上。").FontSize(sizeUnit).TextColor(p.inkFaint).LineHeight(1.4)
		}
	})
}

// voiceToneRow picks one transmitter's tone. It is per transmitter on the
// device, and only the Mic Mini 2 has it, so it sits on the transmitter's card
// rather than among the shared settings.
func (a *App) voiceToneRow(c *ui.Context, p palette, tx duml.TXInfo, index int, tone duml.Setting) {
	pending := a.settingPending(tone.ID, index+1)
	selected := 0
	for i, option := range tone.Options {
		if option.Value == tx.VoiceTone {
			selected = i
		}
	}
	labels := make([]string, 0, len(tone.Options))
	for _, option := range tone.Options {
		labels = append(labels, option.Label)
	}
	ui.Column(c).Gap(7).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tone.Label).FontSize(sizeLabel).TextColor(p.inkDim)
			ui.Spacer(c)
			if pending {
				settingProgress(c, p)
			} else {
				ui.Text(c, condition(tx.VoiceTone == "", "未知", duml.VoiceToneLabel(tx.VoiceTone))).
					FontSize(sizeLabel).TextColor(p.inkFaint)
			}
		})
		if !tx.Present {
			unavailableNote(c, p, "发射器未连接，暂不可设置")
			return
		}
		previous := selected
		if ui.Segmented(c, &selected, labels...).Label(tone.Label).Disabled(pending).Changed() {
			a.setVoiceTone(c, index+1, tone.Options[selected].Value)
			selected = previous
		}
	})
}

// linkLine is the top of a device card: a light, what it means, and the
// device's own name for itself.
func (a *App) linkLine(c *ui.Context, p palette, connected bool, name string) {
	color := p.inkFaint
	text := "未连接"
	if connected {
		color, text = p.link, "已连接"
	}
	ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
		ledDot(c, color, connected, 9)
		ui.Text(c, text).FontSize(sizeBody).FontWeight(600).TextColor(color).SingleLine()
		ui.Spacer(c)
		if name != "" {
			ui.Text(c, name).FontSize(sizeUnit).TextColor(p.inkFaint).SingleLine().
				MaxWidth(140).TextAlign(ui.End)
		}
	})
}

func (a *App) deviceName(snap session.Snapshot) string {
	if snap.State.RX.Name != "" {
		return snap.State.RX.Name
	}
	return "大疆Mic 接收器"
}

// product is the best name for the receiver, used to decide whether a setting
// exists on this model.
func (a *App) product(snap session.Snapshot) string {
	if snap.State.RX.Name != "" {
		return snap.State.RX.Name
	}
	if snap.HaveModel {
		return snap.Model.Name
	}
	return ""
}

func levelText(tx duml.TXInfo, known bool) string {
	if !known {
		return "—"
	}
	return fmt.Sprintf("%d", tx.Level)
}

func blank(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func condition(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

// colorIf picks one of two colours, for a lit and an unlit element.
func colorIf(ok bool, yes, no ui.Color) ui.Color {
	if ok {
		return yes
	}
	return no
}
