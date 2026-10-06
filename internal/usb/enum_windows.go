//go:build windows

package usb

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// enumRoot is where Windows keeps one key per device node, present or
	// remembered.
	enumRoot = `SYSTEM\CurrentControlSet\Enum\USB`
	// classRoot holds the installed driver packages' settings, keyed by
	// class GUID and then by the device's driver key.
	classRoot = `SYSTEM\CurrentControlSet\Control\Class`
	// vendor is the DJI USB vendor id; the app only ever looks at this
	// vendor's devices.
	vendor = 0x2ca3
)

var (
	cfgmgr32 = windows.NewLazySystemDLL("cfgmgr32.dll")

	procCMGetDeviceIDListSize = cfgmgr32.NewProc("CM_Get_Device_ID_List_SizeW")
	procCMGetDeviceIDList     = cfgmgr32.NewProc("CM_Get_Device_ID_ListW")
)

// presentDeviceIDs returns the device instance ids of every USB device node
// currently on the bus. The registry remembers nodes that have been
// unplugged, so presence comes from the configuration manager instead.
func presentDeviceIDs() ([]string, error) {
	filter, err := windows.UTF16PtrFromString("USB")
	if err != nil {
		return nil, err
	}
	var size uint32
	r1, _, _ := procCMGetDeviceIDListSize.Call(
		uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(filter)),
		uintptr(cmGetDeviceIDListPresent),
	)
	if r1 != 0 {
		return nil, fmt.Errorf("usb: listing devices failed (code %d)", r1)
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]uint16, size)
	r1, _, _ = procCMGetDeviceIDList.Call(
		uintptr(unsafe.Pointer(filter)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(size),
		uintptr(cmGetDeviceIDListPresent),
	)
	if r1 != 0 {
		return nil, fmt.Errorf("usb: reading device list failed (code %d)", r1)
	}
	return splitMultiSZ(buf), nil
}

// CM_GETIDLIST_FILTER_PRESENT lists only nodes that are on the bus now; the
// filter text itself ("USB") names an enumerator, which is filter type 0.
const cmGetDeviceIDListPresent = 0x00000100

// splitMultiSZ splits a double-NUL terminated UTF-16 string list.
func splitMultiSZ(buf []uint16) []string {
	var out []string
	for i := 0; i < len(buf); {
		if buf[i] == 0 {
			break
		}
		end := i
		for end < len(buf) && buf[end] != 0 {
			end++
		}
		out = append(out, windows.UTF16ToString(buf[i:end]))
		i = end + 1
	}
	return out
}

// Scan returns every node of every known receiver family: the vendor
// interface the protocol runs on, the audio and HID interfaces left alone,
// and the composite device itself.
func Scan() (Status, error) {
	status := Status{Scanned: time.Now()}

	present, err := presentDeviceIDs()
	if err != nil {
		return status, err
	}
	presentSet := make(map[string]bool, len(present))
	for _, id := range present {
		presentSet[strings.ToUpper(id)] = true
	}

	enum, err := registry.OpenKey(registry.LOCAL_MACHINE, enumRoot, registry.READ|registry.WOW64_64KEY)
	if err != nil {
		return status, fmt.Errorf("usb: opening the device registry failed: %w", err)
	}
	defer enum.Close()

	devices, err := enum.ReadSubKeyNames(-1)
	if err != nil {
		return status, err
	}
	for _, device := range devices {
		vid, pid, mi, ok := parseDeviceID(device)
		if !ok || vid != vendor {
			continue
		}
		// Every DJI device node is listed, not only the models the protocol
		// table knows: a receiver whose ids are not in the table is still
		// something to show a person, and the app must never bind a driver
		// to a guess. Whether a node carries the protocol is decided per node
		// by IsControlInterface.
		deviceKey, err := registry.OpenKey(enum, device, registry.READ)
		if err != nil {
			continue
		}
		instances, err := deviceKey.ReadSubKeyNames(-1)
		deviceKey.Close()
		if err != nil {
			continue
		}
		for _, instance := range instances {
			// The registry key is relative to Enum\USB, while a device
			// instance id carries its enumerator: USB\VID_...&MI_06\...
			keyPath := device + `\` + instance
			path := `USB\` + keyPath
			info := Info{
				InstanceID: path,
				HardwareID: device,
				Vendor:     vid,
				Product:    pid,
				Interface:  mi,
				Present:    presentSet[strings.ToUpper(path)],
			}
			readNode(enum, keyPath, &info)
			status.Devices = append(status.Devices, info)
		}
	}
	status.Control = pickControl(status.Devices)
	return status, nil
}

// pickControl chooses the node whose interface carries the protocol, out of
// every interface node the system knows about.
//
// This cannot simply be the last one found: the registry keeps an entry for
// every device instance Windows has ever seen, and a receiver that is
// unplugged and plugged back in comes back as a new instance, leaving the old
// one behind as a ghost. Choosing a remembered node while a live one is on the
// bus is exactly the kind of wrong answer that looks like "the interface
// disappeared".
func pickControl(nodes []Info) *Info {
	best := -1
	bestScore := 0
	for i := range nodes {
		if !nodes[i].IsControlInterface() {
			continue
		}
		if score := controlScore(nodes[i]); score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return nil
	}
	found := nodes[best]
	return &found
}

// controlScore ranks a node by how usable it is: on the bus beats remembered,
// and among the ones on the bus the WinUSB binding beats a node that has no
// driver or the wrong one, since only a WinUSB node can be opened.
func controlScore(node Info) int {
	if !node.Present {
		return 0
	}
	switch {
	case strings.EqualFold(node.Driver.Service, "WINUSB"):
		return 3
	case node.Driver.Service == "":
		return 2
	default:
		return 1
	}
}

// parseDeviceID reads the vendor, product and interface out of a registry
// key name such as VID_2CA3&PID_4011&MI_06.
func parseDeviceID(name string) (vid, pid uint16, mi int, ok bool) {
	mi = -1
	upper := strings.ToUpper(name)
	if !strings.HasPrefix(upper, "VID_") {
		return 0, 0, -1, false
	}
	parts := strings.Split(upper, "&")
	v, err := strconv.ParseUint(strings.TrimPrefix(parts[0], "VID_"), 16, 16)
	if err != nil {
		return 0, 0, -1, false
	}
	vid = uint16(v)
	for _, part := range parts[1:] {
		switch {
		case strings.HasPrefix(part, "PID_"):
			p, err := strconv.ParseUint(strings.TrimPrefix(part, "PID_"), 16, 16)
			if err != nil {
				return 0, 0, -1, false
			}
			pid = uint16(p)
		case strings.HasPrefix(part, "MI_"):
			m, err := strconv.ParseUint(strings.TrimPrefix(part, "MI_"), 16, 8)
			if err != nil {
				continue
			}
			mi = int(m)
		}
	}
	return vid, pid, mi, true
}

// readNode fills in a node's description and driver details.
func readNode(root registry.Key, path string, info *Info) {
	key, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		return
	}
	defer key.Close()

	info.Description = firstNonEmpty(
		regString(key, "FriendlyName"),
		unexpandDescription(regString(key, "DeviceDesc")),
	)
	info.Driver.Service = regString(key, "Service")

	driver := regString(key, "Driver")
	classGUID := regString(key, "ClassGUID")
	if driver == "" || classGUID == "" {
		return
	}
	// The Driver value is relative to the class root and reads
	// "{class guid}\<n>"; some builds store only the index, so both shapes
	// are handled. Concatenating the class guid twice, as this once did,
	// silently finds nothing.
	driverRef := driver
	if !strings.Contains(driver, `\`) {
		driverRef = classGUID + `\` + driver
	}
	driverKey, err := registry.OpenKey(registry.LOCAL_MACHINE, classRoot+`\`+driverRef, registry.READ)
	if err != nil {
		return
	}
	defer driverKey.Close()
	info.Driver.InfPath = regString(driverKey, "InfPath")
	info.Driver.Provider = regString(driverKey, "ProviderName")
	info.Driver.Description = regString(driverKey, "DriverDesc")
	info.Driver.Version = regString(driverKey, "DriverVersion")
}

func regString(key registry.Key, name string) string {
	v, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// unexpandDescription returns the readable tail of a description Windows
// stores as @<inf>,%string%;<fallback>, which is what a person should see.
func unexpandDescription(desc string) string {
	if i := strings.LastIndex(desc, ";"); i >= 0 {
		return strings.TrimSpace(desc[i+1:])
	}
	return desc
}
