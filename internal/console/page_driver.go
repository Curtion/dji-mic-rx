package console

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
	"dji-mic-rx/internal/usb"
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

		if state.advice != "" {
			ui.Text(c, state.advice).FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
		}

		ui.Row(c).Gap(10).Wrap().Children(func() {
			install := ui.PrimaryButton(c, "安装驱动").
				Disabled(snap.Installing || !state.canInstall)
			if install.Clicked() {
				a.install(c)
			}
			uninstall := ui.Button(c, "卸载驱动").
				Disabled(snap.Installing || !state.canUninstall)
			if uninstall.Clicked() {
				a.ask = confirm{kind: confirmUninstall}
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

		hairline(c, p)

		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "安装需要管理员权限，Windows 会弹出授权窗口。安装只给接口 "+
				itoa(int(interfaceOf(snap)))+" 绑定 WinUSB，接收器的音频与按键接口保持系统驱动，录音不受影响。").
				FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)

			ui.Row(c).Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
				if snap.Tools.WdiSimpleSet {
					if ui.Button(c, "用 wdi-simple.exe 安装").Clicked() {
						a.runWdiSimple(c)
					}
				} else {
					if ui.Button(c, "选择 wdi-simple.exe…").Clicked() {
						a.pickWdiSimple(a.window)
					}
				}
				if snap.Tools.ZadigSet {
					if ui.Button(c, "用 Zadig 安装").Clicked() {
						a.openZadig(c)
					}
				}
				if snap.Tools.Elevated {
					pill(c, p, "已以管理员运行", p.link)
				}
			})

			if snap.Tools.WdiSimpleSet {
				ui.Text(c, "wdi-simple.exe："+snap.Tools.WdiSimple).
					FontSize(sizeUnit).TextColor(p.inkFaint).Font("monospace").Selectable().SingleLine()
			} else {
				ui.Text(c, "wdi-simple.exe 是 libwdi 的命令行安装器，它的驱动包带目录签名，Windows 更容易接受；本程序不附带它，请从 libwdi 项目获取后用上面的按钮指定。").
					FontSize(sizeUnit).TextColor(p.inkFaint).LineHeight(1.5)
			}
			if snap.Tools.ZadigSet {
				ui.Text(c, "Zadig："+snap.Tools.Zadig).
					FontSize(sizeUnit).TextColor(p.inkFaint).Font("monospace").Selectable().SingleLine()
			}
		})
	})

	a.nodeCard(c, p, snap)
	a.installLogCard(c, p, snap)
	a.manualCard(c, p, snap)
}

// driverStatus is what the driver page says at the top, decided by what the
// scan found.
type driverStatus struct {
	title   string
	summary string
	detail  string
	advice  string
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
			for _, node := range unknown {
				ids = append(ids, fmt.Sprintf("0x%04x:0x%04x", node.Vendor, node.Product))
			}
			return driverStatus{
				title:   "型号不在协议表里",
				summary: "检测到 DJI 设备（" + strings.Join(uniqueStrings(ids), "、") + "），但它不在已知接收器表里。",
				detail:  "不知道哪个接口带协议时，给接口装驱动是有风险的：装错了会让设备原本的功能失效，所以程序不会自动装。",
				advice:  "用下面的「复制诊断信息」把设备节点列表发出来，就能把型号加进表里。",
				color:   p.signal,
			}
		}
		return driverStatus{
			title:   "未检测到接收器",
			summary: "把接收器用 USB-C 线连到电脑，然后点「重新检测」。",
			detail:  "驱动要绑定的是接收器上的厂商接口。",
			advice:  "支持的型号：DJI Mic Mobile RX（DMMR01/DMMR02，USB 0x2ca3:0x4011）。它的音频接口会照常出现在系统的录音设备里。",
			color:   p.inkFaint,
		}
	case snap.Status.Ready():
		detail := fmt.Sprintf("接口 %d 由 WinUSB 驱动，麦克风状态可以读取，设置可以写入。",
			snap.Model.Interface)
		if snap.Connected {
			detail = fmt.Sprintf("接口 %d 由 WinUSB 驱动，状态正在推送。", snap.Model.Interface)
		}
		return driverStatus{
			title:        "驱动已就绪",
			summary:      "WinUSB 已经绑定在厂商接口上。",
			detail:       detail,
			advice:       "卸载驱动只会移除这个接口的绑定，卸下后重新插拔接收器即可回到 Windows 的默认状态。",
			color:        p.link,
			lit:          true,
			canUninstall: true,
		}
	case snap.Status.Control != nil && snap.Status.Control.Present:
		service := snap.Status.Control.Driver.Service
		summary := "接口存在，但没有驱动绑定。"
		if service != "" {
			summary = "接口绑定的是 " + service + " 驱动，程序需要 WinUSB。"
		}
		advice := "安装完成后请重新插拔接收器，让 Windows 用新驱动重新枚举这个接口。"
		if snap.UnsignedRefused {
			advice = "上一次安装被 Windows 拒绝：它不接受没有签名目录的第三方驱动包，而本程序自己生成的 INF 就是没有签名的。" +
				"请改用 Zadig（libwdi 的图形安装器，包带签名）：勾选 List All Devices，选中 Wireless Mic Rx 的 Interface 6，驱动选 WinUSB，装完重新插拔接收器。"
		}
		return driverStatus{
			title:      "接口需要 WinUSB 驱动",
			summary:    summary,
			detail:     "没有驱动时，Windows 不会把厂商接口的数据交给应用程序，所以接收器状态是空的。",
			advice:     advice,
			color:      p.alarm,
			canInstall: !snap.UnsignedRefused,
		}
	default:
		return driverStatus{
			title:   "厂商接口还没有出现",
			summary: "接收器已经连上（音频接口正常），但它的厂商接口没有出现在设备树上。",
			detail:  "驱动只能绑定已经枚举出来的接口，所以先安装驱动，再重新插拔接收器试试。",
			advice:  "如果重新插拔后仍然没有接口 " + itoa(int(interfaceOf(snap))) + "，可能是这份固件在当前模式下没有向电脑暴露厂商接口：请把接收器换一个 USB 端口（USB 2.0 与 3.x 枚举出的描述符可能不同），或先用 Zadig 看看它列出的接口。",
			color:   p.signal,

			canInstall: true,
		}
	}
}

func interfaceOf(snap session.Snapshot) int {
	if snap.HaveModel {
		return int(snap.Model.Interface)
	}
	return -1
}

// uniqueStrings keeps the first occurrence of each value, so a device with
// several nodes does not list its ids several times.
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// nodeCard lists the receiver's device nodes and what Windows has bound to
// each, which is the evidence behind the state above.
func (a *App) nodeCard(c *ui.Context, p palette, snap session.Snapshot) {
	card(c, p, "设备节点", "接收器在系统里的每一个接口", func() {
		if len(snap.Status.Devices) == 0 {
			ui.Text(c, "还没有发现任何节点。"+
				"").FontSize(sizeLabel).TextColor(p.inkFaint)
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

// installLogCard shows the installer's output while it runs and after it ends.
// An installer that fails quietly is worse than one that fails loudly.
func (a *App) installLogCard(c *ui.Context, p palette, snap session.Snapshot) {
	if !snap.Installing && len(snap.InstallLog) == 0 && snap.DriverLogPath == "" && !snap.UnsignedRefused {
		return
	}
	title := "安装记录"
	detail := ""
	switch {
	case snap.Installing:
		detail = "安装进行中：请在系统弹窗里允许以管理员身份运行。"
	case snap.UnsignedRefused:
		detail = "Windows 拒绝了没有签名的驱动包，这就是本机装不上的原因。"
	case len(snap.InstallLog) == 0 && snap.DriverLogPath != "":
		detail = "安装程序没有任何输出：多半是提权没有完成（授权窗口没出现或被取消）。"
	}
	card(c, p, title, detail, func() {
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

		if snap.DriverLogPath != "" {
			hairline(c, p)
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "安装日志文件（每次安装各留一份）").FontSize(sizeLabel).TextColor(p.inkDim)
				ui.Text(c, snap.DriverLogPath).FontSize(sizeLabel).TextColor(p.ink).
					Font("monospace").Selectable().NoWrap().Ellipsis("…")
			})
			ui.Row(c).Gap(10).Wrap().Children(func() {
				if ui.Button(c, "打开日志文件夹").Clicked() {
					if err := usb.StartDetached(filepath.Dir(snap.DriverLogPath)); err != nil {
						c.Toast("打开文件夹失败：" + err.Error())
					}
				}
				if ui.Button(c, "复制日志内容").Clicked() {
					c.WriteClipboard(strings.Join(snap.InstallLog, "\n"))
					c.Toast("安装日志已复制")
				}
			})
		}
	})
}

// manualCard keeps the by-hand instructions available without putting them in
// the way of the button above them — unless Windows refused the package this
// app writes itself, in which case the by-hand route is the answer and the
// card opens itself.
func (a *App) manualCard(c *ui.Context, p palette, snap session.Snapshot) {
	if snap.UnsignedRefused {
		a.showManual = true
	}
	ui.Collapsible(c, "自动安装被拒绝时的手动步骤", &a.showManual, func() {
		ui.Column(c).Gap(10).Padding(16).Radius(10).
			Background(p.panel).Border(1, p.line).Children(func() {
			ui.Text(c, "Windows 可能因为驱动包没有签名而拒绝安装。手动步骤绕开签名检查："+
				"").FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
			steps := []string{
				"打开 Zadig（libwdi 的图形版安装器，本程序不附带）。",
				"在列表里勾选「List All Devices」，选中 Wireless Mic Rx 的 Interface 6。",
				"右侧驱动选 WinUSB，点「Install Driver」，等待完成。",
				"回到本程序点「重新检测」，或重新插拔接收器。",
			}
			for i, step := range steps {
				ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
					ui.Text(c, itoa(i+1)).FontSize(sizeLabel).TextColor(p.signal).
						Width(14).FontFeatures(tabular)
					ui.Text(c, step).FontSize(sizeLabel).TextColor(p.inkDim).Grow(1).
						LineHeight(1.5)
				})
			}
			ui.Link(c, "libwdi / Zadig 项目页面", "https://github.com/pbatard/libwdi")
		})
	})
}

// aboutPage says what the program is, where the protocol came from, and that
// it is not DJI's.
func (a *App) aboutPage(c *ui.Context, p palette, snap session.Snapshot) {
	card(c, p, "这是什么", "非官方工具，与 DJI 没有关系", func() {
		ui.Text(c, "DJI Mic 接收器控制台在 Windows 上读出 DJI Mic Mini 系列接收器的实时状态，并写入它的设置。"+
			"").FontSize(sizeBody).TextColor(p.ink).LineHeight(1.5)
		ui.Text(c, "接收器会以约每秒十次主动推送状态，程序只被动接收；改动设置时向它发送一条命令，并等待它的确认。"+
			"").FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
		ui.Text(c, "驱动部分只为厂商接口安装 Microsoft 的 WinUSB 驱动，接收器的音频接口（系统的录音设备）与按键接口不受影响。"+
			"").FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
	})

	card(c, p, "当前设备", "", func() {
		ui.Column(c).Gap(6).Children(func() {
			specRow(c, p, "设备名称", blank(snap.State.RX.Name), false)
			specRow(c, p, "USB 标识", usbID(snap), true)
			specRow(c, p, "协议接口", interfaceLabel(snap), true)
			specRow(c, p, "协议端点", endpointsLabel(snap), true)
			specRow(c, p, "协议版本", dialectText(snap), false)
			specRow(c, p, "序列号", blank(snap.State.RX.Serial), true)
			specRow(c, p, "接收器固件", blank(snap.State.RX.Firmware), true)
			for i, tx := range snap.State.TX {
				label := fmt.Sprintf("发射器 %d", i+1)
				value := "未连接"
				if tx.Present {
					value = fmt.Sprintf("%s · 固件 %s", blank(tx.Serial), blank(tx.Firmware))
				}
				specRow(c, p, label, value, tx.Present)
			}
		})
	})

	card(c, p, "协议来源", "社区逆向整理的资料", func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "帧格式、CRC 与命令表来自 ShadowBitBasher 的 DJI-Mic-Control 项目，字段偏移与位掩码经 usokawa 的 dji-mic-mo 交叉核对。"+
				"").FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
			ui.Link(c, "ShadowBitBasher/DJI-Mic-Control（含 PROTOCOL.md）",
				"https://github.com/ShadowBitBasher/DJI-Mic-Control")
			ui.Link(c, "usokawa/dji-mic-mo（字段表）",
				"https://github.com/usokawa/dji-mic-mo")
			ui.Link(c, "pbatard/libwdi（驱动安装）",
				"https://github.com/pbatard/libwdi")
		})
	})

	card(c, p, "本程序", "", func() {
		ui.Text(c, "界面用 Go 与 mygo 的原生 UI 工具包绘制，不使用网页视图；USB 访问通过 WinUSB 直接完成，没有第三方驱动依赖。"+
			"").FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
		ui.Row(c).Gap(10).Wrap().Children(func() {
			if ui.Button(c, "复制诊断信息").Clicked() {
				a.copyDiagnostics(c)
			}
			if ui.Button(c, "打开驱动页").Clicked() {
				a.ShowPage("driver")
			}
		})
	})
}

func usbID(snap session.Snapshot) string {
	if !snap.HaveModel {
		return "—"
	}
	return fmt.Sprintf("0x%04x:0x%04x", snap.Model.Vendor, snap.Model.Product)
}

func interfaceLabel(snap session.Snapshot) string {
	if !snap.HaveModel {
		return "—"
	}
	label := fmt.Sprintf("接口 %d", snap.Model.Interface)
	if snap.Model.AltSetting != 0 {
		label += fmt.Sprintf("（alternate setting %d）", snap.Model.AltSetting)
	}
	return label
}

func endpointsLabel(snap session.Snapshot) string {
	if !snap.HaveModel {
		return "—"
	}
	return fmt.Sprintf("0x%02x 写 / 0x%02x 读", snap.Model.BulkOut, snap.Model.BulkIn)
}

// interfaceOfModel is kept for the driver page's explanations.
var _ = duml.Model{}
