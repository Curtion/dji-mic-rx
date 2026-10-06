package duml

import (
	"encoding/hex"
	"testing"
)

// The samples below are built field by field at the offsets the protocol
// reference documents, rather than typed out as one long hex blob, so a
// mistake in one field cannot silently shift the rest.

// hexBytes decodes a byte string written with spaces, as the protocol
// reference writes frames.
func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	clean := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ' ', '\n', '\t', '\r':
		default:
			clean = append(clean, c)
		}
	}
	b, err := hex.DecodeString(string(clean))
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

// seal appends the CRC bytes for a dialect.
func seal(f []byte, d Dialect) []byte {
	body := f[:len(f)-2]
	crc := crcBytes(body, d.crcInit(), d.crcResidue())
	f[len(f)-2], f[len(f)-1] = crc[0], crc[1]
	return f
}

// pushHeader fills the 12-byte preamble every status frame shares: start
// marker, length, protocol version, header CRC-8, and the offset 8..11
// status marker whose last byte is the version marker.
func pushHeader(f []byte, length byte, marker byte) {
	f[0], f[1], f[2], f[3] = frameStart, length, protocolVersion, headerCRC8(length)
	f[4], f[5] = 0x5a, 0x02
	f[9], f[10], f[11] = 0x5b, 0x03, marker
}

// sample2TXV1 is the reference's two-transmitter v1 heartbeat, with
// placeholder serials.
func sample2TXV1(t *testing.T) []byte {
	t.Helper()
	body := hexBytes(t, `
		55 54 04 f6 5a 02 0e 79 00 5b 03 00 47 00
		b0 25 38 00 01 01 00 38 0e 42 42 42 42 42 42 42 42 42 42 42 42 42 42
		b0 25 38 00 01 01 00 38 0e 43 43 43 43 43 43 43 43 43 43 43 43 43 43
		32 20 01 01 00 38 0e 41 41 41 41 41 41 41 41 41 41 41 41 41 41 00`)
	return appendCRC(body, V1.crcInit(), V1.crcResidue())
}

// sampleV2Status builds a v2 status push for the given connected bitmask and
// transmitter units. Each slot is the same shape the reference documents: the
// record header 02 <unit> 00 00 00 1a, then flags b8 25 04 01 00 78 and a
// tail of zeroes.
func sampleV2Status(t *testing.T, connected byte, units ...int) []byte {
	t.Helper()
	f := make([]byte, 54+v2SlotLen*len(units))
	pushHeader(f, byte(len(f)), 0x03)
	f[12] = byte(0x26 + 0x20*len(units)) // capability byte
	f[20], f[21], f[22] = 0x28, 0x31, 0x0c
	f[v2ConnectedOff], f[48] = connected, 0x1c
	for i, unit := range units {
		off := v2HeaderLen + v2SlotLen*i
		copy(f[off:], []byte{0x02, byte(unit), 0x00, 0x00, 0x00, 0x1a, 0xb8, 0x25, 0x04, 0x01, 0x00, 0x78})
	}
	return seal(f, V2)
}

// sampleIdentityV2 is the reference's receiver-only identity push.
func sampleIdentityV2(t *testing.T) []byte {
	t.Helper()
	f := make([]byte, 70)
	pushHeader(f, 0x46, 0x03)
	f[12], f[13] = 0x36, 0x00 // length of the record block that follows

	f[14], f[15], f[19] = tagIdentity, 0x00, 0x12
	copy(f[20:24], []byte{0x00, 0x11, 0x03, 0x02}) // reads 02.03.17.00
	copy(f[24:38], []byte("DDDDDDDDDDDDDD"))

	f[38], f[39], f[43] = 0x04, 0x00, 0x06
	copy(f[44:50], []byte("123456"))

	f[50], f[51], f[55] = tagName, 0x00, 0x0c
	copy(f[56:68], []byte("DJI Mic Mini"))
	return seal(f, V2)
}

// sampleLevelV2 builds an audio-level push for both transmitters.
func sampleLevelV2(t *testing.T, first, second byte) []byte {
	t.Helper()
	f := make([]byte, 30)
	pushHeader(f, 0x1e, 0x03)
	f[12], f[13] = 0x0e, 0x00

	f[14], f[15], f[19], f[20] = tagLevel, 0x01, 0x01, first
	f[21], f[22], f[26], f[27] = tagLevel, 0x02, 0x01, second
	return seal(f, V2)
}

func TestDecodeV1Heartbeat(t *testing.T) {
	st, ok := Decode(NewState(), sample2TXV1(t))
	if !ok {
		t.Fatal("heartbeat did not decode")
	}
	if !st.DialectKnown || st.Dialect != V1 {
		t.Errorf("dialect = %v (known %v)", st.Dialect, st.DialectKnown)
	}
	if st.TXCount() != 2 {
		t.Fatalf("tx count = %d", st.TXCount())
	}
	if st.TX[0].Serial != "BBBBBBBBBBBBBB" || st.TX[1].Serial != "CCCCCCCCCCCCCC" {
		t.Errorf("serials = %q %q", st.TX[0].Serial, st.TX[1].Serial)
	}
	if st.TX[0].Firmware != "01.01.00.56" {
		t.Errorf("firmware = %q", st.TX[0].Firmware)
	}
	if st.TX[0].Level != 0x38 {
		t.Errorf("level = %d", st.TX[0].Level)
	}
	if st.RX.Serial != "AAAAAAAAAAAAAA" {
		t.Errorf("rx serial = %q", st.RX.Serial)
	}
	if st.RX.Firmware != "01.01.00.56" {
		t.Errorf("rx firmware = %q", st.RX.Firmware)
	}
	if st.RX.HasGain {
		t.Error("v1 reports no gain dial")
	}
	for id, want := range map[string]string{
		"noise-cancel":       "strong",
		"noise-cancel-power": "on",
		"low-cut":            "off",
		"auto-off-15m":       "on",
		"stereo":             "mono",
		"clip-limiter":       "on",
		"safety-track":       "off",
		"plug-free":          "off",
		"camera-power":       "off",
		"mic-leds":           "on",
	} {
		if got := st.Setting(id); got != want {
			t.Errorf("setting %s = %q, want %q", id, got, want)
		}
	}
	if v := st.Setting("voice-tone"); v != "" {
		t.Errorf("voice tone must not be reported on v1, got %q", v)
	}
}

func TestDecodeV2Status(t *testing.T) {
	st, ok := Decode(NewState(), sampleV2Status(t, 0x03, 1, 2))
	if !ok {
		t.Fatal("status push did not decode")
	}
	if st.Dialect != V2 {
		t.Errorf("dialect = %v", st.Dialect)
	}
	if st.TXCount() != 2 {
		t.Fatalf("tx count = %d", st.TXCount())
	}
	for id, want := range map[string]string{
		"noise-cancel":        "strong",
		"noise-cancel-power":  "on",
		"camera-power":        "off",
		"auto-off-15m":        "on",
		"stereo":              "mono",
		"plug-free":           "off",
		"clip-limiter":        "on",
		"safety-track":        "off",
		"mic-leds":            "on",
		"tx-auto-off-15m":     "on",
		"noise-cancel-button": "on",
		"low-cut":             "off",
	} {
		if got := st.Setting(id); got != want {
			t.Errorf("setting %s = %q, want %q", id, got, want)
		}
	}
	if !st.RX.HasGain || st.RX.GainDial != 12 {
		t.Errorf("gain dial = %d (known %v)", st.RX.GainDial, st.RX.HasGain)
	}
	// Offset 21 also carries the receiver's own battery gauge in bits 5..7,
	// which only the Mic Mini 2 reports. This sample has gauge 1, full.
	if st.RX.BatteryGauge != 1 {
		t.Errorf("receiver battery gauge = %d, want 1", st.RX.BatteryGauge)
	}
	if st.TX[0].VoiceTone != "standard" || st.TX[1].VoiceTone != "standard" {
		t.Errorf("voice tones = %q %q", st.TX[0].VoiceTone, st.TX[1].VoiceTone)
	}
	if st.TX[0].Level != -1 {
		t.Errorf("level should still be unknown, got %d", st.TX[0].Level)
	}
}

func TestDecodeV2StatusReadsBatteryAndTonePerSlot(t *testing.T) {
	f := sampleV2Status(t, 0x03, 1, 2)
	// TX1: 0x04 set in the gauge bits means one step from full (100%),
	// tone byte 0x41 means Rich. TX2: gauge 0x1c (empty), tone 0x81 Bright,
	// and bit 0x02 set means docked and charging.
	f[52+7] = 0x24 // charging bit clear, gauge bits 100
	f[52+9] = 0x41
	f[84+7] = 0x1f // charging bit set, gauge bits 111
	f[84+9] = 0x81
	f = seal(f, V2)

	st, ok := Decode(NewState(), f)
	if !ok {
		t.Fatal("status push did not decode")
	}
	if st.TX[0].BatteryGauge != 1 || st.TX[0].Charging {
		t.Errorf("tx1 battery = %d charging %v", st.TX[0].BatteryGauge, st.TX[0].Charging)
	}
	if st.TX[1].BatteryGauge != 7 || !st.TX[1].Charging {
		t.Errorf("tx2 battery = %d charging %v", st.TX[1].BatteryGauge, st.TX[1].Charging)
	}
	if st.TX[0].VoiceTone != "rich" || st.TX[1].VoiceTone != "bright" {
		t.Errorf("voice tones = %q %q", st.TX[0].VoiceTone, st.TX[1].VoiceTone)
	}
	if pct, ok := st.TX[0].BatteryPercent(); !ok || pct != 100 {
		t.Errorf("tx1 percent = %d %v", pct, ok)
	}
	if pct, ok := st.TX[1].BatteryPercent(); !ok || pct != 0 {
		t.Errorf("tx2 percent = %d %v", pct, ok)
	}
}

func TestDecodeV2StatusReadsReceiverBattery(t *testing.T) {
	f := sampleV2Status(t, 0x01, 1)
	f[21] = 0x31 | 0x01<<5 | 0x10 // auto-off + stereo set, gauge 1, charging
	st, ok := Decode(NewState(), seal(f, V2))
	if !ok {
		t.Fatal("status push did not decode")
	}
	if st.RX.BatteryGauge != 1 || !st.RX.Charging {
		t.Errorf("receiver battery = %d charging %v", st.RX.BatteryGauge, st.RX.Charging)
	}
	if pct, ok := st.RX.BatteryPercent(); !ok || pct != 100 {
		t.Errorf("receiver percent = %d %v", pct, ok)
	}
}

func TestDecodeV2IdentityPush(t *testing.T) {
	st, ok := Decode(NewState(), sampleIdentityV2(t))
	if !ok {
		t.Fatal("identity push did not decode")
	}
	if st.RX.Serial != "DDDDDDDDDDDDDD" {
		t.Errorf("serial = %q", st.RX.Serial)
	}
	if st.RX.Firmware != "02.03.17.00" {
		t.Errorf("firmware = %q", st.RX.Firmware)
	}
	if st.RX.Name != "DJI Mic Mini" {
		t.Errorf("name = %q", st.RX.Name)
	}
}

func TestDecodeV2LevelPush(t *testing.T) {
	// Level arrives on its own push, so the state it lands in already knows
	// both transmitters are connected.
	prev := NewState()
	prev.Dialect, prev.DialectKnown = V2, true
	prev.TX[0] = TXInfo{Present: true, Unit: 1, Level: -1}
	prev.TX[1] = TXInfo{Present: true, Unit: 2, Level: -1}

	st, ok := Decode(prev, sampleLevelV2(t, 0x2a, 0x63))
	if !ok {
		t.Fatal("level push did not decode")
	}
	if st.TX[0].Level != 0x2a || st.TX[1].Level != 0x63 {
		t.Errorf("levels = %d %d", st.TX[0].Level, st.TX[1].Level)
	}
	if !st.TX[0].Present || st.TX[1].Unit != 2 {
		t.Error("transmitter state was lost")
	}
}

func TestDecodeKeepsIdentityAcrossStatusPush(t *testing.T) {
	prev := NewState()
	prev.Dialect, prev.DialectKnown = V2, true
	prev.TX[0] = TXInfo{Present: true, Unit: 1, Level: 40, Serial: "S1", Firmware: "01.02.03.04", Name: "DJI Mic Mini 2"}
	prev.RX.Serial, prev.RX.Firmware = "RX1", "02.00.00.01"

	st, ok := Decode(prev, sampleV2Status(t, 0x03, 1, 2))
	if !ok {
		t.Fatal("status push did not decode")
	}
	if st.RX.Serial != "RX1" || st.RX.Firmware != "02.00.00.01" {
		t.Errorf("receiver identity lost: %q %q", st.RX.Serial, st.RX.Firmware)
	}
	if st.TX[0].Serial != "S1" || st.TX[0].Firmware != "01.02.03.04" || st.TX[0].Name != "DJI Mic Mini 2" {
		t.Errorf("transmitter identity lost: %+v", st.TX[0])
	}
	// A status push does not repeat the audio level; it must be carried.
	if st.TX[0].Level != 40 {
		t.Errorf("level lost: %d", st.TX[0].Level)
	}
}

func TestDecodeDisconnectClearsTransmitter(t *testing.T) {
	prev := NewState()
	prev.Dialect, prev.DialectKnown = V2, true
	prev.TX[1] = TXInfo{Present: true, Unit: 2, Level: 12, Serial: "S2", BatteryGauge: 3}
	prev.RX.Name = "DJI Mic Mini 2"

	// Only TX1 is connected this time.
	st, ok := Decode(prev, sampleV2Status(t, 0x01, 1))
	if !ok {
		t.Fatal("status push did not decode")
	}
	if st.TXCount() != 1 || !st.TX[0].Present || st.TX[1].Present {
		t.Fatalf("connection state wrong: %+v", st.TX)
	}
	if st.TX[1].Serial != "" || st.TX[1].Level != -1 {
		t.Errorf("disconnected transmitter kept stale data: %+v", st.TX[1])
	}
}

func TestDecodeIgnoresNonStatusFrames(t *testing.T) {
	for _, frame := range [][]byte{
		BuildCommand(V1, 1, TargetRX, 0x031d, 1),
		BuildCommand(V2, 1, TargetRX, 0x0037, 1),
		{0x55, 0x0e, 0x04, 0x66, 0x5a, 0x02, 0x01, 0x00, 0x80, 0x5b, 0x01, 0x00, 0xc0, 0xe8},
		{0x55},
	} {
		prev := NewState()
		st, ok := Decode(prev, frame)
		if ok {
			t.Errorf("frame % x decoded but should not", frame)
		}
		if len(st.Settings) != 0 || st.Packets != 0 {
			t.Errorf("state was modified by a non-status frame")
		}
	}
}

func TestBatteryPercent(t *testing.T) {
	for gauge, want := range map[int]int{1: 100, 2: 83, 3: 66, 4: 50, 5: 33, 6: 16, 7: 0} {
		got, ok := BatteryPercent(gauge)
		if !ok || got != want {
			t.Errorf("gauge %d = %d %v, want %d", gauge, got, ok, want)
		}
	}
	if _, ok := BatteryPercent(0); ok {
		t.Error("gauge 0 means unknown")
	}
	if _, ok := BatteryPercent(8); ok {
		t.Error("gauge 8 is out of range")
	}
}

func TestCloneIsIndependent(t *testing.T) {
	a := NewState()
	a.Settings["low-cut"] = "on"
	b := a.Clone()
	b.Settings["low-cut"] = "off"
	if a.Settings["low-cut"] != "on" {
		t.Error("clone shares its settings map")
	}
}

func TestSettingRegistryIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Settings {
		if seen[s.ID] {
			t.Errorf("duplicate setting id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Kind == KindToggle && (s.Off == "" || s.On == "") {
			t.Errorf("%s: toggle without off/on values", s.ID)
		}
		for _, o := range s.Options {
			if o.Value == "" || o.Label == "" {
				t.Errorf("%s: option with missing value or label: %+v", s.ID, o)
			}
		}
	}
	for _, s := range Settings {
		if s.V1 == 0 && s.V2 == 0 {
			t.Errorf("%s: no command id for either protocol version", s.ID)
		}
	}
	if _, ok := SettingByID("voice-tone"); !ok {
		t.Error("voice-tone missing")
	}
	// A v1 device cannot write settings that only exist on v2.
	ncPower, _ := SettingByID("noise-cancel-power")
	if _, _, ok := ncPower.Command(V1); ok {
		t.Error("v1 should have no command for noise-cancel-power")
	}
	if _, _, ok := ncPower.Command(V2); !ok {
		t.Error("v2 should have a command for noise-cancel-power")
	}
	// The LED setting's wire values are inverted.
	leds, _ := SettingByID("mic-leds")
	if wire, _ := leds.WireOf("on"); wire != 0x00 {
		t.Errorf("mic leds on wire = %#02x, want 0", wire)
	}
	if wire, _ := leds.WireOf("off"); wire != 0x02 {
		t.Errorf("mic leds off wire = %#02x, want 2", wire)
	}
	// Stereo uses 0x02 for on, not 0x01.
	stereo, _ := SettingByID("stereo")
	if wire, _ := stereo.WireOf("stereo"); wire != 0x02 {
		t.Errorf("stereo wire = %#02x, want 2", wire)
	}
	// Voice tone is per transmitter and only on the Mic Mini 2.
	tone, _ := SettingByID("voice-tone")
	if !tone.PerTransmitter {
		t.Error("voice tone must be per transmitter")
	}
	if tone.Available(V2, "DJI Mic Mini") {
		t.Error("voice tone must not be available on the original Mic Mini")
	}
	if !tone.Available(V2, "DJI Mic Mini 2") {
		t.Error("voice tone must be available on the Mic Mini 2")
	}
	if _, _, ok := tone.Command(V1); ok {
		t.Error("voice tone has no v1 command")
	}
}
