package console

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/session"
)

// View builds the window: the navigation rail, the page header, and the page
// itself. MyGo calls it for every frame, so it only reads state — everything
// that changes lives in the session or in App.
func (a *App) View(c *ui.Context) {
	theme, p := theme(c.Theme())
	c.SetTheme(theme)

	snap := a.snapshot()
	a.showNotice(c)

	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.chassis).Children(func() {
		a.rail(c, p, snap)
		a.content(c, p, snap)
	})
}

// rail is the left column: what this app is, where you can go, and whether the
// receiver is talking.
func (a *App) rail(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Column(c).Width(232).FillHeight().Padding(16, 14).Gap(18).
		Background(p.panel).BorderWidth(0, 1, 0, 0).BorderColor(p.line).
		Children(func() {
			a.brand(c, p)
			a.nav(c, p)
			ui.Spacer(c)
			a.linkStatus(c, p, snap)
		})
}

// brand is the app's name, with the tool's own indicator light beside it.
func (a *App) brand(c *ui.Context, p palette) {
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
			})
			if item.Clicked() {
				a.page = i
			}
		}
	})
}

// linkStatus is the rail's footer: whether the status stream is alive, and a
// way to look for the receiver again.
func (a *App) linkStatus(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Column(c).Gap(8).Children(func() {
		hairline(c, p)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ledDot(c, p.link, snap.Connected, 8)
			ui.Text(c, a.rateText(snap)).FontSize(sizeUnit).TextColor(p.inkDim).
				FontFeatures(tabular).SingleLine()
			ui.Spacer(c)
			if snap.State.DialectKnown {
				pill(c, p, "协议 "+snap.State.Dialect.String(), p.inkDim)
			}
		})
		if !snap.Connected && snap.ConnectionError != "" {
			ui.Text(c, snap.ConnectionError).FontSize(sizeUnit).TextColor(p.inkDim).LineHeight(1.4)
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
				case "settings":
					a.settingsPage(c, p, snap)
				case "driver":
					a.driverPage(c, p, snap)
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

		text, color, ready := driverBadge(snap, p)
		badge := ui.Row(c).Cursor(ui.CursorPointer).Children(func() {
			pill(c, p, text, color)
		})
		if badge.Clicked() && !ready {
			a.ShowPage("driver")
		}
	})
}

// driverBadge summarises the driver's state in one phrase.
func driverBadge(snap session.Snapshot, p palette) (text string, color ui.Color, ready bool) {
	if !snap.HaveModel {
		if len(snap.Status.UnknownDevices()) > 0 {
			return "型号不在表里", p.signal, false
		}
		return "未检测到设备", p.inkFaint, false
	}
	if snap.Status.Ready() {
		return "WinUSB 就绪", p.link, true
	}
	if snap.Status.Control != nil && snap.Status.Control.Present {
		return "接口未绑定驱动", p.alarm, false
	}
	return "厂商接口未出现", p.signal, false
}

// subtitle is the page's one line of context.
func (p page) subtitle() string {
	switch p.id {
	case "status":
		return "接收器与两支发射器的实时状态，由设备以约 10 次/秒主动推送"
	case "settings":
		return "写回接收器后立即生效；v1 固件没有的设置会写明原因"
	default:
		return "只给厂商接口安装 WinUSB，录音与按键接口保持系统驱动"
	}
}
