package console

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// devicePage groups what the app knows about the hardware itself: the settings
// that change how it behaves, and the identity it reports about itself. It is
// the page to open when someone asks which firmware is on the thing.
func (a *App) devicePage(c *ui.Context, p palette, snap session.Snapshot) {
	a.settingsPage(c, p, snap, duml.GroupDevice)

	card(c, p, "设备身份", "接收器自己上报的信息", func() {
		if !snap.Connected {
			ui.Text(c, "接收器未连接，这些信息会在连接后由设备推送。"+
				"").FontSize(sizeLabel).TextColor(p.inkFaint).LineHeight(1.5)
		}
		ui.Column(c).Gap(6).Children(func() {
			specRow(c, p, "设备名称", blank(snap.State.RX.Name), false)
			specRow(c, p, "序列号", blank(snap.State.RX.Serial), true)
			specRow(c, p, "固件版本", blank(snap.State.RX.Firmware), true)
			specRow(c, p, "协议版本", dialectText(snap), false)
			specRow(c, p, "USB 标识", usbID(snap), true)
			specRow(c, p, "厂商接口", interfaceLabel(snap), true)
		})

		if snap.State.TXCount() > 0 {
			hairline(c, p)
			ui.Column(c).Gap(6).Children(func() {
				for i, tx := range snap.State.TX {
					if !tx.Present {
						continue
					}
					specRow(c, p,
						fmt.Sprintf("发射器 %d 序列号", i+1), blank(tx.Serial), true)
					specRow(c, p,
						fmt.Sprintf("发射器 %d 固件", i+1), blank(tx.Firmware), true)
					specRow(c, p,
						fmt.Sprintf("发射器 %d 音色", i+1),
						condition(tx.VoiceTone == "", "未知", duml.VoiceToneLabel(tx.VoiceTone)), false)
				}
			})
		}

		hairline(c, p)
		ui.Row(c).Gap(10).Wrap().Children(func() {
			if ui.Button(c, "复制诊断信息").Clicked() {
				a.copyDiagnostics(c)
			}
			if ui.Button(c, "打开驱动页").Clicked() {
				a.ShowPage("driver")
			}
		})
	})

	card(c, p, "可以在硬件上直接操作的事", "有些功能不需要这个程序", func() {
		ui.Column(c).Gap(8).Children(func() {
			for _, line := range []string{
				"增益：转动接收器左上侧的拨轮，五档、每档 6 dB；程序只能读出当前位置。",
				"单声道/立体声：双击接收器的配对键即可切换。",
				"配对：发射器与接收器都长按配对键两秒。",
				"降噪开关：v1 固件的发射器短按电源键（Mic Mini 2 是双击）。",
			} {
				ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
					ui.Box(c).Width(4).Height(4).Radius(2).Background(p.signal).
						Margin(4, 0, 0, 0)
					ui.Text(c, line).FontSize(sizeLabel).TextColor(p.inkDim).Grow(1).
						LineHeight(1.5)
				})
			}
		})
	})
}
