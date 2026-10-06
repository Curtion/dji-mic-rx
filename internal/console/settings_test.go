package console

import (
	"slices"
	"testing"

	"dji-mic-rx/internal/duml"
)

// The settings page belongs to one protocol and one end of the link: a v1
// receiver must not be shown v2-only settings (with or without an
// explanation), v2 must not carry v1's read-only special cases, and the
// transmitters' settings and the receiver's go to separate cards.
func TestSettingsSplitByProtocol(t *testing.T) {
	wantV1TX := []string{"noise-cancel", "low-cut", "mic-leds"}
	wantV1RX := []string{"stereo", "safety-track", "clip-limiter", "auto-off-15m", "camera-power", "plug-free"}
	wantV2TX := []string{"noise-cancel", "noise-cancel-power", "noise-cancel-button", "low-cut", "tx-auto-off-15m", "mic-leds"}
	wantV2RX := wantV1RX

	if got := writableIDs(duml.V1, "DJI Mic Mini 2", true); !slices.Equal(got, wantV1TX) {
		t.Errorf("v1 transmitter settings = %v, want %v", got, wantV1TX)
	}
	if got := writableIDs(duml.V1, "DJI Mic Mini 2", false); !slices.Equal(got, wantV1RX) {
		t.Errorf("v1 receiver settings = %v, want %v", got, wantV1RX)
	}
	if got := writableIDs(duml.V2, "DJI Mic Mini 2", true); !slices.Equal(got, wantV2TX) {
		t.Errorf("v2 transmitter settings = %v, want %v", got, wantV2TX)
	}
	if got := writableIDs(duml.V2, "DJI Mic Mini 2", false); !slices.Equal(got, wantV2RX) {
		t.Errorf("v2 receiver settings = %v, want %v", got, wantV2RX)
	}

	// The states v1 reports without a command go to the read-only card
	// instead of a list, and only v1 has any.
	if got := readOnlyIDs(duml.V1); !slices.Equal(got, []string{"noise-cancel-power"}) {
		t.Errorf("v1 read-only = %v, want [noise-cancel-power]", got)
	}
	if got := readOnlyIDs(duml.V2); len(got) != 0 {
		t.Errorf("v2 read-only = %v, want none", got)
	}
}

// writableIDs lists the settings one card of the settings page would draw,
// in the page's order: the transmitters' list or the receiver's, in registry
// order inside.
func writableIDs(dialect duml.Dialect, product string, transmitters bool) []string {
	var ids []string
	for _, setting := range settingsFor(dialect, product, transmitters) {
		ids = append(ids, setting.ID)
	}
	return ids
}

func readOnlyIDs(dialect duml.Dialect) []string {
	var ids []string
	for _, setting := range readOnlySettings(dialect) {
		ids = append(ids, setting.ID)
	}
	return ids
}
