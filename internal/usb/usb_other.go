//go:build !windows

package usb

// Scan and the rest are Windows-only: the driver this app installs, and the
// device interface it opens, do not exist elsewhere.

// Scan reports that there is nothing to scan on this platform.
func Scan() (Status, error) { return Status{}, ErrUnsupported }

// Open reports that the platform cannot open the receiver.
func Open(Info) (*Conn, error) { return nil, ErrUnsupported }

// HasEmbeddedPackage reports that there is nothing to import elsewhere.
func HasEmbeddedPackage() bool { return false }

// InstallEmbeddedPackage reports that driver installation is Windows-only.
func InstallEmbeddedPackage(string, func(string)) (InstallResult, error) {
	return InstallResult{}, ErrUnsupported
}

// UninstallDriver reports that driver removal is Windows-only.
func UninstallDriver(Info, string, func(string)) (InstallResult, error) {
	return InstallResult{}, ErrUnsupported
}

// RunHelper is never reached on other platforms.
func RunHelper([]string) int { return 1 }

// IsElevated is meaningless outside Windows.
func IsElevated() bool { return false }

// Conn is only ever returned by the Windows implementation.
type Conn struct{}

// Read is unused outside Windows.
func (*Conn) Read([]byte) (int, error) { return 0, ErrUnsupported }

// Write is unused outside Windows.
func (*Conn) Write([]byte) (int, error) { return 0, ErrUnsupported }

// Close is unused outside Windows.
func (*Conn) Close() error { return nil }

// Notes is unused outside Windows.
func (*Conn) Notes() []string { return nil }

// Path is unused outside Windows.
func (*Conn) Path() string { return "" }
