package console

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// View builds the window: the navigation rail, the page header, and the page
// itself. MyGo calls it for every frame, so it only reads state — everything
// that changes lives in the session or in App.
func (a *App) View(c *ui.Context) {
	theme, p := theme(c.Theme())
	c.SetTheme(theme)

	snap := a.snapshot()
	a.updatePeaks(snap)
	a.handleShortcuts(c, snap)
	a.showNotice(c)

	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.chassis).Children(func() {
		a.rail(c, p, snap)
		a.content(c, p, snap)
	})
	a.dialogs(c, p)
}

// handleShortcuts wires the keyboard: number keys for the pages, plus a
// rescan and a copy of the diagnostics.
func (a *App) handleShortcuts(c *ui.Context, snap session.Snapshot) {
	for i := range pages {
		key := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6}[i]
		if c.Shortcut(ui.Cmd, key) {
			a.page = i
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyR) {
		a.sess.Rescan()
		a.sess.Log("手动重新检测设备")
		c.Toast("正在重新检测接收器")
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyD) {
		a.copyDiagnostics(c)
	}
}

// rail is the left column: what this app is, where you can go, and whether the
// receiver is talking.
func (a *App) rail(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Column(c).Width(232).FillHeight().Padding(16, 14).Gap(18).
		Background(p.panel).BorderWidth(0, 1, 0, 0).BorderColor(p.line).
		Children(func() {
			a.brand(c, p, snap)
			a.nav(c, p)
			ui.Spacer(c)
			a.linkStatus(c, p, snap)
		})
}

// brand is the app's name, with the tool's own indicator light beside it.
func (a *App) brand(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconReceiver).FontSize(26).TextColor(p.signal)
		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, "接收器控制台").FontSize(15).FontWeight(600).TextColor(p.ink).SingleLine()
			ui.Text(c, "DJI Mic Mini 系列").FontSize(sizeUnit).TextColor(p.inkFaint).SingleLine()
		})
	})
}

// nav lists the pages. The selected one is marked by a lit bar rather than by
// a filled block, the way a pressed button on a device reads.
func (a *App) nav(c *ui.Context, p palette) {
	ui.Column(c).Gap(2).Children(func() {
		for i, pg := range pages {
			selected := i == a.page
			item := ui.Row(c).Gap(10).Padding(7, 8).Radius(7).
				AlignItems(ui.Center).Cursor(ui.CursorPointer)
			if selected {
				item.Background(p.panelHi)
			}
			item.Children(func() {
				bar := p.signal
				if !selected {
					bar = ui.Transparent
				}
				ui.Box(c).Width(2).Height(16).Radius(1).Background(bar)
				iconColor := p.inkDim
				labelColor := p.inkDim
				if selected {
					iconColor = p.signal
					labelColor = p.ink
				}
				ui.Icon(c, pg.icon).FontSize(17).TextColor(iconColor)
				ui.Text(c, pg.label).FontSize(sizeBody).TextColor(labelColor).Grow(1).SingleLine()
				if a.showKeys {
					keyHint(c, p, itoa(i+1))
				}
			})
			if item.Clicked() {
				a.page = i
			}
		}
	})
}

// linkStatus is the rail's footer: the activity light, the push rate, and —
// when something is wrong — what to do about it.
func (a *App) linkStatus(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Column(c).Gap(8).Children(func() {
		hairline(c, p)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			// The light burns while status frames arrive and dims between
			// them, so the window shows the stream is alive.
			level := a.pulse(snap)
			color := p.link.Alpha(level)
			ledDot(c, color, snap.Connected, 8)
			ui.Text(c, a.rateText(snap)).FontSize(sizeUnit).TextColor(p.inkDim).
				FontFeatures(tabular).SingleLine()
			ui.Spacer(c)
			if snap.State.DialectKnown {
				pill(c, p, "协议 "+snap.State.Dialect.String(), p.inkDim)
			}
		})
		if message := a.linkMessage(snap); message != "" {
			ui.Text(c, message).FontSize(sizeUnit).TextColor(p.inkDim).LineHeight(1.4)
		}
		action := ui.Row(c).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		action.Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.inkDim)
			ui.Text(c, "重新检测").FontSize(sizeUnit).TextColor(p.inkDim)
		})
		if action.Clicked() {
			a.sess.Rescan()
			a.sess.Log("重新检测设备")
		}
	})
}

// rateText describes the status stream's rate in words rather than as a bare
// number, since that is what tells a person the receiver is talking.
func (a *App) rateText(snap session.Snapshot) string {
	switch {
	case !snap.Connected:
		return "未连接"
	case snap.FramesPerSecond <= 0:
		return "正在等待状态"
	default:
		return fmt.Sprintf("%.1f 帧/秒", snap.FramesPerSecond)
	}
}

// linkMessage is the rail's explanation of a link that is not up.
func (a *App) linkMessage(snap session.Snapshot) string {
	if snap.Connected {
		return ""
	}
	return snap.ConnectionError
}

// content is the page column: its header, then the page itself.
func (a *App) content(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		a.header(c, p, snap)
		hairline(c, p)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(20, 20).Gap(16).Children(func() {
				switch pages[a.page].id {
				case "status":
					a.statusPage(c, p, snap)
				case "audio":
					a.settingsPage(c, p, snap, duml.GroupAudio)
				case "power":
					a.settingsPage(c, p, snap, duml.GroupPower)
				case "device":
					a.devicePage(c, p, snap)
				case "driver":
					a.driverPage(c, p, snap)
				case "about":
					a.aboutPage(c, p, snap)
				}
			})
		})
	})
}

// header is the page's title and its live badges: whether the receiver is
// connected, and whether its driver is in place. The driver badge is also the
// way to the driver page, so a problem is one click from its fix.
func (a *App) header(c *ui.Context, p palette, snap session.Snapshot) {
	page := pages[a.page]
	ui.Row(c).Padding(18, 20).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, page.title).FontSize(sizePage).FontWeight(600).TextColor(p.ink).SingleLine()
			ui.Text(c, page.subtitle()).FontSize(sizeLabel).TextColor(p.inkDim).SingleLine()
		})
		ui.Spacer(c)

		link := p.link
		linkText := "已连接"
		if !snap.Connected {
			link, linkText = p.inkFaint, "未连接"
		}
		pill(c, p, linkText, link)

		driver, driverText, driverColor := driverBadge(snap, p)
		badge := ui.Row(c).Cursor(ui.CursorPointer).Children(func() {
			pill(c, p, driverText, driverColor)
		})
		if badge.Clicked() && driver != "ready" {
			a.ShowPage("driver")
		}
		_ = driver
	})
}

// driverBadge summarises the driver's state in one phrase.
func driverBadge(snap session.Snapshot, p palette) (state, text string, color ui.Color) {
	if !snap.HaveModel {
		if len(snap.Status.UnknownDevices()) > 0 {
			return "unknown", "型号不在表里", p.signal
		}
		return "missing", "未检测到设备", p.inkFaint
	}
	if snap.Status.Ready() {
		return "ready", "WinUSB 就绪", p.link
	}
	if snap.Status.Control != nil && snap.Status.Control.Present {
		return "nodriver", "接口未绑定驱动", p.alarm
	}
	return "nointerface", "厂商接口未出现", p.signal
}

// subtitle is the page's one line of context, written from what the app knows
// rather than from a fixed string.
func (p page) subtitle() string {
	switch p.id {
	case "status":
		return "接收器与两支发射器的实时状态，由设备以约 10 次/秒主动推送"
	case "audio":
		return "降噪、低切与声道设置，写回接收器后立即生效"
	case "power":
		return "自动关机与随相机开机的行为"
	case "device":
		return "指示灯、外放与设备身份信息"
	case "driver":
		return "只给厂商接口安装 WinUSB，录音与按键接口保持系统驱动"
	default:
		return "非官方工具，协议来自社区逆向"
	}
}
