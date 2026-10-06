//go:build windows

package usb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"dji-mic-rx/internal/duml"
)

// The driver the app installs is Microsoft's in-box WinUSB bound to one
// interface, described by an INF this app writes. The names below appear in
// Windows' own listings, so they say what the package is for rather than
// borrowing the vendor's name.
const (
	infFileName  = "dji_mic_rx_control.inf"
	providerName = "DJI Mic Control"
	deviceName   = "DJI Mic Receiver control interface"
)

// Our own device interface GUID, registered by the INF so the app can find
// this one interface. The standard USB device interface GUID is registered
// alongside it, so tools that expect a USB device interface keep working.
const (
	controlInterfaceGUID = "{c4f1c6a9-1f4e-4b2c-9f7b-0d5c2a7e4b31}"
	usbDeviceGUIDString  = "{a5dcbf10-6530-11d2-901f-00c04fb951ed}"
)

// Helper flags: the elevated half of an install runs this same program again
// with one of these, so that everything privileged happens in one place.
const (
	flagInstall        = "--driver-install"
	flagWdiInstall     = "--driver-install-wdi"
	flagSignedInstall  = "--driver-install-signed"
	flagPackageInstall = "--driver-install-package"
	flagUninstall      = "--driver-uninstall"
	flagLog            = "--log"
)

// infText renders the INF that binds WinUSB to a receiver's control
// interface. It includes Microsoft's in-box winusb.inf sections and adds the
// device interface GUIDs itself, which the in-box template deliberately
// leaves to the external INF.
func infText(m duml.Model) string {
	hardwareID := fmt.Sprintf("USB\\VID_%04X&PID_%04X&MI_%02X", m.Vendor, m.Product, m.Interface)
	return fmt.Sprintf(`; %s
; Written by DJI Mic 接收器控制台 to install Microsoft's in-box WinUSB driver
; on the receiver's vendor interface only. The device's audio and HID
; interfaces keep their system drivers, so recording is unaffected.
;
;   %s  %s

[Version]
Signature   = "$Windows NT$"
Class       = USBDevice
ClassGuid   = {88BAE032-5A81-49f0-BC3D-A4FF138216D6}
Provider    = %%ProviderName%%
CatalogFile = %s
DriverVer   = 01/01/2026,1.0.0.0

[Manufacturer]
%%ProviderName%% = DJIMic, NTamd64, NTarm64

[DJIMic.NTamd64]
%%DeviceName%% = DJIMic_Install, %s

[DJIMic.NTarm64]
%%DeviceName%% = DJIMic_Install, %s

[DJIMic_Install]
Include = winusb.inf
Needs   = WINUSB.NT

[DJIMic_Install.HW]
Include = winusb.inf
Needs   = WINUSB.NT.HW
AddReg  = DJIMic_AddReg

[DJIMic_AddReg]
HKR,,DeviceInterfaceGUIDs,0x10000,"%s","%s"

[DJIMic_Install.Services]
Include = winusb.inf
Needs   = WINUSB.NT.Services

[Strings]
ProviderName = "%s"
DeviceName   = "%s"
`, m.Name, m.Name, hardwareID, hardwareID, hardwareID, controlInterfaceGUID, usbDeviceGUIDString, catalogName, providerName, deviceName)
}

// DefaultModel is the receiver family this build targets, used when no device
// is plugged in to identify itself — the build script needs it to write the
// INF, for instance.
func DefaultModel() duml.Model {
	if model, ok := duml.ModelFor(0x2ca3, 0x4011); ok {
		return model
	}
	return duml.Model{}
}

// WriteInf writes the INF for a model into dir and returns its path.
func WriteInf(m duml.Model, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, infFileName)
	if err := os.WriteFile(path, []byte(infText(m)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// IsElevated reports whether this process already has administrator rights.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// InstallResult is what an elevated install reported.
type InstallResult struct {
	// Log is the helper's output, line by line.
	Log []string
	// LogPath is the file the helper wrote that output to. It is kept after
	// the run: the time it matters is the time an install failed and
	// somebody has to read why.
	LogPath string
	// ExitCode is the helper's exit code.
	ExitCode uint32
}

// ErrCanceled reports that the user dismissed the administrator prompt.
var ErrCanceled = errors.New("已取消管理员授权")

// InstallDriver installs WinUSB on the model's control interface. It writes
// the INF, then runs this program again with administrator rights, because
// Windows only lets an administrator touch the driver store. When a
// wdi-simple.exe is available the helper uses it instead of pnputil, since its
// driver packages carry a catalog that Windows accepts.
// The installer's output is kept in logDir, under a name no later run
// overwrites.
func InstallDriver(m duml.Model, logDir string, progress func(string)) (InstallResult, error) {
	dir, err := os.MkdirTemp("", "dji-mic-rx-driver-")
	if err != nil {
		return InstallResult{}, err
	}
	infPath, err := WriteInf(m, dir)
	if err != nil {
		return InstallResult{}, err
	}
	if progress != nil {
		progress("驱动配置文件：" + infPath)
	}

	if wdi, ok := FindWdiSimple(); ok {
		if progress != nil {
			progress("找到 wdi-simple.exe，用它安装：" + wdi)
		}
		return runElevated([]string{flagWdiInstall, wdi}, logDir, progress)
	}
	if progress != nil {
		progress("未找到 wdi-simple.exe，改用 Windows 自带的 pnputil 安装驱动包：" + infPath)
	}
	return runElevated([]string{flagInstall, infPath}, logDir, progress)
}

// UninstallDriver removes the driver package bound to a device node. The
// package name is the one Windows publishes, which is also what pnputil
// accepts back.
func UninstallDriver(info Info, logDir string, progress func(string)) (InstallResult, error) {
	inf, err := publishedInfName(info)
	if err != nil {
		return InstallResult{}, err
	}
	if progress != nil {
		progress("移除驱动包：" + inf)
	}
	return runElevated([]string{flagUninstall, inf}, logDir, progress)
}

// publishedInfName is the name pnputil knows a driver package by, such as
// "oem123.inf". Windows publishes a package under a name of its own, which is
// what has to be handed back to remove it.
//
// The driver store's own listing is the source of truth here: it names the
// package by its original file name, so a package this app installed and one
// libwdi installed are both found, whatever the registry calls them.
func publishedInfName(info Info) (string, error) {
	if packages, err := DriverPackages(); err == nil {
		for _, p := range packages {
			name := strings.ToLower(p.Original)
			switch {
			case strings.Contains(name, "dji_mic_rx_control"),
				strings.Contains(name, "wireless_mic_rx"),
				strings.Contains(name, "dji_mic_control"):
				if p.Published != "" {
					return p.Published, nil
				}
			}
		}
	}
	if info.Driver.InfPath != "" {
		return info.Driver.InfPath, nil
	}
	return "", errors.New("驱动库里没有找到接收器的驱动包（可能已经卸载）")
}

// runElevated runs this program again as administrator and streams its log.
func runElevated(args []string, logDir string, progress func(string)) (InstallResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return InstallResult{}, err
	}
	logPath, err := newLogPath(logDir)
	if err != nil {
		return InstallResult{}, err
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		return InstallResult{}, err
	}
	if progress != nil {
		progress("安装日志：" + logPath)
	}

	full := append([]string{}, args...)
	full = append(full, flagLog, logPath)

	// The log file is the only channel back from an elevated process, so it
	// is polled while the helper runs and once more when it exits.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var lastSize int
	var mu sync.Mutex
	emit := func(lines []string) {
		if progress == nil {
			return
		}
		for _, line := range lines {
			progress(line)
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
				mu.Lock()
				lines, size := tailFrom(logPath, lastSize)
				lastSize = size
				mu.Unlock()
				emit(lines)
			}
		}
	}()

	code, err := shellExecuteWait(exe, full)
	close(stop)
	wg.Wait()
	mu.Lock()
	lines, _ := tailFrom(logPath, 0)
	mu.Unlock()

	res := InstallResult{Log: lines, ExitCode: code, LogPath: logPath}
	if err != nil {
		return res, err
	}
	if code != 0 {
		return res, fmt.Errorf("驱动工具以退出码 %d 结束", code)
	}
	return res, nil
}

// newLogPath names a log file that the next attempt will not overwrite, so the
// record of a failure outlives the window that showed it.
func newLogPath(dir string) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "logs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "driver-"+time.Now().Format("20060102-150405")+".log"), nil
}

// tailFrom reads the lines of a file past offset, returning the new offset.
func tailFrom(path string, offset int) ([]string, int) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < offset {
		return nil, offset
	}
	text := string(data[offset:])
	lines := splitLines(text)
	return lines, len(data)
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line = strings.TrimRight(line, " \t"); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// shellExecuteInfo mirrors SHELLEXECUTEINFOW. The field order and the padding
// after dwHotKey are what Windows expects, so the struct is spelled out
// rather than built from a helper library.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     uintptr
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskFlagNoUI       = 0x00000400
	swShownormal          = 1
	errorCancelled        = 1223
)

var (
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

// shellExecuteWait starts a program as administrator and waits for it, since
// installing a driver cannot be done without one.
func shellExecuteWait(file string, args []string) (uint32, error) {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 0, err
	}
	filePtr, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return 0, err
	}
	params, err := windows.UTF16PtrFromString(quoteArgs(args))
	if err != nil {
		return 0, err
	}

	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       filePtr,
		lpParameters: params,
		nShow:        swShownormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	r1, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		if errno, ok := callErr.(windows.Errno); ok && errno == errorCancelled {
			return 0, ErrCanceled
		}
		return 0, fmt.Errorf("以管理员身份启动失败: %w", callErr)
	}
	if info.hProcess == 0 {
		return 0, errors.New("没有拿到提权进程的句柄")
	}
	defer windows.CloseHandle(windows.Handle(info.hProcess))

	if _, err := windows.WaitForSingleObject(windows.Handle(info.hProcess), windows.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(windows.Handle(info.hProcess), &code); err != nil {
		return 0, err
	}
	return code, nil
}

// quoteArgs quotes arguments for a command line, as CommandLineToArgvW reads
// them back.
func quoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, quoteArg(a))
	}
	return strings.Join(quoted, " ")
}

func quoteArg(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"\\") {
		return a
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range a {
		switch r {
		case '\\':
			backslashes++
			b.WriteRune(r)
		case '"':
			b.WriteString(strings.Repeat("\\", backslashes+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			backslashes = 0
			b.WriteRune(r)
		}
	}
	b.WriteString(strings.Repeat("\\", backslashes))
	b.WriteByte('"')
	return b.String()
}

// RunHelper is the elevated half of an install or uninstall. It returns the
// exit code the parent process checks, and writes its output to the log file
// named on the command line, since an elevated process has no console to
// print to.
func RunHelper(args []string) int {
	logPath := argValue(args, flagLog)
	var logMu sync.Mutex
	logf := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		logMu.Lock()
		defer logMu.Unlock()
		if logPath == "" {
			return
		}
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, line)
	}

	logf("以管理员身份运行：%v", args)
	switch {
	case hasFlag(args, flagPackageInstall):
		dir := argValue(args, flagPackageInstall)
		// The INF name follows the directory.
		infName := infFileName
		for i, a := range args {
			if a == flagPackageInstall && i+2 < len(args) {
				infName = args[i+2]
			}
		}
		return installEmbeddedPackage(dir, infName, logf)

	case hasFlag(args, flagSignedInstall):
		dir := argValue(args, flagSignedInstall)
		logf("给程序自己写的驱动包签名后再安装（libwdi/Zadig 的做法）")
		return signAndInstall(dir, logf)

	case hasFlag(args, flagWdiInstall):
		tool := argValue(args, flagWdiInstall)
		logf("调用 wdi-simple.exe 把 WinUSB 绑定到接口 6：%s", tool)
		if err := runStreaming(tool, []string{
			"-n", providerName, "-m", "DJI",
			"-v", "0x2ca3", "-p", "0x4011", "-i", "6", "-t", "0", "-s",
		}, logf); err != nil {
			logf("wdi-simple.exe 失败：%v", err)
			return 1
		}
		logf("wdi-simple.exe 完成。请重新插拔接收器。")
		return 0

	case hasFlag(args, flagInstall):
		inf := argValue(args, flagInstall)
		logf("安装驱动包：%s", inf)
		if err := runStreaming("pnputil", []string{"/add-driver", inf, "/install"}, logf); err != nil {
			logf("pnputil 失败：%v", err)
			logf("如果失败原因是签名或证书，请改用 Zadig（选择 Wireless Mic Rx 的 Interface 6，驱动选 WinUSB）安装。")
			return 1
		}
		logf("驱动包安装完成。若接口仍未出现，请重新插拔接收器。")
		return 0

	case hasFlag(args, flagUninstall):
		oem := argValue(args, flagUninstall)
		logf("移除驱动包：%s", oem)
		if err := runStreaming("pnputil", []string{"/delete-driver", oem, "/uninstall", "/force"}, logf); err != nil {
			logf("pnputil 失败：%v", err)
			return 1
		}
		// The certificate was installed for this package alone, so it goes
		// when the package goes.
		RemoveTrustedCertificate(logf)
		logf("驱动包与自签名证书已移除。请重新插拔接收器，让 Windows 回到默认驱动。")
		return 0
	}
	logf("没有可执行的操作")
	return 1
}

// runStreaming runs a command and appends each line of its output to logf.
// runStreaming runs a command and appends each line of its output to logf.
//
// The window is a GUI program with no console of its own, so a helper started
// without CREATE_NO_WINDOW makes Windows flash a console window: a black box
// that appears and vanishes, which looks like a crash rather than an install.
func runStreaming(name string, args []string, logf func(string, ...any)) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	out, err := cmd.CombinedOutput()
	for _, line := range splitLines(string(out)) {
		logf("  %s", line)
	}
	return err
}

// createNoWindow is CREATE_NO_WINDOW: run the child without a console window.
const createNoWindow = 0x08000000

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// argValue returns the value that follows a flag.
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// StartDetached opens a program the user asked for and returns at once.
func StartDetached(path string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		fMask:  seeMaskFlagNoUI,
		lpFile: file,
		nShow:  swShownormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r1, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return fmt.Errorf("启动 %s 失败: %w", path, callErr)
	}
	return nil
}

// InstallWithWdiSimple installs the driver with a wdi-simple.exe the user
// supplied. libwdi's packages carry a catalog Windows accepts, which is why
// the project's notes recommend this tool over writing an INF by hand.
func InstallWithWdiSimple(m duml.Model, tool string, logDir string, progress func(string)) (InstallResult, error) {
	if _, err := os.Stat(tool); err != nil {
		return InstallResult{}, fmt.Errorf("找不到 %s: %w", tool, err)
	}
	if progress != nil {
		progress("用 wdi-simple.exe 安装：" + tool)
	}
	return runElevated([]string{flagWdiInstall, tool}, logDir, progress)
}

// UnsignedHint explains a driver install that Windows refused for want of a
// signature, which is the usual outcome for a package this app writes itself:
// Windows only accepts third-party driver packages that carry a signed
// catalog, and building one needs a certificate the app does not have.
//
// It returns an empty string when the log does not look like that failure, so
// the caller can fall back to a plain report.
func UnsignedHint(log []string) string {
	markers := []string{
		"0xE000022F", "0xE0000247", "0x800B0100", "0x800B0109",
		"not signed", "unsigned", "签名",
	}
	for _, line := range log {
		lower := strings.ToLower(line)
		for _, marker := range markers {
			if strings.Contains(line, marker) || strings.Contains(lower, strings.ToLower(marker)) {
				return "Windows 拒绝了没有数字签名的驱动包。第三方驱动包必须有签名目录，程序自己生成的 INF 没有，所以这条路在本机走不通。\n" +
					"改用 Zadig（libwdi 的图形安装器，它的驱动包带目录签名）安装：选中 Wireless Mic Rx 的 Interface 6，驱动选 WinUSB，装完再拔插接收器。"
			}
		}
	}
	return ""
}

// DriverPackage is one entry of the driver store, as pnputil lists it.
type DriverPackage struct {
	Published string
	Original  string
	Provider  string
	Class     string
	Version   string
	Signer    string
}

// Signed reports whether the package carries a signature Windows names.
func (p DriverPackage) Signed() bool { return p.Signer != "" }

// DriverPackages lists the driver store. The labels pnputil prints are
// localized, so both the English and the Chinese wording are recognised.
func DriverPackages() ([]DriverPackage, error) {
	out, err := exec.Command("pnputil", "/enum-drivers").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pnputil /enum-drivers: %w", err)
	}
	var (
		packages []DriverPackage
		current  *DriverPackage
	)
	for _, line := range splitLines(string(out)) {
		key, value, ok := splitLabel(line)
		if !ok {
			continue
		}
		switch key {
		case "published name", "已发布的名称", "发布的名称":
			if current != nil {
				packages = append(packages, *current)
			}
			current = &DriverPackage{Published: value}
		case "original name", "原始名称":
			if current != nil {
				current.Original = value
			}
		case "provider name", "提供程序名称":
			if current != nil {
				current.Provider = value
			}
		case "class name", "类名称":
			if current != nil {
				current.Class = value
			}
		case "driver version", "驱动程序版本":
			if current != nil {
				current.Version = value
			}
		case "signer name", "签名者姓名", "签名者名称":
			if current != nil {
				current.Signer = value
			}
		}
	}
	if current != nil {
		packages = append(packages, *current)
	}
	return packages, nil
}

// splitLabel splits "Published Name:   oem42.inf" into its label in lower
// case and its value.
func splitLabel(line string) (key, value string, ok bool) {
	i := strings.IndexAny(line, ":：")
	if i < 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(line[:i]))
	return key, strings.TrimSpace(line[i+1:]), true
}

// FindWdiSimple looks for libwdi's command line installer: the tool the
// project's notes recommend, because its packages carry a catalog Windows
// accepts without further work. It is never bundled; the user supplies it.
func FindWdiSimple() (string, bool) { return findTool("wdi-simple.exe", "wdi-simple*.exe") }

// FindZadig looks for Zadig, libwdi's graphical installer, which the user may
// already have from installing the driver by hand.
func FindZadig() (string, bool) { return findTool("zadig.exe", "zadig*.exe") }

// findTool searches the program's own directory, the usual places a
// downloaded tool ends up, the program files directories and PATH.
func findTool(exact, pattern string) (string, bool) {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "assets", "bin"))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd, filepath.Join(wd, "assets", "bin"))
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "OneDrive", "Downloads"),
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "DjiMicRx", "bin"),
	} {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) > 0 {
		// The app's own directory first, so a bundled tool wins.
		for _, dir := range dirs {
			candidate := filepath.Join(dir, exact)
			if fileExists(candidate) {
				return candidate, true
			}
		}
		for _, dir := range dirs {
			matches, err := filepath.Glob(filepath.Join(dir, pattern))
			if err != nil || len(matches) == 0 {
				continue
			}
			sort.Strings(matches)
			for _, match := range matches {
				if fileExists(match) {
					return match, true
				}
			}
		}
	}
	if path, err := exec.LookPath(exact); err == nil {
		return path, true
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// FormatDeviceList renders the driver store entries for the app's
// diagnostics, most recent first.
func FormatDeviceList(packages []DriverPackage) []string {
	out := make([]string, 0, len(packages))
	for _, p := range packages {
		signer := p.Signer
		if signer == "" {
			signer = "未签名"
		}
		out = append(out, fmt.Sprintf("%s ← %s · %s · %s · %s",
			p.Published, p.Original, p.Provider, p.Class, signer))
	}
	return out
}
