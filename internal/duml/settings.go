package duml

import "strings"

// Kind is how a setting is presented.
type Kind int

const (
	// KindToggle is a switch: two options, one of them "on".
	KindToggle Kind = iota
	// KindChoice is a small set of named modes.
	KindChoice
)

// Group is the section a setting belongs to.
type Group string

const (
	// GroupAudio covers noise handling and channel routing.
	GroupAudio Group = "音频"
	// GroupPower covers the device's power behaviour.
	GroupPower Group = "电源与开机"
	// GroupDevice covers indicators and other hardware behaviour.
	GroupDevice Group = "设备"
)

// Option is one value of a setting: the slug kept in State.Settings, the
// label the UI shows, and the byte that goes on the wire.
type Option struct {
	Value string
	Label string
	Wire  byte
}

// Setting describes one receiver setting: how to show it, and how to write it
// to a device speaking either protocol version.
type Setting struct {
	// ID keys State.Settings and the command log.
	ID string
	// Label and Detail are shown in the UI.
	Label, Detail string
	Group         Group
	Kind          Kind

	// V1 is the v1 command id, zero when v1 firmware has no such setting.
	V1 uint16
	// V2 is the v2 command id.
	V2 uint16
	// Target is the v2 unit the command addresses. v1 always addresses the
	// receiver.
	Target Target
	// Options are the values, in the order the UI shows them.
	Options []Option
	// Off is the value that means "off", for toggles.
	Off, On string

	// RequiresProduct, when set, is a substring of the product name the
	// setting exists on (Voice Tone is DJI Mic Mini 2 only).
	RequiresProduct string
	// PerTransmitter is true when the receiver keeps this setting per
	// transmitter instead of mirroring it across them, so the UI places it
	// with the transmitter rather than with shared settings.
	PerTransmitter bool
	// ReadOnlyOnV1 is true where v1 firmware reports the setting but has no
	// command to change it (only the transmitter's own button can).
	ReadOnlyOnV1 bool
}

// Command returns the command id and target to use for a setting on dialect d.
func (s Setting) Command(d Dialect) (cmd uint16, target Target, ok bool) {
	if d == V2 {
		if s.V2 == 0 {
			return 0, 0, false
		}
		return s.V2, s.Target, true
	}
	if s.V1 == 0 {
		return 0, 0, false
	}
	// v1 has no target field, so a per-transmitter setting is broadcast by
	// writing the value that applies to all of them.
	return s.V1, TargetRX, true
}

// Available reports whether a device can use this setting at all.
func (s Setting) Available(d Dialect, product string) bool {
	if _, _, ok := s.Command(d); !ok {
		return false
	}
	if s.RequiresProduct != "" && !strings.Contains(product, s.RequiresProduct) {
		return false
	}
	return true
}

// Option returns the option with the given value slug.
func (s Setting) Option(value string) (Option, bool) {
	for _, o := range s.Options {
		if o.Value == value {
			return o, true
		}
	}
	return Option{}, false
}

// WireOf returns the wire byte for a value slug.
func (s Setting) WireOf(value string) (byte, bool) {
	o, ok := s.Option(value)
	return o.Wire, ok
}

func toggle(off, on Option) []Option { return []Option{off, on} }

var (
	offOption = Option{Value: "off", Label: "关闭", Wire: 0x00}
	onOption  = Option{Value: "on", Label: "开启", Wire: 0x01}
)

// Settings is every setting the app can read and (except where noted) write,
// in the order the UI shows it.
var Settings = []Setting{
	{
		ID:      "noise-cancel",
		Label:   "降噪强度",
		Detail:  "环境噪声较大时用「强」",
		Group:   GroupAudio,
		Kind:    KindChoice,
		V1:      0x031d,
		V2:      0x0037,
		Target:  TargetAllTX,
		Options: []Option{{Value: "basic", Label: "普通", Wire: 0x00}, {Value: "strong", Label: "强", Wire: 0x01}},
	},
	{
		ID:      "noise-cancel-power",
		Label:   "降噪开关",
		Detail:  "整体开启或关闭降噪",
		Group:   GroupAudio,
		Kind:    KindToggle,
		V2:      0x0038,
		Target:  TargetAllTX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
		// v1 firmware reports this (the transmitter's button sets it) but
		// offers no command for it.
		ReadOnlyOnV1: true,
	},
	{
		ID:      "noise-cancel-button",
		Label:   "按键切换降噪",
		Detail:  "允许短按发射器电源键开关降噪",
		Group:   GroupAudio,
		Kind:    KindToggle,
		V2:      0x000f,
		Target:  TargetAllTX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:      "low-cut",
		Label:   "低切",
		Detail:  "滤掉低频风噪与隆隆声",
		Group:   GroupAudio,
		Kind:    KindToggle,
		V1:      0x0303,
		V2:      0x0003,
		Target:  TargetAllTX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:      "stereo",
		Label:   "声道模式",
		Detail:  "单声道为两路混音，立体声分左右声道；与安全音轨共用第二声道，开一个会关另一个",
		Group:   GroupAudio,
		Kind:    KindChoice,
		V1:      0x0008,
		V2:      0x0008,
		Target:  TargetRX,
		Options: []Option{{Value: "mono", Label: "单声道", Wire: 0x00}, {Value: "stereo", Label: "立体声", Wire: 0x02}},
	},
	{
		ID:      "safety-track",
		Label:   "安全音轨",
		Detail:  "第二声道低 6 dB 备份，防止爆音；与立体声共用第二声道，开一个会关另一个",
		Group:   GroupAudio,
		Kind:    KindToggle,
		V1:      0x0021,
		V2:      0x0021,
		Target:  TargetRX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:      "clip-limiter",
		Label:   "自动限幅",
		Detail:  "音量过载时自动压低",
		Group:   GroupAudio,
		Kind:    KindToggle,
		V1:      0x001e,
		V2:      0x001e,
		Target:  TargetRX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:      "auto-off-15m",
		Label:   "接收器自动关机",
		Detail:  "15 分钟无操作后自动关机",
		Group:   GroupPower,
		Kind:    KindToggle,
		V1:      0x0010,
		V2:      0x0010,
		Target:  TargetRX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:     "tx-auto-off-15m",
		Label:  "发射器自动关机",
		Detail: "15 分钟无操作后自动关机",
		Group:  GroupPower,
		Kind:   KindToggle,
		// v2 split the receiver and transmitter timers apart; they share one
		// command id and differ only in the target.
		V2:      0x0010,
		Target:  TargetAllTX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:      "camera-power",
		Label:   "跟随相机开关机",
		Detail:  "相机开机时接收器自动开机",
		Group:   GroupPower,
		Kind:    KindToggle,
		V1:      0x0020,
		V2:      0x0020,
		Target:  TargetRX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:     "mic-leds",
		Label:  "发射器指示灯",
		Detail: "关闭后录制时不亮灯",
		Group:  GroupDevice,
		Kind:   KindToggle,
		V1:     0x030a,
		V2:     0x000a,
		Target: TargetAllTX,
		// The wire values are inverted: 0x00 is on, 0x02 is off.
		Options: toggle(Option{Value: "on", Label: "开启", Wire: 0x00}, Option{Value: "off", Label: "关闭", Wire: 0x02}),
		Off:     "off",
		On:      "on",
	},
	{
		ID:      "plug-free",
		Label:   "免拔插外放",
		Detail:  "接收器从相机/手机拔出后也用外放；更改后接收器会重启，几秒后自动恢复",
		Group:   GroupDevice,
		Kind:    KindToggle,
		V1:      0x0023,
		V2:      0x0023,
		Target:  TargetRX,
		Options: toggle(offOption, onOption),
		Off:     "off",
		On:      "on",
	},
	{
		ID:     "voice-tone",
		Label:  "音色",
		Detail: "每支发射器单独设置",
		Group:  GroupAudio,
		Kind:   KindChoice,
		V2:     0x0029,
		Target: TargetRX, // replaced per transmitter by the caller
		Options: []Option{
			{Value: "standard", Label: "标准", Wire: 0x00},
			{Value: "rich", Label: "醇厚", Wire: 0x01},
			{Value: "bright", Label: "明亮", Wire: 0x02},
		},
		RequiresProduct: "DJI Mic Mini 2",
		PerTransmitter:  true,
	},
}

// SettingByID finds a setting in the registry.
func SettingByID(id string) (Setting, bool) {
	for _, s := range Settings {
		if s.ID == id {
			return s, true
		}
	}
	return Setting{}, false
}

// BatteryPercent converts the receiver's 3-bit battery gauge to a percentage.
// The gauge runs 1 (full) to 7 (empty, immediately before shutdown); values
// in between are evenly spaced, and 0, which has not been observed in
// practice, means unknown.
func BatteryPercent(gauge int) (int, bool) {
	if gauge < 1 || gauge > 7 {
		return 0, false
	}
	return (7 - gauge) * 100 / 6, true
}

// levelMax normalises the device's raw audio level for the meter. The
// receivers report a level in their own units rather than a percentage; the
// UI shows the raw number and scales the meter to this.
const levelMax = 100

// LevelFraction returns 0..1 for the meter, and false when the level is
// unknown.
func LevelFraction(raw int) (float64, bool) {
	if raw < 0 {
		return 0, false
	}
	f := float64(raw) / levelMax
	if f > 1 {
		f = 1
	}
	return f, true
}
