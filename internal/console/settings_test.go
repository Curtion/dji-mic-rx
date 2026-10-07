package console

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

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
	if got := writableIDs(duml.V2, "DJI Mic Mini", true); !slices.Equal(got, wantV2TX) {
		t.Errorf("v2 transmitter settings = %v, want %v", got, wantV2TX)
	}
	if got := writableIDs(duml.V2, "DJI Mic Mini", false); !slices.Equal(got, wantV2RX) {
		t.Errorf("v2 receiver settings = %v, want %v", got, wantV2RX)
	}

	if got := readOnlyIDs(duml.V1, "DJI Mic Mini"); !slices.Equal(got, []string{"noise-cancel-power"}) {
		t.Errorf("v1 read-only = %v, want [noise-cancel-power]", got)
	}
	if got := readOnlyIDs(duml.V2, "DJI Mic Mini"); len(got) != 0 {
		t.Errorf("v2 read-only = %v, want none", got)
	}
	if got := writableIDs(duml.V2, "DJI Mic Mini 2", true); !slices.Equal(got, wantV2TX[3:]) {
		t.Errorf("mobile transmitter settings = %v, want %v", got, wantV2TX[3:])
	}
	if got := writableIDs(duml.V2, "DJI Mic Mini 2", false); !slices.Equal(got, []string{"stereo", "safety-track", "clip-limiter", "plug-free"}) {
		t.Errorf("mobile receiver settings = %v", got)
	}
	if got := readOnlyIDs(duml.V2, "DJI Mic Mini 2"); !slices.Equal(got, []string{"noise-cancel", "noise-cancel-power"}) {
		t.Errorf("mobile read-only = %v, want noise strength and power", got)
	}
}

func writableIDs(dialect duml.Dialect, product string, transmitters bool) []string {
	var ids []string
	for _, setting := range settingsFor(dialect, product, transmitters) {
		if setting.Available(dialect, product) {
			ids = append(ids, setting.ID)
		}
	}
	return ids
}

func readOnlyIDs(dialect duml.Dialect, product string) []string {
	var ids []string
	for _, setting := range settingsFor(dialect, product, true) {
		if setting.ReadOnlyReason(dialect, product) != "" {
			ids = append(ids, setting.ID)
		}
	}
	return ids
}

func TestReadOnlyNoiseRowsFollowDeviceState(t *testing.T) {
	source := NewDemo(DemoMobile)
	app := New(source)
	tester := ui.NewTester(func(c *ui.Context) {
		_, p := theme(c.Theme())
		snap := source.Snapshot()
		app.settingsSection(c, p, snap, "发射器设置", "对两支发射器一起生效", settingsFor(snap.State.Dialect, snap.State.RX.Name, true))
	}, 1000, 900)
	tester.Frame()
	for _, label := range []string{"发射器设置", "降噪强度", "降噪开关", "强", "开启"} {
		if !tester.HasText(label) {
			t.Errorf("transmitter card missing %s", label)
		}
	}
	source.mu.Lock()
	source.snap.State.Settings["noise-cancel"] = "basic"
	source.snap.State.Settings["noise-cancel-power"] = "off"
	source.mu.Unlock()
	tester.Frame()
	if !tester.HasText("普通") || !tester.HasText("关闭") {
		t.Fatal("read-only rows did not reflect device updates")
	}
}

type delayedSettings struct {
	*Demo
	requests chan settingKey
	result   chan error
	finished chan struct{}
}

func (d *delayedSettings) Snapshot() session.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	snap := d.snap
	snap.State = snap.State.Clone()
	snap.Log = append([]session.LogLine(nil), snap.Log...)
	return snap
}

func (d *delayedSettings) Send(id, value string) error {
	return d.SendToTransmitter(id, value, 0)
}

func (d *delayedSettings) SendToTransmitter(id, value string, unit int) error {
	d.requests <- settingKey{id: id, unit: unit}
	if err := <-d.result; err != nil {
		return err
	}
	d.mu.Lock()
	if unit == 0 {
		d.snap.State.Settings[id] = value
	} else {
		d.snap.State.TX[unit-1].VoiceTone = value
	}
	d.mu.Unlock()
	return nil
}

func (d *delayedSettings) Log(format string, args ...any) {
	d.Demo.Log(format, args...)
	if strings.HasPrefix(format, "已设置") || strings.HasPrefix(format, "设置未确认") {
		d.finished <- struct{}{}
	}
}

func TestSettingLoadingUntilDeviceReply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		id    string
		value string
		unit  int
		err   error
	}{
		{"toggle-success", "low-cut", "on", 0, nil},
		{"toggle-timeout", "low-cut", "on", 0, errors.New("设备未确认")},
		{"segmented-success", "noise-cancel", "strong", 0, nil},
		{"tone-success", "voice-tone", "bright", 1, nil},
		{"tone-failure", "voice-tone", "bright", 2, errors.New("设备未确认")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &delayedSettings{
				Demo: NewDemo(DemoLive), requests: make(chan settingKey, 2),
				result: make(chan error, 2), finished: make(chan struct{}, 2),
			}
			source.snap.State.Settings["low-cut"] = "off"
			source.snap.State.Settings["noise-cancel"] = "basic"
			app := New(source)
			setting, _ := duml.SettingByID(tc.id)
			readValue := func() string {
				snap := source.Snapshot()
				if tc.unit > 0 {
					return snap.State.TX[tc.unit-1].VoiceTone
				}
				return snap.State.Setting(tc.id)
			}
			old := readValue()
			tester := ui.NewTester(func(c *ui.Context) {
				theme, p := theme(c.Theme())
				c.SetTheme(theme)
				snap := source.Snapshot()
				if tc.unit > 0 {
					app.voiceToneRow(c, p, snap.State.TX[tc.unit-1], tc.unit-1, setting)
				} else {
					app.settingRow(c, p, snap, setting)
				}
			}, 820, 100)
			if tc.name == "toggle-success" {
				tester.SetDark(true)
			}
			if tc.unit > 0 {
				tester.SetSize(320, 100)
			}
			clickSetting := func() {
				if setting.Kind == duml.KindToggle {
					tester.Key(0, ui.KeySpace)
				} else {
					option, _ := setting.Option(tc.value)
					if err := tester.Click(option.Label); err != nil {
						t.Fatal(err)
					}
				}
			}
			if setting.Kind == duml.KindToggle {
				tester.Key(0, ui.KeyTab)
			}
			clickSetting()
			select {
			case key := <-source.requests:
				if key != (settingKey{id: tc.id, unit: tc.unit}) {
					t.Fatalf("wrong target: %+v", key)
				}
			case <-time.After(time.Second):
				t.Fatal("setting not sent")
			}
			tester.Frame()
			if !tester.HasText("等待设备确认") || readValue() != old {
				t.Fatal("waiting state missing or value changed before confirmation")
			}
			app.sendSetting(tc.id, tc.value, tc.unit)
			clickSetting()
			if len(source.requests) != 0 {
				t.Fatal("duplicate command sent while pending")
			}
			if tc.unit > 0 && app.settingPending(tc.id, 3-tc.unit) {
				t.Fatal("other transmitter blocked by this request")
			}
			source.result <- tc.err
			select {
			case <-source.finished:
			case <-time.After(time.Second):
				t.Fatal("loading did not finish")
			}
			tester.Frame()
			if tester.HasText("等待设备确认") || app.settingPending(tc.id, tc.unit) {
				t.Fatal("loading still visible after reply")
			}
			want := tc.value
			if tc.err != nil {
				want = old
			}
			if got := readValue(); got != want {
				t.Fatalf("value = %q, want %q", got, want)
			}
			app.mu.Lock()
			failed := app.noticeError
			app.mu.Unlock()
			if failed != (tc.err != nil) {
				t.Fatal("wrong completion notification")
			}
		})
	}
}
