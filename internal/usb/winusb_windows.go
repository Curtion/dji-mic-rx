//go:build windows

package usb

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"dji-mic-rx/internal/duml"
)

var (
	winusbDLL = windows.NewLazySystemDLL("winusb.dll")

	procWinUsbInitialize                 = winusbDLL.NewProc("WinUsb_Initialize")
	procWinUsbFree                       = winusbDLL.NewProc("WinUsb_Free")
	procWinUsbQueryInterfaceSettings     = winusbDLL.NewProc("WinUsb_QueryInterfaceSettings")
	procWinUsbQueryPipeEx                = winusbDLL.NewProc("WinUsb_QueryPipeEx")
	procWinUsbQueryPipe                  = winusbDLL.NewProc("WinUsb_QueryPipe")
	procWinUsbSetPipePolicy              = winusbDLL.NewProc("WinUsb_SetPipePolicy")
	procWinUsbSetCurrentAlternateSetting = winusbDLL.NewProc("WinUsb_SetCurrentAlternateSetting")
	procWinUsbReadPipe                   = winusbDLL.NewProc("WinUsb_ReadPipe")
	procWinUsbWritePipe                  = winusbDLL.NewProc("WinUsb_WritePipe")
	procWinUsbAbortPipe                  = winusbDLL.NewProc("WinUsb_AbortPipe")
	procWinUsbResetPipe                  = winusbDLL.NewProc("WinUsb_ResetPipe")
	procCMGetDeviceInterfaceListSize     = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceList         = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")
)

// Device interface GUIDs to try, in order, when looking for the interface
// path: the one this app's INF registers, the standard USB device interface
// GUID, and the one Microsoft's WinUSB INF template uses. A driver package
// installed by libwdi (Zadig, wdi-simple) registers the middle one.
var candidateInterfaceGUIDs = []windows.GUID{
	{Data1: 0xc4f1c6a9, Data2: 0x1f4e, Data3: 0x4b2c, Data4: [8]byte{0x9f, 0x7b, 0x0d, 0x5c, 0x2a, 0x7e, 0x4b, 0x31}},
	{Data1: 0xa5dcbf10, Data2: 0x6530, Data3: 0x11d2, Data4: [8]byte{0x90, 0x1f, 0x00, 0xc0, 0x4f, 0xb9, 0x51, 0xed}},
	{Data1: 0xdee824ef, Data2: 0x729b, Data3: 0x4a0e, Data4: [8]byte{0x9c, 0x14, 0xb7, 0x11, 0x7d, 0x33, 0xa8, 0x17}},
}

const (
	// cmGetDeviceInterfaceListPresent lists interfaces that exist now.
	cmGetDeviceInterfaceListPresent = 0x00000000

	// WinUSB pipe policies.
	pipeTransferTimeout = 0x03
	allowPartialReads   = 0x05
)

// ErrTimeout reports that a read found no status frame before its timeout.
// It is normal: the receiver pushes about ten frames a second, and between
// them the read pipe is simply empty.
var ErrTimeout = errors.New("usb: read timed out")

// ErrClosed reports that the connection was closed.
var ErrClosed = errors.New("usb: connection closed")

// readTimeout bounds one bulk read. Short enough that closing the device does
// not have to wait long, long enough that a status frame is never missed.
const readTimeout = 300 * time.Millisecond

// Pipe is one endpoint WinUSB reported for the interface.
type Pipe struct {
	Address   byte
	Type      uint32
	MaxPacket uint16
	Interval  byte
}

// Conn is an open connection to the receiver's vendor interface.
type Conn struct {
	device windows.Handle
	iface  windows.Handle
	path   string
	model  duml.Model
	pipes  []Pipe
	notes  []string

	closed atomic.Bool
	mu     sync.Mutex
}

// Open opens the vendor interface of a present, WinUSB-bound device node.
func Open(info Info) (*Conn, error) {
	if !info.Present {
		return nil, errors.New("usb: the device is not on the bus")
	}
	model, known := info.Model()
	if !known {
		return nil, fmt.Errorf("usb: unknown device %s", info.HardwareID)
	}
	if info.Interface != int(model.Interface) {
		return nil, fmt.Errorf("usb: %s is interface %d, but the protocol is on interface %d",
			info.HardwareID, info.Interface, model.Interface)
	}

	path, err := deviceInterfacePath(info.InstanceID)
	if err != nil {
		return nil, err
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("usb: opening %s failed: %w. Is WinUSB installed for interface %d?",
			path, err, model.Interface)
	}

	// WinUSB refuses a handle that was not opened for overlapped I/O, even
	// though this connection uses it synchronously: every call below passes no
	// OVERLAPPED structure and simply blocks until it finishes or times out.
	var iface windows.Handle
	if r1, _, callErr := procWinUsbInitialize.Call(uintptr(handle), uintptr(unsafe.Pointer(&iface))); r1 == 0 {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("usb: WinUsb_Initialize failed: %w", lastError(callErr))
	}

	c := &Conn{device: handle, iface: iface, path: path, model: model}

	// The app's driver registers the standard USB device interface, so
	// WinUSB is already on the interface's first setting; only the 2S
	// receivers need another one.
	if model.AltSetting != 0 {
		if r1, _, callErr := procWinUsbSetCurrentAlternateSetting.Call(uintptr(iface), uintptr(model.AltSetting)); r1 == 0 {
			c.notes = append(c.notes, fmt.Sprintf("switching to alternate setting %d failed: %v",
				model.AltSetting, lastError(callErr)))
		} else {
			c.notes = append(c.notes, fmt.Sprintf("using alternate setting %d", model.AltSetting))
		}
	}

	c.pipes = c.queryPipes()
	c.notes = append(c.notes, c.describePipes())

	// A read must be able to give up: the receive loop polls for a close and
	// must not sit in a pipe with no traffic in it. Partial reads are turned
	// off so a timeout is reported as a timeout instead of as a short frame.
	c.setPipePolicy(model.BulkIn, pipeTransferTimeout, uint32(readTimeout/time.Millisecond))
	c.setPipePolicy(model.BulkIn, allowPartialReads, 0)

	return c, nil
}

// Notes returns what happened while opening the device, for the app's
// diagnostics: alternate setting, endpoints found, and anything unexpected.
func (c *Conn) Notes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.notes...)
}

// Path is the device interface path the connection was opened from.
func (c *Conn) Path() string { return c.path }

// Read fills p with the next chunk of the receiver's status stream.
func (c *Conn) Read(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	var read uint32
	r1, _, callErr := procWinUsbReadPipe.Call(
		uintptr(c.iface),
		uintptr(c.model.BulkIn),
		uintptr(unsafe.Pointer(&p[0])),
		uintptr(uint32(len(p))),
		uintptr(unsafe.Pointer(&read)),
		0,
	)
	if r1 == 0 {
		err := lastError(callErr)
		if isTimeout(err) {
			return 0, ErrTimeout
		}
		if c.closed.Load() {
			return 0, ErrClosed
		}
		return 0, fmt.Errorf("usb: reading from the receiver failed: %w", err)
	}
	return int(read), nil
}

// Write sends one command frame to the receiver.
func (c *Conn) Write(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	var written uint32
	r1, _, callErr := procWinUsbWritePipe.Call(
		uintptr(c.iface),
		uintptr(c.model.BulkOut),
		uintptr(unsafe.Pointer(&p[0])),
		uintptr(uint32(len(p))),
		uintptr(unsafe.Pointer(&written)),
		0,
	)
	if r1 == 0 {
		err := lastError(callErr)
		if isTimeout(err) {
			return 0, ErrTimeout
		}
		return 0, fmt.Errorf("usb: writing to the receiver failed: %w", err)
	}
	return int(written), nil
}

// Close aborts the pipes and releases the device.
func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	if c.iface != 0 {
		// Abort pending transfers so a blocked read returns at once.
		procWinUsbAbortPipe.Call(uintptr(c.iface), uintptr(c.model.BulkIn))
		procWinUsbAbortPipe.Call(uintptr(c.iface), uintptr(c.model.BulkOut))
		procWinUsbFree.Call(uintptr(c.iface))
		c.iface = 0
	}
	if c.device != 0 {
		windows.CloseHandle(c.device)
		c.device = 0
	}
	return nil
}

// InterfaceAttempt is one device interface class the app tried for a node: the
// path it produced, and whether WinUSB accepted it. WinUsb_Initialize refuses
// a handle that was opened through an interface class the driver did not
// register, so when opening fails, this is what says which one works.
type InterfaceAttempt struct {
	GUID string
	Path string
	OK   bool
	Err  error
}

// ProbeInterfaces tries every device interface class registered for a node and
// reports the outcome of each. It is the diagnostic behind "the interface is
// bound to WinUSB but the app cannot open it".
func ProbeInterfaces(info Info) []InterfaceAttempt {
	deviceID, err := windows.UTF16PtrFromString(info.InstanceID)
	if err != nil {
		return []InterfaceAttempt{{Err: err}}
	}
	out := make([]InterfaceAttempt, 0, len(candidateInterfaceGUIDs))
	for _, guid := range candidateInterfaceGUIDs {
		attempt := InterfaceAttempt{GUID: guidText(guid)}
		path, err := interfacePathFor(guid, deviceID)
		if err != nil {
			attempt.Err = fmt.Errorf("没有接口路径: %w", err)
			out = append(out, attempt)
			continue
		}
		attempt.Path = path

		pathPtr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			attempt.Err = err
			out = append(out, attempt)
			continue
		}
		handle, err := windows.CreateFile(pathPtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
		if err != nil {
			attempt.Err = fmt.Errorf("CreateFile: %w", err)
			out = append(out, attempt)
			continue
		}
		var iface windows.Handle
		r1, _, callErr := procWinUsbInitialize.Call(uintptr(handle), uintptr(unsafe.Pointer(&iface)))
		if r1 == 0 {
			attempt.Err = fmt.Errorf("WinUsb_Initialize: %w", lastError(callErr))
		} else {
			attempt.OK = true
			procWinUsbFree.Call(uintptr(iface))
		}
		windows.CloseHandle(handle)
		out = append(out, attempt)
	}
	return out
}

// guidText renders a GUID the way the registry and the INF write it.
func guidText(g windows.GUID) string {
	return fmt.Sprintf("{%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x}",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3],
		g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

// deviceInterfacePath asks the configuration manager for the device interface
// path of a device node, which is what CreateFile opens. Each candidate
// interface class is tried in turn, since which one a driver package
// registered depends on who installed it.
func deviceInterfacePath(instanceID string) (string, error) {
	deviceID, err := windows.UTF16PtrFromString(instanceID)
	if err != nil {
		return "", err
	}
	var firstErr error
	for _, guid := range candidateInterfaceGUIDs {
		path, err := interfacePathFor(guid, deviceID)
		if err == nil {
			return path, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", fmt.Errorf("usb: %s registered no usable device interface: %v", instanceID, firstErr)
}

func interfacePathFor(guid windows.GUID, deviceID *uint16) (string, error) {
	var size uint32
	r1, _, callErr := procCMGetDeviceInterfaceListSize.Call(
		uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&guid)),
		uintptr(unsafe.Pointer(deviceID)),
		uintptr(cmGetDeviceInterfaceListPresent),
	)
	if r1 != 0 {
		return "", fmt.Errorf("looking up the device path failed: %w", lastError(callErr))
	}
	if size == 0 {
		return "", errors.New("no interface of this class")
	}
	buf := make([]uint16, size)
	// CM_Get_Device_Interface_ListW takes the interface class, then the
	// device, then the buffer to fill.
	r1, _, callErr = procCMGetDeviceInterfaceList.Call(
		uintptr(unsafe.Pointer(&guid)),
		uintptr(unsafe.Pointer(deviceID)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(size),
		uintptr(cmGetDeviceInterfaceListPresent),
	)
	if r1 != 0 {
		return "", fmt.Errorf("reading the device path failed: %w", lastError(callErr))
	}
	paths := splitMultiSZ(buf)
	if len(paths) == 0 {
		return "", errors.New("no interface path")
	}
	return paths[0], nil
}

// winusbPipeInformation mirrors WINUSB_PIPE_INFORMATION.
type winusbPipeInformation struct {
	PipeType          uint32
	PipeID            byte
	MaximumPacketSize uint16
	Interval          byte
}

// winusbPipeInformationEx mirrors WINUSB_PIPE_INFORMATION_EX.
type winusbPipeInformationEx struct {
	PipeType                uint32
	PipeID                  byte
	MaximumPacketSize       uint16
	Interval                byte
	MaximumBytesPerInterval uint32
}

// queryPipes lists the interface's endpoints, for the diagnostics log. The
// connection does not depend on it: the endpoint addresses come from the
// protocol table, because WinUSB's pipe querying differs across Windows
// versions.
func (c *Conn) queryPipes() []Pipe {
	var desc usbInterfaceDescriptor
	r1, _, _ := procWinUsbQueryInterfaceSettings.Call(
		uintptr(c.iface), uintptr(c.model.AltSetting), uintptr(unsafe.Pointer(&desc)))
	if r1 == 0 {
		c.notes = append(c.notes, "WinUsb_QueryInterfaceSettings failed; endpoints are taken from the protocol table")
		return nil
	}

	pipes := make([]Pipe, 0, desc.NumEndpoints)
	for i := byte(0); i < desc.NumEndpoints; i++ {
		var info winusbPipeInformationEx
		r1, _, _ := procWinUsbQueryPipeEx.Call(
			uintptr(c.iface), uintptr(c.model.AltSetting), uintptr(i), uintptr(unsafe.Pointer(&info)))
		if r1 == 0 {
			info = winusbPipeInformationEx{}
			var basic winusbPipeInformation
			r1, _, _ = procWinUsbQueryPipe.Call(
				uintptr(c.iface), uintptr(c.model.AltSetting), uintptr(i), uintptr(unsafe.Pointer(&basic)))
			if r1 == 0 {
				continue
			}
			info.PipeType = basic.PipeType
			info.PipeID = basic.PipeID
			info.MaximumPacketSize = basic.MaximumPacketSize
			info.Interval = basic.Interval
		}
		pipes = append(pipes, Pipe{
			Address:   info.PipeID,
			Type:      info.PipeType,
			MaxPacket: info.MaximumPacketSize,
			Interval:  info.Interval,
		})
	}
	return pipes
}

func (c *Conn) describePipes() string {
	if len(c.pipes) == 0 {
		return fmt.Sprintf("no pipe information; writing to 0x%02x and reading from 0x%02x as documented",
			c.model.BulkOut, c.model.BulkIn)
	}
	desc := "endpoints:"
	for _, p := range c.pipes {
		kind := "bulk"
		switch p.Type {
		case 1:
			kind = "isochronous"
		case 2:
			kind = "bulk"
		case 3:
			kind = "interrupt"
		}
		desc += fmt.Sprintf(" 0x%02x(%s,%d)", p.Address, kind, p.MaxPacket)
	}
	found := false
	for _, p := range c.pipes {
		if p.Address == c.model.BulkOut || p.Address == c.model.BulkIn {
			found = true
		}
	}
	if !found {
		desc += fmt.Sprintf("; expected 0x%02x and 0x%02x from the protocol table",
			c.model.BulkOut, c.model.BulkIn)
	}
	return desc
}

// setPipePolicy ignores failures: a policy the driver refuses is not fatal.
func (c *Conn) setPipePolicy(pipe, policy byte, value uint32) {
	procWinUsbSetPipePolicy.Call(
		uintptr(c.iface),
		uintptr(pipe),
		uintptr(policy),
		uintptr(4),
		uintptr(unsafe.Pointer(&value)),
	)
}

// usbInterfaceDescriptor mirrors USB_INTERFACE_DESCRIPTOR.
type usbInterfaceDescriptor struct {
	Length            byte
	DescriptorType    byte
	InterfaceNumber   byte
	AlternateSetting  byte
	NumEndpoints      byte
	InterfaceClass    byte
	InterfaceSubClass byte
	InterfaceProtocol byte
	Interface         byte
}

// lastError turns a syscall error into something worth showing, treating the
// zero errno WinUSB leaves behind on some failures as a plain failure.
func lastError(err error) error {
	if err == nil {
		return errors.New("the call failed")
	}
	if errno, ok := err.(windows.Errno); ok && errno == 0 {
		return errors.New("the call failed")
	}
	return err
}

func isTimeout(err error) bool {
	if errno, ok := err.(windows.Errno); ok {
		return errno == windows.ERROR_SEM_TIMEOUT || errno == windows.ERROR_TIMEOUT || errno == windows.ERROR_OPERATION_ABORTED
	}
	return false
}
