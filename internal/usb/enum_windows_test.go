//go:build windows

package usb

import "testing"

// A receiver comes and goes, and Windows remembers every instance it has seen.
// These tests pin down which node the app picks out of that history: a node on
// the bus, preferably the one already bound to WinUSB, and never a remembered
// one while a live one exists.
func TestPickControl(t *testing.T) {
	receiver := func(instance string, present bool, service string) Info {
		return Info{
			InstanceID: `USB\VID_2CA3&PID_4011&MI_06\` + instance,
			HardwareID: `USB\VID_2CA3&PID_4011&MI_06`,
			Vendor:     0x2ca3,
			Product:    0x4011,
			Interface:  6,
			Present:    present,
			Driver:     DriverInfo{Service: service},
		}
	}
	audio := Info{
		InstanceID: `USB\VID_2CA3&PID_4011&MI_01\9&18F74E0F&1&0001`,
		HardwareID: `USB\VID_2CA3&PID_4011&MI_01`,
		Vendor:     0x2ca3,
		Product:    0x4011,
		Interface:  1,
		Present:    true,
		Driver:     DriverInfo{Service: "usbaudio"},
	}
	otherDevice := Info{
		InstanceID: `USB\VID_1234&PID_5678&MI_06\5&1&0&0006`,
		HardwareID: `USB\VID_1234&PID_5678&MI_06`,
		Vendor:     0x1234,
		Product:    0x5678,
		Interface:  6,
		Present:    true,
		Driver:     DriverInfo{Service: "WINUSB"},
	}

	for _, tc := range []struct {
		name  string
		nodes []Info
		want  string // instance id, or "" for none
	}{
		{
			name:  "only the audio interface is present",
			nodes: []Info{audio},
			want:  "",
		},
		{
			name:  "a live node with no driver",
			nodes: []Info{audio, receiver("9&18F74E0F&1&0006", true, "")},
			want:  `USB\VID_2CA3&PID_4011&MI_06\9&18F74E0F&1&0006`,
		},
		{
			name: "a ghost and a live node: the live one wins",
			nodes: []Info{
				receiver("9&18F74E0F&1&0006", false, ""),
				audio,
				receiver("a&2BB12C4F&0&0006", true, "WINUSB"),
			},
			want: `USB\VID_2CA3&PID_4011&MI_06\a&2BB12C4F&0&0006`,
		},
		{
			name: "the ghost is listed last: it still must not win",
			nodes: []Info{
				receiver("a&2BB12C4F&0&0006", true, "WINUSB"),
				receiver("9&18F74E0F&1&0006", false, ""),
			},
			want: `USB\VID_2CA3&PID_4011&MI_06\a&2BB12C4F&0&0006`,
		},
		{
			name: "two live nodes: the WinUSB-bound one wins",
			nodes: []Info{
				receiver("a&2BB12C4F&0&0006", true, ""),
				receiver("b&3CC24D5F&0&0006", true, "WINUSB"),
			},
			want: `USB\VID_2CA3&PID_4011&MI_06\b&3CC24D5F&0&0006`,
		},
		{
			name: "a live node with the wrong driver still beats a ghost",
			nodes: []Info{
				receiver("9&18F74E0F&1&0006", false, "WINUSB"),
				receiver("a&2BB12C4F&0&0006", true, "usbser"),
			},
			want: `USB\VID_2CA3&PID_4011&MI_06\a&2BB12C4F&0&0006`,
		},
		{
			name:  "another vendor's interface 6 is not the receiver",
			nodes: []Info{otherDevice},
			want:  "",
		},
		{
			name:  "nothing at all",
			nodes: nil,
			want:  "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pickControl(tc.nodes)
			switch {
			case tc.want == "" && got != nil:
				t.Errorf("picked %s, want no control interface", got.InstanceID)
			case tc.want != "" && got == nil:
				t.Errorf("picked nothing, want %s", tc.want)
			case tc.want != "" && got.InstanceID != tc.want:
				t.Errorf("picked %s, want %s", got.InstanceID, tc.want)
			}
		})
	}
}

// The status the window reads has to agree with the node that was picked.
func TestStatusReadyFollowsPickedNode(t *testing.T) {
	live := Info{
		InstanceID: `USB\VID_2CA3&PID_4011&MI_06\a&2BB12C4F&0&0006`,
		HardwareID: `USB\VID_2CA3&PID_4011&MI_06`,
		Vendor:     0x2ca3,
		Product:    0x4011,
		Interface:  6,
		Present:    true,
		Driver:     DriverInfo{Service: "WINUSB"},
	}
	ghost := live
	ghost.InstanceID = `USB\VID_2CA3&PID_4011&MI_06\9&18F74E0F&1&0006`
	ghost.Present = false
	ghost.Driver = DriverInfo{}

	status := Status{Devices: []Info{ghost, live}}
	status.Control = pickControl(status.Devices)
	if !status.Ready() {
		t.Fatalf("a live WinUSB node should be ready; control = %+v", status.Control)
	}
	if status.Control.Present != true || status.Control.Driver.Service != "WINUSB" {
		t.Errorf("control = %+v", *status.Control)
	}

	// And the reverse: with only the ghost left, the app must not claim to be
	// ready.
	status = Status{Devices: []Info{ghost}}
	status.Control = pickControl(status.Devices)
	if status.Ready() {
		t.Error("a remembered node must not look ready")
	}
	if status.Connected() {
		t.Error("nothing is on the bus, so nothing is connected")
	}
}
