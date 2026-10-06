package console

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// statusPage is what the window is for: is the microphone on, how loud is it,
// how much charge is left, and what is it set to.
func (a *App) statusPage(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).Children(func() { a.receiverCard(c, p, snap) })
		ui.Column(c).Grow(1).Children(func() { a.transmitterCard(c, p, snap, 0) })
		ui.Column(c).Grow(1).Children(func() { a.transmitterCard(c, p, snap, 1) })
	})
	a.streamCard(c, p, snap)
}

// receiverCard shows the receiver: the link, the gain dial's position, its
// charge, and its identity.
func (a *App) receiverCard(c *ui.Context, p palette, snap session.Snapshot) {
	card(c, p, "接收器", "USB-C 连电脑", func() {
		a.linkLine(c, p, snap.Connected, a.deviceName(snap))

		if snap.State.RX.HasGain {
			metric(c, p, gainText(snap.State.RX.GainDial), "dB", "增益旋钮位置", p.ink)
			a.dialScale(c, p, snap.State.RX.GainDial)
		} else {
			metric(c, p, "—", "", "增益旋钮位置", p.inkFaint)
			ui.Text(c, "v1 固件不上报旋钮位置，转动接收器上的拨轮即可调整。").FontSize(sizeLabel).TextColor(p.inkFaint).LineHeight(1.4)
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
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				batteryGlyph(c, p, 0, false, false)
				ui.Text(c, "电量未知").FontSize(sizeValue).TextColor(p.inkDim)
			})
			ui.Text(c, "接收器由 USB 供电，只有 v2 固件的 Mic Mini 2 会上报自身电量。").FontSize(sizeLabel).TextColor(p.inkFaint).LineHeight(1.4)
		}

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
// dial now points.
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
				ui.Box(c).Width(4).Height(height).Radius(2).Background(color)
				ui.Text(c, gainText(detent)).FontSize(sizeUnit).
					TextColor(colorIf(lit, p.ink, p.inkFaint)).FontFeatures(tabular)
			})
		}
		ui.Spacer(c)
	})
}

// transmitterCard shows one transmitter: its link, its charge, its live level,
// and the settings that belong to it alone.
func (a *App) transmitterCard(c *ui.Context, p palette, snap session.Snapshot, index int) {
	tx := snap.State.TX[index]
	title := fmt.Sprintf("发射器 %d", index+1)
	card(c, p, title, "别在衣领上的那支", func() {
		a.linkLine(c, p, tx.Present, transmitterName(tx))

		pct, batteryKnown := tx.BatteryPercent()
		if batteryKnown {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				batteryGlyph(c, p, pct, true, tx.Charging)
				ui.Text(c, percentText(pct, true)).FontSize(sizeValue).
					TextColor(p.ink).FontFeatures(tabular)
				if tx.Charging {
					pill(c, p, "在盒中充电", p.charge)
				}
				ui.Spacer(c)
			})
		} else {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				batteryGlyph(c, p, 0, false, false)
				ui.Text(c, condition(tx.Present, "电量未知", "未连接")).FontSize(sizeValue).
					TextColor(p.inkDim)
			})
		}
		if tx.Present {
			ui.Text(c, "v1 固件不上报发射器电量，指示灯会显示红/绿。").FontSize(sizeUnit).TextColor(p.inkFaint).LineHeight(1.4)
		}

		hairline(c, p)
		a.levelRow(c, p, snap, index)

		if tone, ok := duml.SettingByID("voice-tone"); ok && tone.Available(snap.State.Dialect, a.product(snap)) {
			hairline(c, p)
			a.voiceToneRow(c, p, tx, index, tone)
		}

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
		levelLadder(c, p, fraction, a.peak[index], known)
		if !tx.Present {
			ui.Text(c, "发射器未连接：长按它的电源键开机，指示灯转绿后即连上。").FontSize(sizeUnit).TextColor(p.inkFaint).LineHeight(1.4)
		}
	})
}

// voiceToneRow picks one transmitter's tone. It is per transmitter on the
// device, and only the Mic Mini 2 has it, so it sits on the transmitter's card
// rather than among the shared settings.
func (a *App) voiceToneRow(c *ui.Context, p palette, tx duml.TXInfo, index int, tone duml.Setting) {
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
			ui.Text(c, condition(tx.VoiceTone == "", "未知", duml.VoiceToneLabel(tx.VoiceTone))).
				FontSize(sizeLabel).TextColor(p.inkFaint)
		})
		if !tx.Present {
			unavailableNote(c, p, "发射器未连接，暂不可设置")
			return
		}
		if ui.Segmented(c, &selected, labels...).Label(tone.Label).Changed() {
			a.setVoiceTone(c, index+1, tone.Options[selected].Value)
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

// streamCard reports the status stream itself, which is what a person needs
// when the window stays empty: the app reads the receiver rather than asking
// it questions, so a silent device is a real symptom.
func (a *App) streamCard(c *ui.Context, p palette, snap session.Snapshot) {
	card(c, p, "状态流", "设备主动推送，无需轮询", func() {
		ui.Row(c).Gap(24).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Gap(6).Children(func() {
				specRow(c, p, "协议版本", dialectText(snap), false)
				specRow(c, p, "推送频率", fmt.Sprintf("%.1f 帧/秒", snap.FramesPerSecond), true)
			})
			ui.Column(c).Grow(1).Gap(6).Children(func() {
				specRow(c, p, "最近一帧", ageText(snap), true)
				specRow(c, p, "已解码帧数", fmt.Sprintf("%d", snap.State.Packets), true)
			})
		})
		if !snap.Connected && snap.ConnectionError != "" {
			hairline(c, p)
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, iconWarning).FontSize(14).TextColor(p.signal)
				ui.Text(c, snap.ConnectionError).FontSize(sizeLabel).
					TextColor(p.inkDim).Grow(1).LineHeight(1.45)
			})
		}
	})
}

// deviceName is what to call the receiver: its own product name once the
// identity push has arrived, otherwise the family the USB ids matched.
func (a *App) deviceName(snap session.Snapshot) string {
	if snap.State.RX.Name != "" {
		return snap.State.RX.Name
	}
	if snap.State.DialectKnown {
		return snap.State.Dialect.String() + " 固件"
	}
	return ""
}

func transmitterName(tx duml.TXInfo) string {
	if tx.Name != "" {
		return tx.Name
	}
	return ""
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

func dialectText(snap session.Snapshot) string {
	if !snap.State.DialectKnown {
		return "未知"
	}
	return "v" + strings.TrimPrefix(snap.State.Dialect.String(), "v") + " 固件"
}

func ageText(snap session.Snapshot) string {
	if snap.LastFrame.IsZero() {
		return "—"
	}
	age := snap.Time.Sub(snap.LastFrame)
	switch {
	case age < time.Second:
		return fmt.Sprintf("%d 毫秒前", age.Milliseconds())
	case age < time.Minute:
		return fmt.Sprintf("%d 秒前", int(age.Seconds()))
	default:
		return fmt.Sprintf("%d 分钟前", int(age.Minutes()))
	}
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
