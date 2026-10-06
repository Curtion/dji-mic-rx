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
	procWinUsbSetPipePolicy              = winusbDLL.NewProc("WinUsb_SetPipePolicy")
	procWinUsbSetCurrentAlternateSetting = winusbDLL.NewProc("WinUsb_SetCurrentAlternateSetting")
	procWinUsbReadPipe                   = winusbDLL.NewProc("WinUsb_ReadPipe")
	procWinUsbWritePipe                  = winusbDLL.NewProc("WinUsb_WritePipe")
	procWinUsbAbortPipe                  = winusbDLL.NewProc("WinUsb_AbortPipe")
	procCMGetDeviceInterfaceListSize     = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceList         = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")
)

// Device interface GUIDs to try, in order, when looking for the interface
// path: the one this app's INF registers, then the standard USB device
// interface GUID it registers alongside it.
var candidateInterfaceGUIDs = []windows.GUID{
	{Data1: 0xc4f1c6a9, Data2: 0x1f4e, Data3: 0x4b2c, Data4: [8]byte{0x9f, 0x7b, 0x0d, 0x5c, 0x2a, 0x7e, 0x4b, 0x31}},
	{Data1: 0xa5dcbf10, Data2: 0x6530, Data3: 0x11d2, Data4: [8]byte{0x90, 0x1f, 0x00, 0xc0, 0x4f, 0xb9, 0x51, 0xed}},
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

// Conn is an open connection to the receiver's vendor interface.
type Conn struct {
	device windows.Handle
	iface  windows.Handle
	path   string
	model  duml.Model
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
