package console

import (
	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// settingsPage shows one group of settings. Every row is built from the
// setting registry, so a control exists exactly when the receiver has a
// command for it, and says why when it does not.
func (a *App) settingsPage(c *ui.Context, p palette, snap session.Snapshot, group duml.Group) {
	settings := make([]duml.Setting, 0, len(duml.Settings))
	for _, s := range duml.Settings {
		if s.Group == group && !s.PerTransmitter {
			settings = append(settings, s)
		}
	}

	card(c, p, string(group), groupDetail(group), func() {
		if !snap.Connected {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconInfo).FontSize(13).TextColor(p.inkFaint)
				ui.Text(c, "接收器未连接，设置暂时只能查看。").
					FontSize(sizeLabel).TextColor(p.inkFaint)
			})
		}
		for i, setting := range settings {
			if i > 0 {
				hairline(c, p)
			}
			a.settingRow(c, p, snap, setting)
		}
	})

	if note := groupNote(group); note != "" {
		card(c, p, "说明", "", func() {
			ui.Text(c, note).FontSize(sizeLabel).TextColor(p.inkDim).LineHeight(1.5)
		})
	}
}

// settingRow draws one setting: its name, what it does, and the control that
// changes it — or, when it cannot be changed here, the reason.
func (a *App) settingRow(c *ui.Context, p palette, snap session.Snapshot, setting duml.Setting) {
	// While the protocol version is unknown, assume the older one: a control
	// that appears a moment later is better than one that disappears.
	dialect := snap.State.Dialect
	if !snap.State.DialectKnown {
		dialect = duml.V1
	}
	value := snap.State.Setting(setting.ID)
	available := setting.Available(dialect, a.product(snap))

	if !available {
		fieldRow(c, p, setting.Label, setting.Detail, func() {
			if setting.Kind == duml.KindToggle {
				unavailableNote(c, p, unavailableReason(setting, dialect))
				return
			}
			unavailableNote(c, p, unavailableReason(setting, dialect))
		})
		return
	}

	if setting.ReadOnlyOnV1 && dialect == duml.V1 {
		current, known := setting.Option(value)
		fieldRow(c, p, setting.Label, setting.Detail, func() {
			if known {
				pill(c, p, current.Label, p.inkDim)
			}
			unavailableNote(c, p, "v1 固件要按发射器的电源键切换")
		})
		return
	}

	switch setting.Kind {
	case duml.KindToggle:
		on := value == setting.On
		fieldRow(c, p, setting.Label, setting.Detail, func() {
			// The switch joins the row it is built in, so it has to be
			// created here rather than before the row.
			reply := ui.Switch(c, &on).Label(setting.Label).Disabled(!snap.Connected)
			if setting.Reboots {
				ui.Icon(c, iconWarning).FontSize(13).TextColor(p.signal).
					Tooltip("更改后接收器会重启")
			}
			if !reply.Changed() {
				return
			}
			next := setting.Off
			if on {
				next = setting.On
			}
			if setting.Reboots {
				a.ask = confirm{kind: confirmReboot, setting: setting.ID, value: next}
				return
			}
			a.setSetting(c, setting.ID, next)
		})

	default:
		selected := 0
		for i, option := range setting.Options {
			if option.Value == value {
				selected = i
			}
		}
		labels := make([]string, 0, len(setting.Options))
		for _, option := range setting.Options {
			labels = append(labels, option.Label)
		}
		fieldRow(c, p, setting.Label, setting.Detail, func() {
			reply := ui.Segmented(c, &selected, labels...).
				Label(setting.Label).Disabled(!snap.Connected)
			if reply.Changed() && selected >= 0 && selected < len(setting.Options) {
				a.setSetting(c, setting.ID, setting.Options[selected].Value)
			}
		})
	}
}

// unavailableReason explains why a setting is not offered, naming the firmware
// or the model that has it.
func unavailableReason(setting duml.Setting, dialect duml.Dialect) string {
	if setting.V1 == 0 {
		return "需要 v2 固件（DJI Mic Mini 2）"
	}
	if setting.RequiresProduct != "" {
		return "只有 " + setting.RequiresProduct + " 支持"
	}
	return "当前固件不支持"
}

// groupDetail is the one line under a group's title.
func groupDetail(group duml.Group) string {
	switch group {
	case duml.GroupAudio:
		return "收音与降噪"
	case duml.GroupPower:
		return "省电与联动"
	default:
		return "硬件行为"
	}
}

// groupNote is the extra explanation a group needs, written where a person
// will meet the setting rather than in a manual.
func groupNote(group duml.Group) string {
	switch group {
	case duml.GroupAudio:
		return "立体声与安全音轨共用第二声道，打开一个会让接收器关掉另一个。"
	case duml.GroupPower:
		return "自动关机在 15 分钟无操作后触发；「跟随相机开关机」只对相机有效，USB 供电时不受影响。"
	default:
		return "关闭发射器指示灯后，录制时发射器不再亮灯；「免拔插外放」改动后接收器会重启，状态需要几秒才能回来。"
	}
}
