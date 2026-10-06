package console

import (
	"slices"
	"testing"

	"dji-mic-rx/internal/duml"
)

// The settings page belongs to one protocol: a v1 receiver must not be shown
// v2-only settings (with or without an explanation), and v2 must not carry
// v1's read-only special cases.
func TestSettingsSplitByProtocol(t *testing.T) {
	wantV1 := []string{
		"noise-cancel", "low-cut", "stereo", "safety-track", "clip-limiter",
		"auto-off-15m", "camera-power", "mic-leds", "plug-free",
	}
	wantV2 := []string{
		"noise-cancel", "noise-cancel-power", "noise-cancel-button", "low-cut",
		"stereo", "safety-track", "clip-limiter", "auto-off-15m",
		"tx-auto-off-15m", "camera-power", "mic-leds", "plug-free",
	}

	if got := writableIDs(duml.V1, "DJI Mic Mini 2"); !slices.Equal(got, wantV1) {
		t.Errorf("v1 settings = %v, want %v", got, wantV1)
	}
	if got := writableIDs(duml.V2, "DJI Mic Mini 2"); !slices.Equal(got, wantV2) {
		t.Errorf("v2 settings = %v, want %v", got, wantV2)
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

// writableIDs lists the settings the settings page would draw, in the page's
// order: group by group, registry order inside.
func writableIDs(dialect duml.Dialect, product string) []string {
	var ids []string
	for _, group := range []duml.Group{duml.GroupAudio, duml.GroupPower, duml.GroupDevice} {
		for _, setting := range settingsInGroup(dialect, product, group) {
			ids = append(ids, setting.ID)
		}
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
