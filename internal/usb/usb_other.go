//go:build !windows

package usb

import "dji-mic-rx/internal/duml"

// Scan and the rest are Windows-only: the driver this app installs, and the
// device interface it opens, do not exist elsewhere. The app still starts so
// it can explain that, rather than failing silently.

// Scan reports that there is nothing to scan on this platform.
func Scan() (Status, error) { return Status{}, ErrUnsupported }

// Open reports that the platform cannot open the receiver.
func Open(Info) (*Conn, error) { return nil, ErrUnsupported }

// EmbeddedPackage returns no package outside Windows.
func EmbeddedPackage() (map[string][]byte, string, bool) { return nil, "", false }

// HasEmbeddedPackage reports that there is nothing to import elsewhere.
func HasEmbeddedPackage() bool { return false }

// InstallEmbeddedPackage reports that driver installation is Windows-only.
func InstallEmbeddedPackage(string, func(string)) (InstallResult, error) {
	return InstallResult{}, ErrUnsupported
}

// SignedInstallAvailable reports that on-the-fly signing is Windows-only.
func SignedInstallAvailable() bool { return false }

// SDKTools reports that the Windows SDK tools are not here to be found.
func SDKTools() (string, string, bool) { return "", "", false }

// InstallDriver reports that driver installation is Windows-only.
func InstallDriver(duml.Model, string, func(string)) (InstallResult, error) {
	return InstallResult{}, ErrUnsupported
}

// StartDetached reports that launching a helper is Windows-only.
func StartDetached(string) error { return ErrUnsupported }

// UnsignedHint is empty outside Windows.
func UnsignedHint([]string) string { return "" }

// InstallWithWdiSimple reports that driver installation is Windows-only.
func InstallWithWdiSimple(duml.Model, string, string, func(string)) (InstallResult, error) {
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

// FindWdiSimple finds nothing outside Windows.
func FindWdiSimple() (string, bool) { return "", false }

// FindZadig finds nothing outside Windows.
func FindZadig() (string, bool) { return "", false }

// DriverPackages lists nothing outside Windows.
func DriverPackages() ([]DriverPackage, error) { return nil, ErrUnsupported }

// WriteInf is Windows-only.
func WriteInf(duml.Model, string) (string, error) { return "", ErrUnsupported }

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
