package duml

import (
	"time"
)

// RXInfo is what the app knows about the receiver itself.
type RXInfo struct {
	// Name is the product name the receiver reports, e.g. "DJI Mic Mini 2".
	Name string
	// Serial and Firmware are reported by the identity push (v2) or inside
	// the heartbeat (v1).
	Serial, Firmware string
	// BatteryGauge is the receiver's 3-bit gauge, 1 (full) to 7 (empty);
	// 0 means there is no battery to report. A receiver with a battery
	// reports it on v2 firmware; the mobile receiver (DMMR02) has none and
	// leaves the bits clear.
	BatteryGauge int
	// Charging is true while the receiver is charging.
	Charging bool
	// GainDial is the receiver's physical gain dial in dB, and HasGain
	// reports whether it is known (v2 firmware only).
	GainDial int
	HasGain  bool
}

// TXInfo is what the app knows about one transmitter. Fields that v1 firmware
// does not report stay zero.
type TXInfo struct {
	// Present is true while the transmitter is connected.
	Present bool
	// Unit is the physical unit number, 1 or 2.
	Unit int
	// Name, Serial and Firmware come from the identity push on v2, and from
	// the heartbeat on v1 (no product name there).
	Name, Serial, Firmware string
	// Level is the live audio input level in the device's own units, or -1
	// while unknown.
	Level int
	// BatteryGauge is 1 (full) to 7 (empty); 0 means it is not reported,
	// which is always the case on v1 firmware, and behind the DJI Mic
	// series mobile receiver (DMMR02) as well.
	BatteryGauge int
	// Charging is true while the transmitter sits in its charging case.
	Charging bool
	// VoiceTone is "standard", "rich" or "bright"; empty when unknown.
	VoiceTone string
}

// BatteryPercent converts the gauge for display, reporting false when unknown.
func (t TXInfo) BatteryPercent() (int, bool) { return BatteryPercent(t.BatteryGauge) }

// BatteryPercent converts the gauge for display, reporting false when it is
// not reported.
func (r RXInfo) BatteryPercent() (int, bool) { return BatteryPercent(r.BatteryGauge) }

// State is everything the app knows about a receiver at one moment. Decoders
// return a new State built from the previous one, so values carried by one
// kind of status frame survive frames that do not repeat them (on v2
// firmware, identity and audio level arrive in pushes of their own).
type State struct {
	// Dialect is the protocol version seen on the wire.
	Dialect Dialect
	// DialectKnown is false until a status frame identifies the version.
	DialectKnown bool
	// Settings holds the current value slug of every setting the device has
	// reported, keyed by Setting.ID.
	Settings map[string]string

	RX RXInfo
	TX [2]TXInfo

	// Packets counts decoded status frames and Updated is when the last one
	// arrived.
	Packets int
	Updated time.Time
}

// NewState returns an empty state, ready to be fed frames.
func NewState() State {
	return State{
		Settings: map[string]string{},
		RX:       RXInfo{BatteryGauge: 0},
		TX: [2]TXInfo{
			{Unit: 1, Level: -1},
			{Unit: 2, Level: -1},
		},
	}
}

// Clone returns a copy safe to modify, sharing nothing with s.
func (s State) Clone() State {
	next := s
	next.Settings = make(map[string]string, len(s.Settings))
	for k, v := range s.Settings {
		next.Settings[k] = v
	}
	return next
}

// Setting returns the current value slug of a setting.
func (s State) Setting(id string) string { return s.Settings[id] }

// TXCount is how many transmitters are powered on.
func (s State) TXCount() int {
	n := 0
	for i := range s.TX {
		if s.TX[i].Present {
			n++
		}
	}
	return n
}

// Mode returns the display name of a transmitter's voice tone.
func VoiceToneLabel(value string) string {
	switch value {
	case "rich":
		return "醇厚"
	case "bright":
		return "明亮"
	case "standard":
		return "标准"
	}
	return "未知"
}
