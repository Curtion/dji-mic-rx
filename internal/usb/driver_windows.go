//go:build windows

package usb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Helper flags: the elevated half of an install runs this same program again
// with one of these, so that everything privileged happens in one place.
const (
	flagPackageInstall = "--driver-install-package"
	flagUninstall      = "--driver-uninstall"
	flagLog            = "--log"
)

// certSubject identifies the self-signed certificate in the store, for the
// case the embedded package was signed with the app's own certificate.
const certSubject = "CN=DJI Mic Control (self-signed)"

// IsElevated reports whether this process already has administrator rights.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// InstallResult is what an elevated install reported.
type InstallResult struct {
	// Log is the helper's output, line by line.
	Log []string
	// LogPath is the file the helper wrote that output to.
	LogPath string
	// ExitCode is the helper's exit code.
	ExitCode uint32
}

// ErrCanceled reports that the user dismissed the administrator prompt.
var ErrCanceled = errors.New("已取消管理员授权")

// UninstallDriver removes the driver package bound to a device node. The
// published package name (oemNN.inf) is what the scan already read from the
// registry, which is also what pnputil accepts back.
func UninstallDriver(info Info, logDir string, progress func(string)) (InstallResult, error) {
	if info.Driver.InfPath == "" {
		return InstallResult{}, errors.New("节点上没有读到驱动包名称（可能已经卸载）")
	}
	if progress != nil {
		progress("移除驱动包：" + info.Driver.InfPath)
	}
	return runElevated([]string{flagUninstall, info.Driver.InfPath}, logDir, progress)
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

	full := append([]string{}, args...)
	full = append(full, flagLog, logPath)

	// The log file is the only channel back from an elevated process, so it
	// is polled while the helper runs and once more when it exits.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var lastSize int
	var mu sync.Mutex
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
				if progress != nil {
					for _, line := range lines {
						progress(line)
					}
				}
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

// newLogPath names the log file the elevated helper writes to.
func newLogPath(dir string) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "driver-"+time.Now().Format("20060102-150405")+".log"), nil
}

// tailFrom reads the lines of a file past offset, returning the new offset.
func tailFrom(path string, offset int) ([]string, int) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < offset {
		return nil, offset
	}
	return splitLines(string(data[offset:])), len(data)
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
		infName := ""
		for i, a := range args {
			if a == flagPackageInstall && i+2 < len(args) {
				infName = args[i+2]
			}
		}
		return installEmbeddedPackage(dir, infName, logf)

	case hasFlag(args, flagUninstall):
		oem := argValue(args, flagUninstall)
		logf("移除驱动包：%s", oem)
		if _, err := runCaptured("pnputil", []string{"/delete-driver", oem, "/uninstall", "/force"}, logf); err != nil {
			logf("pnputil 失败：%v", err)
			return 1
		}
		// The certificate was installed for this package alone, so it goes
		// when the package goes.
		RemoveTrustedCertificate(logf)
		logf("驱动包已移除。请重新插拔接收器，让 Windows 回到默认驱动。")
		return 0
	}
	logf("没有可执行的操作")
	return 1
}

// runCaptured runs a helper and passes its output to logf, one line at a
// time. The window is a GUI program with no console, so a helper started
// without CREATE_NO_WINDOW would flash a console window on screen.
func runCaptured(name string, args []string, logf func(string, ...any)) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	out, err := cmd.CombinedOutput()
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimRight(line, " \t"); line != "" {
			logf("  %s", line)
		}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return text, fmt.Errorf("%s 退出码 %d", filepath.Base(name), exitErr.ExitCode())
		}
		return text, err
	}
	return text, nil
}

// RemoveTrustedCertificate takes the self-signed certificate back out of the
// machine's trust stores, for symmetry with installing it. It is best effort:
// a certificate that is not there is not an error.
func RemoveTrustedCertificate(logf func(string, ...any)) {
	for _, store := range []string{"Root", "TrustedPublisher"} {
		if _, err := runCaptured("certutil", []string{"-delstore", store, certSubject}, logf); err != nil {
			logf("从 %s 移除证书时报告：%v", store, err)
		}
	}
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
