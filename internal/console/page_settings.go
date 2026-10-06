package console

import (
	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// settingsPage shows the settings of the protocol the receiver speaks — and
// only those. v1 and v2 get their own lists rather than one list with the
// other version's gaps explained away; a receiver whose version is not known
// yet gets a waiting card instead of a guess.
func (a *App) settingsPage(c *ui.Context, p palette, snap session.Snapshot) {
	dialect, known := dialectOf(snap)
	if !snap.Connected || !known {
		a.waitingCard(c, p, snap)
		return
	}
	for _, group := range []duml.Group{duml.GroupAudio, duml.GroupPower, duml.GroupDevice} {
		list := settingsInGroup(dialect, a.product(snap), group)
		if len(list) == 0 {
			continue
		}
		a.settingsGroup(c, p, snap, group, list)
	}
	if list := readOnlySettings(dialect); len(list) > 0 {
		a.readOnlyGroup(c, p, snap, list)
	}
	a.handHeldCard(c, p, dialect)
}

// settingsInGroup returns the settings of one group this protocol can write,
// in registry order. Per-transmitter settings live on the transmitter's card
// and are never listed here.
func settingsInGroup(dialect duml.Dialect, product string, group duml.Group) []duml.Setting {
	var list []duml.Setting
	for _, setting := range duml.Settings {
		if setting.PerTransmitter || setting.Group != group {
			continue
		}
		if !setting.Available(dialect, product) {
			continue
		}
		list = append(list, setting)
	}
	return list
}

// readOnlySettings returns the states a protocol reports but has no command
// to change — on v1, the transmitter's own button holds the pen.
func readOnlySettings(dialect duml.Dialect) []duml.Setting {
	if dialect != duml.V1 {
		return nil
	}
	var list []duml.Setting
	for _, setting := range duml.Settings {
		if setting.ReadOnlyOnV1 && !setting.PerTransmitter {
			list = append(list, setting)
		}
	}
	return list
}

// handHeldCard is the card of things done on the device itself; the two
// generations differ in how much of the gain dial the program can see.
func (a *App) handHeldCard(c *ui.Context, p palette, dialect duml.Dialect) {
	gain := "增益：转动接收器左上侧的拨轮，五档、每档 6 dB；程序只能读出当前位置。"
	if dialect == duml.V1 {
		gain = "增益：转动接收器左上侧的拨轮，五档、每档 6 dB；v1 固件不上报位置，以旋钮刻度为准。"
	}
	card(c, p, "可以在硬件上直接操作的事", "有些功能不需要这个程序", func() {
		ui.Column(c).Gap(8).Children(func() {
			for _, line := range []string{
				gain,
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
func (a *App) settingsGroup(c *ui.Context, p palette, snap session.Snapshot, group duml.Group, settings []duml.Setting) {
	card(c, p, string(group), "", func() {
		for i, setting := range settings {
			if i > 0 {
				hairline(c, p)
			}
			a.settingRow(c, p, snap, setting)
		}
	})
}

// readOnlyGroup lists the states only the hardware can change, as readings
// rather than as controls.
func (a *App) readOnlyGroup(c *ui.Context, p palette, snap session.Snapshot, settings []duml.Setting) {
	card(c, p, "在发射器上调整", "程序能读出状态，改变要按发射器自己的按键", func() {
		for i, setting := range settings {
			if i > 0 {
				hairline(c, p)
			}
			value := snap.State.Setting(setting.ID)
			fieldRow(c, p, setting.Label, readOnlyHint(setting), func() {
				if option, ok := setting.Option(value); ok {
					pill(c, p, option.Label, p.inkDim)
					return
				}
				ui.Text(c, "—").FontSize(sizeLabel).TextColor(p.inkFaint)
			})
		}
	})
}

// readOnlyHint says where the device changes the value instead.
func readOnlyHint(setting duml.Setting) string {
	if setting.ID == "noise-cancel-power" {
		return "短按发射器的电源键切换"
	}
	return "在发射器上调整"
}

// settingRow draws one setting: its name, what it does, and the control that
// changes it. Every row the page lists is one the protocol can write.
func (a *App) settingRow(c *ui.Context, p palette, snap session.Snapshot, setting duml.Setting) {
	value := snap.State.Setting(setting.ID)

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
