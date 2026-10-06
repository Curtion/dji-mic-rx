package console

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/session"
)

// driverPage is where the one thing this app must do that nothing else does
// lives: give the receiver's vendor interface a driver, so its status can be
// read and its settings written. It states what it finds, offers the fix, and
// shows the installer's own output rather than hiding it.
func (a *App) driverPage(c *ui.Context, p palette, snap session.Snapshot) {
	state := driverStatusFor(snap, p)
	card(c, p, state.title, state.detail, func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ledDot(c, state.color, state.lit, 10)
			ui.Text(c, state.summary).FontSize(sizeBody).TextColor(p.ink).Grow(1).
				LineHeight(1.45)
		})

		ui.Row(c).Gap(10).Wrap().Children(func() {
			install := ui.PrimaryButton(c, "安装驱动").
				Disabled(snap.Installing || !state.canInstall)
			if install.Clicked() {
				a.install(c)
			}
			uninstall := ui.Button(c, "卸载驱动").
				Disabled(snap.Installing || !state.canUninstall)
			if uninstall.Clicked() {
				a.uninstall(c)
			}
			if ui.Button(c, "重新检测").Clicked() {
				a.sess.Rescan()
				a.sess.Log("重新检测设备")
				c.Toast("正在重新检测接收器")
			}
			if ui.Button(c, "复制诊断信息").Clicked() {
				a.copyDiagnostics(c)
			}
		})

		ui.Text(c, "安装需要管理员权限，Windows 会弹出授权窗口。驱动包在构建时已签名，"+
			"安装只是把它导入系统。安装只绑定厂商接口，接收器的音频与按键接口保持系统驱动，录音不受影响。"+
			"装完请重新插拔接收器。").
			FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
	})

	if snap.Installing || len(snap.InstallLog) > 0 {
		a.installLogCard(c, p, snap)
	}
	a.nodeCard(c, p, snap)

	card(c, p, "关于", "非官方工具，与 DJI 没有关系", func() {
		ui.Text(c, "帧格式、CRC 与命令表来自 ShadowBitBasher 的 DJI-Mic-Control 项目，"+
			"字段偏移与位掩码经 usokawa 的 dji-mic-mo 交叉核对。").
			FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
		ui.Row(c).Gap(10).Wrap().Children(func() {
			ui.Link(c, "DJI-Mic-Control", "https://github.com/ShadowBitBasher/DJI-Mic-Control")
			ui.Link(c, "dji-mic-mo", "https://github.com/usokawa/dji-mic-mo")
		})
	})
}

// driverStatus is what the driver page says at the top, decided by what the
// scan found.
type driverStatus struct {
	title   string
	summary string
	detail  string
	color   ui.Color
	lit     bool

	canInstall   bool
	canUninstall bool
}

func driverStatusFor(snap session.Snapshot, p palette) driverStatus {
	switch {
	case !snap.HaveModel:
		if unknown := snap.Status.UnknownDevices(); len(unknown) > 0 {
			ids := make([]string, 0, len(unknown))
			seen := map[string]bool{}
			for _, node := range unknown {
				id := fmt.Sprintf("0x%04x:0x%04x", node.Vendor, node.Product)
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			return driverStatus{
				title:   "型号不在协议表里",
				summary: "检测到 DJI 设备（" + strings.Join(ids, "、") + "），但它不在已知接收器表里，程序不会贸然给它装驱动。",
				detail:  "用「复制诊断信息」把设备节点列表发出来，就能把型号加进表里。",
				color:   p.signal,
			}
		}
		return driverStatus{
			title:   "未检测到接收器",
			summary: "把接收器用 USB-C 线连到电脑，然后点「重新检测」。",
			detail:  "支持的型号：DJI Mic Mobile RX（DMMR01/DMMR02，USB 0x2ca3:0x4011）。它的音频接口会照常出现在系统的录音设备里。",
			color:   p.inkFaint,
		}
	case snap.Status.Ready():
		summary := fmt.Sprintf("WinUSB 已经绑定在接口 %d 上。", snap.Model.Interface)
		if snap.Connected {
			summary = fmt.Sprintf("WinUSB 已经绑定在接口 %d 上，状态正在推送。", snap.Model.Interface)
		}
		return driverStatus{
			title:        "驱动已就绪",
			summary:      summary,
			detail:       "卸载驱动只会移除这个接口的绑定，卸下后重新插拔接收器即可回到 Windows 的默认状态。",
			color:        p.link,
			lit:          true,
			canUninstall: true,
		}
	case snap.Status.Control != nil && snap.Status.Control.Present:
		summary := "接口存在，但没有驱动绑定。"
		if service := snap.Status.Control.Driver.Service; service != "" {
			summary = "接口绑定的是 " + service + " 驱动，程序需要 WinUSB。"
		}
		return driverStatus{
			title:      "接口需要 WinUSB 驱动",
			summary:    summary,
			detail:     "没有驱动时，Windows 不会把厂商接口的数据交给应用程序，所以接收器状态是空的。",
			color:      p.alarm,
			canInstall: true,
		}
	default:
		return driverStatus{
			title:      "厂商接口还没有出现",
			summary:    "接收器已经连上（音频接口正常），但它的厂商接口没有出现在设备树上。",
			detail:     "驱动只能绑定已经枚举出来的接口：先安装驱动，再重新插拔接收器，接口就会出现。",
			color:      p.signal,
			canInstall: true,
		}
	}
}

// installLogCard shows the installer's output while it runs and after it
// ends. An installer that fails quietly is worse than one that fails loudly.
func (a *App) installLogCard(c *ui.Context, p palette, snap session.Snapshot) {
	detail := ""
	if snap.Installing {
		detail = "安装进行中：请在系统弹窗里允许以管理员身份运行。"
	}
	card(c, p, "安装记录", detail, func() {
		ui.Column(c).Gap(3).Children(func() {
			lines := snap.InstallLog
			if len(lines) > 14 {
				lines = lines[len(lines)-14:]
			}
			if len(lines) == 0 {
				ui.Text(c, "（没有输出）").FontSize(sizeLabel).TextColor(p.inkFaint)
			}
			for _, line := range lines {
				ui.Text(c, line).FontSize(sizeLabel).TextColor(p.inkDim).
					Font("monospace").Selectable().NoWrap()
			}
		})
	})
}

// nodeCard lists the receiver's device nodes and what Windows has bound to
// each, which is the evidence behind the state above.
func (a *App) nodeCard(c *ui.Context, p palette, snap session.Snapshot) {
	card(c, p, "设备节点", "接收器在系统里的每一个接口", func() {
		if len(snap.Status.Devices) == 0 {
			ui.Text(c, "还没有发现任何节点。").FontSize(sizeLabel).TextColor(p.inkFaint)
			return
		}
		for i, node := range snap.Status.Devices {
			if i > 0 {
				hairline(c, p)
			}
			ui.Column(c).Gap(4).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, shortInstance(node.InstanceID)).FontSize(sizeLabel).
						TextColor(p.ink).Font("monospace").Selectable().Grow(1).SingleLine().
						Ellipsis("…")
					if node.Present {
						pill(c, p, "在线", p.link)
					} else {
						pill(c, p, "未插入", p.inkFaint)
					}
					if node.IsControlInterface() {
						pill(c, p, "协议接口", p.signal)
					}
				})
				driver := node.Driver.Service
				if driver == "" {
					driver = "无驱动"
				}
				parts := []string{driver}
				if node.Driver.InfPath != "" {
					parts = append(parts, node.Driver.InfPath)
				}
				if node.Driver.Provider != "" {
					parts = append(parts, node.Driver.Provider)
				}
				if node.Driver.Version != "" {
					parts = append(parts, node.Driver.Version)
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, strings.Join(parts, "  ·  ")).FontSize(sizeUnit).
						TextColor(p.inkFaint).SingleLine().Ellipsis("…")
					if node.Description != "" {
						ui.Spacer(c)
						ui.Text(c, node.Description).FontSize(sizeUnit).TextColor(p.inkFaint).
							SingleLine().MaxWidth(220).TextAlign(ui.End)
					}
				})
			})
		}
	})
}

// shortInstance drops the "USB\" prefix, which adds nothing for a person
// reading a list of this device's own interfaces.
func shortInstance(id string) string {
	return strings.TrimPrefix(id, `USB\`)
}
