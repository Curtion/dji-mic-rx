package console

import (
	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// settingsPage shows every shared setting in one list, grouped by what it
// affects. Every row is built from the setting registry, so a control exists
// exactly when the receiver has a command for it, and says why when it does
// not.
func (a *App) settingsPage(c *ui.Context, p palette, snap session.Snapshot) {
	if !snap.Connected {
		card(c, p, "", "", func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconInfo).FontSize(13).TextColor(p.inkFaint)
				ui.Text(c, "接收器未连接，设置暂时只能查看。").
					FontSize(sizeLabel).TextColor(p.inkFaint)
			})
		})
	}
	for _, group := range []duml.Group{duml.GroupAudio, duml.GroupPower, duml.GroupDevice} {
		a.settingsGroup(c, p, snap, group)
	}

	card(c, p, "可以在硬件上直接操作的事", "有些功能不需要这个程序", func() {
		ui.Column(c).Gap(8).Children(func() {
			for _, line := range []string{
				"增益：转动接收器左上侧的拨轮，五档、每档 6 dB；程序只能读出当前位置。",
				"单声道/立体声：双击接收器的配对键即可切换。",
				"配对：发射器与接收器都长按配对键两秒。",
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

// settingsGroup draws one section of the list.
func (a *App) settingsGroup(c *ui.Context, p palette, snap session.Snapshot, group duml.Group) {
	card(c, p, string(group), "", func() {
		first := true
		for _, setting := range duml.Settings {
			if setting.Group != group || setting.PerTransmitter {
				continue
			}
			if !first {
				hairline(c, p)
			}
			first = false
			a.settingRow(c, p, snap, setting)
		}
	})
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

	if !setting.Available(dialect, a.product(snap)) {
		fieldRow(c, p, setting.Label, setting.Detail, func() {
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
			if !reply.Changed() {
				return
			}
			next := setting.Off
			if on {
				next = setting.On
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
