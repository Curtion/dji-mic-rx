package console

import (
	"fmt"
	"sync"
	"time"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
	"dji-mic-rx/internal/usb"
)

// DemoKind is the situation a demo source stands in for.
type DemoKind int

const (
	// DemoLive is a Mic Mini 2 with both transmitters connected and the
	// driver in place.
	DemoLive DemoKind = iota
	// DemoNoInterface is a receiver whose vendor interface is not on the
	// device tree at all, which is what a missing driver looks like when
	// the interface has never been enumerated.
	DemoNoInterface
	// DemoNoDriver is a receiver whose vendor interface exists with no
	// driver bound to it.
	DemoNoDriver
	// DemoV1 is a first generation Mic Mini: protocol v1, no battery
	// levels, no voice tone.
	DemoV1
)

// Demo is a stand-in for the device session, so every page can be drawn — and
// looked at — without a receiver plugged in.
type Demo struct {
	kind  DemoKind
	mu    sync.Mutex
	snap  session.Snapshot
	logAt time.Time
}

// NewDemo returns a source that stands in for the receiver.
func NewDemo(kind DemoKind) *Demo {
	d := &Demo{kind: kind}
	d.snap = d.build()
	return d
}

func (d *Demo) build() session.Snapshot {
	now := time.Now()
	snap := session.Snapshot{
		Time:            now,
		HaveModel:       true,
		Model:           mustModel(),
		State:           duml.NewState(),
		FramesPerSecond: 10.2,
		LastFrame:       now.Add(-80 * time.Millisecond),
		Tools:           session.Tools{WdiSimple: `C:\tools\wdi-simple.exe`, WdiSimpleSet: true},
		Log: []session.LogLine{
			{At: now.Add(-42 * time.Second), Text: "已连接接收器（DJI Mic Mobile RX (DMMR01 / DMMR02)，接口 6，端点 0x06/0x86）"},
			{At: now.Add(-42 * time.Second), Text: "endpoints: 0x06(bulk,512) 0x86(bulk,512)"},
			{At: now.Add(-42 * time.Second), Text: "协议版本 v2，状态推送已开始"},
			{At: now.Add(-12 * time.Second), Text: "已设置 降噪强度 = 强"},
		},
	}

	nodes := []usb.Info{
		{
			InstanceID:  `USB\VID_2CA3&PID_4011\XSP12345678B`,
			HardwareID:  `USB\VID_2CA3&PID_4011`,
			Vendor:      0x2ca3,
			Product:     0x4011,
			Interface:   -1,
			Present:     true,
			Description: "USB Composite Device",
			Driver:      usb.DriverInfo{Service: "usbccgp", InfPath: "usb.inf", Provider: "Microsoft", Version: "10.0.26100.1"},
		},
		{
			InstanceID:  `USB\VID_2CA3&PID_4011&MI_00\9&18F74E0F&1&0000`,
			HardwareID:  `USB\VID_2CA3&PID_4011&MI_00`,
			Vendor:      0x2ca3,
			Product:     0x4011,
			Interface:   0,
			Present:     true,
			Description: "USB Input Device",
			Driver:      usb.DriverInfo{Service: "HidUsb", InfPath: "input.inf", Provider: "Microsoft", Version: "10.0.26100.1"},
		},
		{
			InstanceID:  `USB\VID_2CA3&PID_4011&MI_01\9&18F74E0F&1&0001`,
			HardwareID:  `USB\VID_2CA3&PID_4011&MI_01`,
			Vendor:      0x2ca3,
			Product:     0x4011,
			Interface:   1,
			Present:     true,
			Description: "Wireless Mic Rx",
			Driver:      usb.DriverInfo{Service: "usbaudio", InfPath: "wdma_usb.inf", Provider: "Microsoft", Version: "10.0.26100.9457"},
		},
	}
	control := usb.Info{
		InstanceID:  `USB\VID_2CA3&PID_4011&MI_06\9&18F74E0F&1&0006`,
		HardwareID:  `USB\VID_2CA3&PID_4011&MI_06`,
		Vendor:      0x2ca3,
		Product:     0x4011,
		Interface:   6,
		Present:     true,
		Description: "DJI Mic Receiver control interface",
		Driver: usb.DriverInfo{
			Service: "WINUSB", InfPath: "oem42.inf",
			Provider: "DJI Mic Control", Description: "DJI Mic Receiver control interface",
			Version: "01/01/2026 1.0.0.0",
		},
	}

	switch d.kind {
	case DemoLive, DemoV1:
		nodes = append(nodes, control)
		snap.Status = usb.Status{Devices: nodes, Control: &control, Scanned: now}
		snap.Connected = true
		snap.Notes = []string{"endpoints: 0x06(bulk,512) 0x86(bulk,512)"}
		snap.State = liveState(d.kind == DemoV1)
	case DemoNoInterface:
		snap.Status = usb.Status{Devices: nodes, Scanned: now}
		snap.ConnectionError = "接收器已连接，但它的厂商接口（接口 6）还没有出现。装上驱动后请重新插拔接收器。"
	case DemoNoDriver:
		noDriver := control
		noDriver.Driver = usb.DriverInfo{}
		nodes = append(nodes, noDriver)
		snap.Status = usb.Status{Devices: nodes, Control: &noDriver, Scanned: now}
		snap.ConnectionError = "厂商接口还没有绑定 WinUSB 驱动，所以收不到麦克风状态。"
		// The state this machine really ends up in: Windows refuses the
		// package the app writes itself, because it carries no signature.
		snap.UnsignedRefused = true
		snap.DriverLogPath = `C:\Users\Curtion\AppData\Roaming\dji-mic-rx\logs\driver-20261006-200150.log`
		snap.InstallLog = []string{
			`以管理员身份运行：[--driver-install C:\Users\Curtion\AppData\Local\Temp\dji-mic-rx-driver-541133507\dji_mic_rx_control.inf --log ...]`,
			`安装驱动包：C:\Users\Curtion\AppData\Local\Temp\dji-mic-rx-driver-541133507\dji_mic_rx_control.inf`,
			`  Microsoft PnP Utility`,
			`  Adding driver package:  dji_mic_rx_control.inf`,
			`  Failed to add driver package: 该驱动程序包的签名无效或不存在（0xE000022F）`,
			`  Total driver packages:  1`,
			`  Added driver packages:  0`,
			`pnputil 失败：exit status 5`,
		}
	}
	return snap
}

// liveState is the decoded state of a receiver that is working.
func liveState(v1 bool) duml.State {
	state := duml.NewState()
	if v1 {
		state.Dialect, state.DialectKnown = duml.V1, true
		state.RX = duml.RXInfo{
			Serial:   "3PCDM7B0A1K9Z2",
			Firmware: "01.01.00.56",
		}
		state.TX[0] = duml.TXInfo{
			Present: true, Unit: 1, Level: 62,
			Serial: "4S1ABC1234567X", Firmware: "01.01.00.56",
		}
		state.TX[1] = duml.TXInfo{
			Present: true, Unit: 2, Level: 24,
			Serial: "4S1ABC7654321Y", Firmware: "01.01.00.56",
		}
		for id, value := range map[string]string{
			"noise-cancel": "strong", "noise-cancel-power": "on", "low-cut": "off",
			"stereo": "mono", "safety-track": "off", "clip-limiter": "on",
			"auto-off-15m": "on", "camera-power": "off", "mic-leds": "on",
			"plug-free": "off",
		} {
			state.Settings[id] = value
		}
		state.Packets = 2214
		return state
	}

	state.Dialect, state.DialectKnown = duml.V2, true
	state.RX = duml.RXInfo{
		Name:         "DJI Mic Mini 2",
		Serial:       "3PCDM7B0A1K9Z2",
		Firmware:     "02.03.17.00",
		BatteryGauge: 2,
		GainDial:     6,
		HasGain:      true,
	}
	state.TX[0] = duml.TXInfo{
		Present: true, Unit: 1, Level: 62,
		Name: "DJI Mic Mini 2", Serial: "4S1ABC1234567X", Firmware: "01.01.00.56",
		BatteryGauge: 1, VoiceTone: "rich",
	}
	state.TX[1] = duml.TXInfo{
		Present: true, Unit: 2, Level: 24,
		Name: "DJI Mic Mini 2", Serial: "4S1ABC7654321Y", Firmware: "01.01.00.56",
		BatteryGauge: 5, Charging: true, VoiceTone: "standard",
	}
	for id, value := range map[string]string{
		"noise-cancel": "strong", "noise-cancel-power": "on", "noise-cancel-button": "on",
		"low-cut": "on", "stereo": "stereo", "safety-track": "off", "clip-limiter": "on",
		"auto-off-15m": "on", "tx-auto-off-15m": "off", "camera-power": "off",
		"mic-leds": "on", "plug-free": "off",
	} {
		state.Settings[id] = value
	}
	state.Packets = 3841
	state.Updated = time.Now()
	return state
}

func mustModel() duml.Model {
	model, _ := duml.ModelFor(0x2ca3, 0x4011)
	return model
}

// Snapshot returns the demo state.
func (d *Demo) Snapshot() session.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	snap := d.snap
	snap.Time = time.Now()
	if snap.Connected {
		snap.LastFrame = snap.Time.Add(-80 * time.Millisecond)
		snap.State.Updated = snap.LastFrame
	}
	return snap
}

// Rescan does nothing.
func (d *Demo) Rescan() { d.Log("重新检测设备") }

// Log records a line in the demo's log.
func (d *Demo) Log(format string, a ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.snap.Log = append(d.snap.Log, session.LogLine{
		At:   time.Now(),
		Text: fmt.Sprintf(format, a...),
	})
}

// Send pretends to write a setting and reports that it worked.
func (d *Demo) Send(settingID, value string) error {
	d.Log("已设置 %s = %s", settingID, value)
	return nil
}

// SendToTransmitter pretends to write a transmitter setting.
func (d *Demo) SendToTransmitter(settingID, value string, unit int) error {
	d.Log("已设置发射器 %d %s = %s", unit, settingID, value)
	return nil
}

// Install pretends to install the driver.
func (d *Demo) Install(progress func(string)) error {
	lines := []string{
		"以管理员身份运行：--driver-install-wdi C:\\tools\\wdi-simple.exe",
		"调用 wdi-simple.exe 把 WinUSB 绑定到接口 6",
		"  wdi-simple: installing driver for USB\\VID_2CA3&PID_4011&MI_06",
		"  wdi-simple: driver installed successfully",
		"wdi-simple.exe 完成。请重新插拔接收器。",
	}
	d.mu.Lock()
	d.snap.InstallLog = append([]string(nil), lines...)
	d.mu.Unlock()
	if progress != nil {
		for _, line := range lines {
			progress(line)
		}
	}
	return nil
}

// Uninstall pretends to remove the driver.
func (d *Demo) Uninstall(progress func(string)) error {
	lines := []string{"移除驱动包：oem42.inf", "  pnputil: 已成功删除驱动程序包。"}
	d.mu.Lock()
	d.snap.InstallLog = append([]string(nil), lines...)
	d.mu.Unlock()
	if progress != nil {
		for _, line := range lines {
			progress(line)
		}
	}
	return nil
}

// OpenZadig pretends to start Zadig.
func (d *Demo) OpenZadig() error { d.Log("已打开 Zadig"); return nil }

// OpenWdiSimple pretends to run wdi-simple.exe.
func (d *Demo) OpenWdiSimple(path string) error {
	return d.Install(nil)
}

// RememberWdiSimple stores a helper path.
func (d *Demo) RememberWdiSimple(path string) {
	d.mu.Lock()
	d.snap.Tools.WdiSimple, d.snap.Tools.WdiSimpleSet = path, path != ""
	d.mu.Unlock()
}

// Diagnostics renders a short report.
func (d *Demo) Diagnostics() string {
	return "DJI Mic 接收器控制台 · 诊断信息（示例数据）\n"
}
