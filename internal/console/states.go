package console

import (
	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/session"
)

// waitingCard fills a page that has no protocol to draw yet: the receiver is
// not connected, or its firmware version has not shown itself. Rather than
// guessing a version, the page says what it is waiting for.
func (a *App) waitingCard(c *ui.Context, p palette, snap session.Snapshot) {
	if snap.Connected {
		card(c, p, "正在读取固件版本", "收到第一个状态帧后，界面会按 v1 或 v2 分别显示", func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "接收器已经连上，正在等待状态流…").FontSize(sizeLabel).TextColor(p.inkDim)
			})
		})
		return
	}

	title, detail := "还没有状态", "程序会自动重新检测设备"
	if pages[a.page].id == "settings" {
		title, detail = "设置需要接收器", "连接后显示它固件支持的设置"
	}
	reason := snap.ConnectionError
	if reason == "" {
		reason = "正在寻找接收器，插上后会自己连上。"
	}
	card(c, p, title, detail, func() {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, iconWarning).FontSize(15).TextColor(p.signal).Margin(2, 0, 0, 0)
			ui.Text(c, reason).FontSize(sizeLabel).TextColor(p.inkDim).Grow(1).LineHeight(1.5)
		})
		a.connectActions(c, p, snap)
	})
}

// connectActions is what a page with nothing to show offers as next steps.
func (a *App) connectActions(c *ui.Context, p palette, snap session.Snapshot) {
	ui.Row(c).Gap(10).Wrap().Children(func() {
		if ui.Button(c, "重新检测").Clicked() {
			a.sess.Rescan()
			a.sess.Log("重新检测设备")
		}
		if _, _, ready := driverBadge(snap, p); !ready {
			if ui.Button(c, "打开驱动页").Clicked() {
				a.ShowPage("driver")
			}
		}
	})
}
