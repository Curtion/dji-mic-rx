package duml

import (
	"strings"
	"time"
)

// Layout constants, from the protocol reference.
const (
	// v1 heartbeat: 14-byte header, two transmitter slots (one of two sizes
	// each), then the receiver's entry.
	v1HeaderLen   = 14
	v1PresentLen  = 23
	v1AbsentLen   = 9
	v1ReceiverLen = 22

	// v2 status push: a fixed header, then one 32-byte slot per connected
	// transmitter and a 2-byte CRC.
	v2HeaderLen    = 52
	v2SlotLen      = 32
	v2ConnectedOff = 44

	// v2 identity and audio-level pushes: records from offset 14, each with
	// a 6-byte header.
	v2RecordsStart = 14
	v2RecordHdrLen = 6
)

// Record tags shared by the v2 identity and audio-level pushes.
const (
	tagIdentity = 0x01
	tagLevel    = 0x05
	tagName     = 0x06
)

// Decode feeds one complete frame to the state, returning the new state and
// whether the frame was a status frame of the dialect it claims. Frames that
// are not status pushes (commands, ACKs) and frames whose shape does not match
// either dialect are ignored, and the previous state is returned unchanged.
//
// The returned state is always a copy: the caller keeps ownership of the
// state it passes in.
func Decode(s State, frame []byte) (State, bool) {
	dialect, ok := DialectOf(frame)
	if !ok {
		return s, false
	}

	next := s.Clone()
	next.Dialect = dialect
	next.DialectKnown = true

	var decoded bool
	switch dialect {
	case V1:
		decoded = decodeV1(&next, frame)
	case V2:
		// Try the fixed status shape first, then the record-based pushes.
		decoded = decodeV2Status(&next, frame) || decodeV2Records(&next, frame)
	}
	if !decoded {
		return s, false
	}

	next.Packets++
	next.Updated = time.Now()
	return next, true
}

// v1Slot is one transmitter slot of a v1 heartbeat.
type v1Slot struct {
	present bool
	state   byte
	flags   byte
	ledOff  bool
	ledRead bool
	length  int
}

// readV1Slot parses the slot at off, present entry or absent stub.
func readV1Slot(f []byte, off int) (v1Slot, bool) {
	if off+1 >= len(f) {
		return v1Slot{}, false
	}
	flags := f[off+1]
	switch {
	case flags&0x20 != 0: // present
		if off+v1PresentLen > len(f) {
			return v1Slot{}, false
		}
		return v1Slot{
			present: true,
			state:   f[off],
			flags:   flags,
			ledOff:  f[off+3]&0x80 != 0,
			ledRead: true,
			length:  v1PresentLen,
		}, true
	default: // absent stub
		if off+v1AbsentLen > len(f) {
			return v1Slot{}, false
		}
		return v1Slot{state: f[off], flags: flags, length: v1AbsentLen}, true
	}
}

// decodeV1 decodes a v1 heartbeat, which carries every setting and both
// transmitters in one frame.
func decodeV1(s *State, f []byte) bool {
	if len(f) < v1HeaderLen+v1ReceiverLen {
		return false
	}
	slotA, ok := readV1Slot(f, v1HeaderLen)
	if !ok {
		return false
	}
	slotB, ok := readV1Slot(f, v1HeaderLen+slotA.length)
	if !ok {
		return false
	}
	rxOff := v1HeaderLen + slotA.length + slotB.length
	if len(f) < rxOff+v1ReceiverLen {
		return false
	}

	// The state and flag bytes are meaningful even in an absent stub; a
	// present slot is preferred so live data wins over a stale stub.
	slot := slotA
	for _, candidate := range []v1Slot{slotA, slotB} {
		if candidate.present {
			slot = candidate
			break
		}
	}

	s.Settings["noise-cancel"] = pick(slot.state&0x20 != 0, "strong", "basic")
	s.Settings["noise-cancel-power"] = pick(slot.flags&0x01 != 0, "on", "off")
	s.Settings["low-cut"] = pick(slot.state&0x04 != 0, "on", "off")
	if slotA.ledRead || slotB.ledRead {
		ledOff := slotA.ledRead && slotA.ledOff || slotB.ledRead && slotB.ledOff
		s.Settings["mic-leds"] = pick(ledOff, "off", "on")
	}

	rx0, rx1 := f[rxOff], f[rxOff+1]
	s.Settings["camera-power"] = pick(rx0&0x01 != 0, "on", "off")
	s.Settings["auto-off-15m"] = pick(rx0&0x02 != 0, "on", "off")
	s.Settings["stereo"] = pick(rx0&0x08 != 0, "stereo", "mono")
	s.Settings["safety-track"] = pick(rx1&0x80 != 0, "on", "off")
	s.Settings["clip-limiter"] = pick(rx1&0x20 != 0, "on", "off")
	s.Settings["plug-free"] = pick(f[rxOff+21] == 0x01, "on", "off")

	s.RX.Firmware = firmwareString(f[rxOff+2 : rxOff+6])
	s.RX.Serial = asciiField(f[rxOff+7 : rxOff+21])

	for i, candidate := range []v1Slot{slotA, slotB} {
		tx := &s.TX[i]
		tx.Unit = i + 1
		if !candidate.present {
			*tx = TXInfo{Unit: i + 1, Level: -1}
			continue
		}
		off := v1HeaderLen
		if i == 1 {
			off += slotA.length
		}
		tx.Present = true
		tx.Level = int(f[off+2])
		tx.Firmware = firmwareString(f[off+4 : off+8])
		tx.Serial = asciiField(f[off+9 : off+v1PresentLen])
		tx.BatteryGauge = 0 // v1 firmware does not report battery level
		tx.Charging = false
		tx.VoiceTone = ""
		tx.Name = ""
	}
	return true
}

// decodeV2Status decodes the v2 status push: connection, the settings that
// mirror across transmitters, the receiver's gain dial, and each connected
// transmitter's own battery, charging state and voice tone.
func decodeV2Status(s *State, f []byte) bool {
	if len(f) < v2HeaderLen+2 {
		return false
	}
	slots := (len(f) - (v2HeaderLen + 2)) / v2SlotLen
	if slots > 2 || (len(f)-(v2HeaderLen+2))%v2SlotLen != 0 {
		return false
	}

	flags1, flags2 := f[20], f[21]
	flags3 := f[48]
	s.RX.GainDial = int(int8(f[22]))
	s.RX.HasGain = true
	// The Mic Mini 2 reports the receiver's own battery in bits 5..7 and its
	// charging state in bit 4 of offset 21.
	s.RX.BatteryGauge = int(f[21]>>5) & 0x07
	s.RX.Charging = f[21]&0x10 != 0

	s.Settings["noise-cancel"] = pick(flags1&0x08 != 0, "strong", "basic")
	s.Settings["noise-cancel-power"] = pick(flags1&0x20 != 0, "on", "off")
	s.Settings["camera-power"] = pick(flags1&0x80 != 0, "on", "off")
	s.Settings["auto-off-15m"] = pick(flags2&0x01 != 0, "on", "off")
	s.Settings["stereo"] = pick(flags2&0x04 != 0, "stereo", "mono")
	s.Settings["plug-free"] = pick(flags3&0x02 != 0, "on", "off")
	s.Settings["clip-limiter"] = pick(flags3&0x10 != 0, "on", "off")
	s.Settings["safety-track"] = pick(flags3&0x40 != 0, "on", "off")

	// Bit 0x01 of offset 44 is TX1 and 0x02 is TX2, by physical unit rather
	// than slot position. It is the authoritative connection signal: it is
	// set up to one push before that transmitter's slot appears.
	connected := f[v2ConnectedOff]
	for i := range s.TX {
		if connected&(1<<i) == 0 {
			s.TX[i] = TXInfo{Unit: i + 1, Level: -1}
			continue
		}
		if !s.TX[i].Present {
			// First push after connecting: identity and level are still to
			// come, so clear anything left from a previous session.
			s.TX[i] = TXInfo{Unit: i + 1, Level: -1}
		}
		s.TX[i].Present = true
		s.TX[i].Unit = i + 1
	}

	// Settings that are purely per transmitter mirror across them, so the
	// first slot stands for all.
	if slots > 0 {
		ledFlags := f[v2HeaderLen+6]
		s.Settings["mic-leds"] = pick(ledFlags&0x02 != 0, "off", "on")
		s.Settings["tx-auto-off-15m"] = pick(ledFlags&0x10 != 0, "on", "off")
		s.Settings["noise-cancel-button"] = pick(ledFlags&0x80 != 0, "on", "off")
		s.Settings["low-cut"] = pick(f[v2HeaderLen+9]&0x20 != 0, "on", "off")
	}

	// Voice tone, charging and battery are read from each slot by the
	// transmitter's own unit number, since they are not mirrored.
	for slot := 0; slot < slots; slot++ {
		off := v2HeaderLen + v2SlotLen*slot
		unit := int(f[off+1])
		if unit < 1 || unit > 2 {
			continue
		}
		tx := &s.TX[unit-1]
		tx.Present = true
		tx.Unit = unit

		switch tone := f[off+9]; {
		case tone&0x80 != 0:
			tx.VoiceTone = "bright"
		case tone&0x40 != 0:
			tx.VoiceTone = "rich"
		default:
			tx.VoiceTone = "standard"
		}

		battery := f[off+7]
		tx.Charging = battery&0x02 != 0
		tx.BatteryGauge = int(battery>>2) & 0x07
	}
	return true
}

// decodeV2Records decodes the identity and audio-level pushes, which share a
// record layout. Identity carries firmware, serial and product name, so this
// fills fields the status push does not.
func decodeV2Records(s *State, f []byte) bool {
	if len(f) < v2RecordsStart+2 {
		return false
	}
	end := len(f) - 2 // the trailing CRC
	matched := false
	for i := v2RecordsStart; i+v2RecordHdrLen <= end; {
		tag, index := f[i], int(f[i+1])
		length := int(f[i+5])
		dataStart := i + v2RecordHdrLen
		dataEnd := dataStart + length
		if dataEnd > end || length == 0 {
			break
		}
		data := f[dataStart:dataEnd]

		switch tag {
		case tagIdentity:
			if len(data) > 4 {
				// The firmware bytes arrive most significant last.
				fw := firmwareString([]byte{data[3], data[2], data[1], data[0]})
				serial := asciiField(data[4:])
				if index == 0 {
					s.RX.Firmware, s.RX.Serial = fw, serial
					matched = true
				} else if index >= 1 && index <= 2 {
					tx := &s.TX[index-1]
					tx.Firmware, tx.Serial = fw, serial
					matched = true
				}
			}
		case tagName:
			name := asciiField(data)
			if index == 0 {
				s.RX.Name = name
				matched = true
			} else if index >= 1 && index <= 2 {
				s.TX[index-1].Name = name
				matched = true
			}
		case tagLevel:
			// The level push numbers its units 1 and 2, like the identity
			// push, so index 1 is the first transmitter.
			if len(data) == 1 && index >= 1 && index <= 2 {
				s.TX[index-1].Level = int(data[0])
				matched = true
			}
		}
		i = dataEnd
	}
	return matched
}

// asciiField trims a fixed-width ASCII field at its first NUL.
func asciiField(b []byte) string {
	if i := indexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// firmwareString formats the four decimal version components.
func firmwareString(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	return uint8String(b[0]) + "." + uint8String(b[1]) + "." + uint8String(b[2]) + "." + uint8String(b[3])
}

func uint8String(v byte) string {
	if v > 99 {
		return "??"
	}
	s := []byte{byte('0' + v/10), byte('0' + v%10)}
	return string(s)
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
