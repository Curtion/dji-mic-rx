// Package usb finds the DJI receiver's vendor interface on the system and
// talks to it through WinUSB.
//
// On Windows a vendor-specific USB interface has no driver out of the box, so
// nothing user mode can open. The app installs Microsoft's in-box WinUSB
// driver on that one interface, leaving the device's audio and HID interfaces
// on their system drivers.
package usb

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"dji-mic-rx/internal/duml"
)

// ErrUnsupported reports that the platform cannot talk to the receiver
// directly; the app then explains what to do instead.
var ErrUnsupported = errors.New("usb: this app talks to the receiver on Windows only")

// DriverInfo describes the driver bound to one device node.
type DriverInfo struct {
	// Service is the kernel service the node is bound to: "WINUSB" once the
	// app's driver is installed, "usbaudio" or "HidUsb" for the device's
	// other interfaces, or empty when no driver matched.
	Service string
	// InfPath is the driver package as the driver store names it, e.g.
	// "oem42.inf"; this is what pnputil needs to remove it.
	InfPath string
	// Provider is who published the package, e.g. "Microsoft".
	Provider string
	// Description and Version come from the driver package.
	Description string
	Version     string
}

// Info is one device node of the receiver: the whole device, or one of its
// interfaces.
type Info struct {
	// InstanceID is the node's device instance id, e.g.
	// USB\VID_2CA3&PID_4011&MI_06\9&18F74E0F&1&0006.
	InstanceID string
	// HardwareID is the id without the instance part, e.g.
	// USB\VID_2CA3&PID_4011&MI_06.
	HardwareID string
	// Vendor and Product are the USB ids.
	Vendor, Product uint16
	// Interface is the interface number, or -1 for the whole device.
	Interface int
	// Present is true while the node is on the bus.
	Present bool
	// Description is what Windows calls the node, e.g. "Wireless Mic Rx".
	Description string
	// Driver is the driver package bound to the node.
	Driver DriverInfo
}

// String renders the node for diagnostics.
func (i Info) String() string {
	m := "whole device"
	if i.Interface >= 0 {
		m = fmt.Sprintf("interface %d", i.Interface)
	}
	return fmt.Sprintf("%s (%s, %s)", i.HardwareID, i.Description, m)
}

// Model returns the protocol table entry for this device.
func (i Info) Model() (duml.Model, bool) { return duml.ModelFor(i.Vendor, i.Product) }

// IsControlInterface reports whether this node is the vendor interface that
// carries the protocol.
func (i Info) IsControlInterface() bool {
	m, ok := i.Model()
	return ok && i.Interface == int(m.Interface)
}

// ReadyToOpen reports whether the node is the one the app should open, and
// whether the WinUSB driver is bound to it.
func (i Info) ReadyToOpen() (ok bool, why string) {
	m, known := i.Model()
	if !known {
		return false, "不认识的型号（USB id " + vidpid(i.Vendor, i.Product) + "）"
	}
	if i.Interface != int(m.Interface) {
		return false, fmt.Sprintf("这是接口 %d，协议在接口 %d 上", i.Interface, m.Interface)
	}
	if !i.Present {
		return false, "设备不在总线上"
	}
	if !strings.EqualFold(i.Driver.Service, "WINUSB") {
		if i.Driver.Service == "" {
			return false, "接口没有绑定驱动（需要安装 WinUSB）"
		}
		return false, fmt.Sprintf("接口绑定的是 %s 驱动（需要 WinUSB）", i.Driver.Service)
	}
	return true, ""
}

func vidpid(vendor, product uint16) string {
	return fmt.Sprintf("%04x:%04x", vendor, product)
}

// Status is a snapshot of everything the app can learn about the receiver's
// presence and driver without opening it.
type Status struct {
	// Devices is every device node of every known receiver family, present
	// or remembered by Windows.
	Devices []Info
	// Control is the vendor interface node, if it exists at all.
	Control *Info
	// Scanned is when the scan ran.
	Scanned time.Time
}

// Model returns the table entry of the receiver the system knows about, if
// any node matched a known USB id.
func (s Status) Model() (duml.Model, bool) {
	for _, d := range s.Devices {
		if m, ok := d.Model(); ok {
			return m, true
		}
	}
	return duml.Model{}, false
}

// Connected reports whether any node of a DJI device is present.
func (s Status) Connected() bool {
	for _, d := range s.Devices {
		if d.Present {
			return true
		}
	}
	return false
}

// UnknownDevices returns the DJI device nodes that are on the bus but whose
// USB ids are not in the protocol table. The app shows them and refuses to
// touch them rather than guessing which interface to bind.
func (s Status) UnknownDevices() []Info {
	var out []Info
	for _, d := range s.Devices {
		if !d.Present {
			continue
		}
		if _, known := d.Model(); !known {
			out = append(out, d)
		}
	}
	return out
}

// Ready reports whether the vendor interface is present and bound to WinUSB,
// which is what opening the device needs.
func (s Status) Ready() bool {
	return s.Control != nil && s.Control.Present && strings.EqualFold(s.Control.Driver.Service, "WINUSB")
}
