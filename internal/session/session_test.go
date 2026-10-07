package session

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"dji-mic-rx/internal/duml"
)

func TestSnapshotSettingsAreIndependent(t *testing.T) {
	s := New(Options{})
	s.snapshot.State.Settings["low-cut"] = "off"

	snap := s.Snapshot()
	s.mu.Lock()
	s.snapshot.State.Settings["low-cut"] = "on"
	s.mu.Unlock()
	if got := snap.State.Setting("low-cut"); got != "off" {
		t.Fatalf("previous snapshot changed: got %q, want off", got)
	}

	snap.State.Settings["low-cut"] = "snapshot-only"
	if got := s.Snapshot().State.Setting("low-cut"); got != "on" {
		t.Fatalf("snapshot mutation changed session: got %q, want on", got)
	}
}

func TestSnapshotSettingsConcurrentUpdate(t *testing.T) {
	s := New(Options{})
	s.snapshot.State.Settings["low-cut"] = "off"
	snap := s.Snapshot()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			s.mu.Lock()
			s.snapshot.State.Settings["low-cut"] = "on"
			s.mu.Unlock()
		}
	}()
	defer func() { <-done }()

	for range 1000 {
		if got := snap.State.Setting("low-cut"); got != "off" {
			t.Fatalf("previous snapshot changed: got %q, want off", got)
		}
		_ = s.Snapshot().State.Setting("low-cut")
	}
}

type testConnection struct {
	written chan []byte
	err     error
}

func (c *testConnection) Read([]byte) (int, error) { return 0, io.EOF }
func (c *testConnection) Close() error             { return nil }
func (c *testConnection) Write(frame []byte) (int, error) {
	c.written <- frame
	if c.err != nil {
		return 0, c.err
	}
	return len(frame), nil
}

func testSession(timeout time.Duration) (*Session, *testConnection) {
	s := New(Options{AckTimeout: timeout})
	c := &testConnection{written: make(chan []byte, 1)}
	s.conn = c
	s.snapshot.Connected = true
	s.snapshot.State.Dialect = duml.V2
	s.snapshot.State.DialectKnown = true
	s.snapshot.State.Settings["low-cut"] = "off"
	s.snapshot.State.TX[0].Present = true
	s.snapshot.State.TX[0].Name = "DJI Mic Mini 2"
	s.snapshot.State.TX[0].VoiceTone = "standard"
	return s, c
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("operation did not finish")
		var zero T
		return zero
	}
}

func settingPush(flags byte) []byte {
	f := make([]byte, 86)
	f[9], f[10], f[11] = 0x5b, 0x03, 0x03
	f[44], f[53], f[61] = 1, 1, flags
	return f
}

func TestSendWaitsForReportedValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit int
		ack  bool
	}{
		{"shared-with-ack", 0, true},
		{"shared-without-ack", 0, false},
		{"transmitter", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, conn := testSession(time.Second)
			result := make(chan error, 1)
			go func() {
				if tc.unit == 0 {
					result <- s.Send("low-cut", "on")
				} else {
					result <- s.SendToTransmitter("voice-tone", "bright", tc.unit)
				}
			}()
			wire := receive(t, conn.written)
			before := s.Snapshot()
			if before.State.Setting("low-cut") != "off" || before.State.TX[0].VoiceTone != "standard" {
				t.Fatal("sending optimistically changed the device value")
			}
			reported := 0
			if tc.ack {
				ack := append([]byte(nil), wire...)
				ack[8] = 0x80
				s.handleFrame(ack, &reported)
			}
			s.handleFrame(settingPush(0), &reported)
			select {
			case err := <-result:
				t.Fatalf("completed before value confirmation: %v", err)
			default:
			}
			snap := s.Snapshot()
			if snap.State.Setting("low-cut") != "off" || snap.State.TX[0].VoiceTone != "standard" {
				t.Fatal("setting changed before the device reported it")
			}
			s.handleFrame(settingPush(0xa0), &reported)
			if err := receive(t, result); err != nil {
				t.Fatal(err)
			}
			if len(s.pending) != 0 {
				t.Fatal("confirmed command still pending")
			}
			snap = s.Snapshot()
			if snap.State.Setting("low-cut") != "on" || snap.State.TX[0].VoiceTone != "bright" {
				t.Fatal("confirmed device values missing")
			}
			if _, ok := snap.State.Settings["voice-tone"]; ok || snap.State.TX[1].VoiceTone != "" {
				t.Fatal("per-transmitter value leaked into shared or other transmitter state")
			}
		})
	}
}

func TestSendFailureKeepsDeviceValue(t *testing.T) {
	for _, mode := range []string{"timeout", "ack-only", "write-error", "disconnect", "stop"} {
		t.Run(mode, func(t *testing.T) {
			s, conn := testSession(100 * time.Millisecond)
			if mode == "write-error" {
				conn.err = errors.New("write failed")
			}
			result := make(chan error, 1)
			go func() { result <- s.Send("low-cut", "on") }()
			wire := receive(t, conn.written)
			switch mode {
			case "ack-only":
				ack := append([]byte(nil), wire...)
				ack[8] = 0x80
				reported := 0
				s.handleFrame(ack, &reported)
			case "disconnect":
				s.close("接收器已断开")
			case "stop":
				s.Stop()
			}
			err := receive(t, result)
			if err == nil {
				t.Fatal("failed command reported success")
			}
			if mode == "ack-only" && !strings.Contains(err.Error(), "设备已应答") {
				t.Fatalf("ACK-only timeout has wrong message: %v", err)
			}
			if got := s.Snapshot().State.Setting("low-cut"); got != "off" {
				t.Fatalf("failed command changed value to %q", got)
			}
			if len(s.pending) != 0 {
				t.Fatal("failed command still pending")
			}
		})
	}
}

func TestMobileReceiverRejectsNoiseWrites(t *testing.T) {
	for _, id := range []string{"noise-cancel", "noise-cancel-power"} {
		for _, unit := range []int{0, 1} {
			s, conn := testSession(10 * time.Millisecond)
			s.snapshot.State.RX.Name = "DJI Mic Mini 2"
			setting, _ := duml.SettingByID(id)
			value := setting.Options[0].Value
			var err error
			if unit == 0 {
				err = s.Send(id, value)
			} else {
				err = s.SendToTransmitter(id, value, unit)
			}
			if err == nil || !strings.Contains(err.Error(), "O / L / H") {
				t.Errorf("%s unit %d: expected hardware switch hint, got %v", id, unit, err)
			}
			if len(conn.written) != 0 || len(s.pending) != 0 {
				t.Errorf("%s unit %d: read-only setting sent to device", id, unit)
			}
		}
	}
}

func TestMobileReceiverRejectsUnsupportedSettings(t *testing.T) {
	for _, id := range []string{"noise-cancel-button", "auto-off-15m", "camera-power"} {
		s, conn := testSession(10 * time.Millisecond)
		s.snapshot.State.RX.Name = "DJI Mic Mini 2"
		if err := s.Send(id, "on"); err == nil || !strings.Contains(err.Error(), "不可用") {
			t.Errorf("%s: expected unavailable error, got %v", id, err)
		}
		if len(conn.written) != 0 || len(s.pending) != 0 {
			t.Errorf("%s: unsupported command sent to device", id)
		}
	}
}

func TestVoiceToneValidatesTargetProduct(t *testing.T) {
	for _, unit := range []int{0, 1} {
		s, conn := testSession(10 * time.Millisecond)
		s.snapshot.State.RX.Name = "DJI Mic Mini 2"
		s.snapshot.State.TX[0].Name = "DJI Mic Mini"
		var err error
		if unit == 0 {
			err = s.Send("voice-tone", "bright")
		} else {
			err = s.SendToTransmitter("voice-tone", "bright", unit)
		}
		if err == nil || len(conn.written) != 0 {
			t.Errorf("unit %d: unsupported voice tone reached device: %v", unit, err)
		}
	}
}
