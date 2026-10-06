//go:build windows

package usb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"dji-mic-rx/internal/duml"
)

// Signing a driver package the way libwdi does, so that Windows accepts it:
//
//  1. a self-signed code signing certificate, created once and reused;
//  2. a catalog (.cat) built from the INF with the Windows SDK's makecat;
//  3. the catalog signed with that certificate using signtool;
//  4. the certificate trusted machine-wide, in Root and TrustedPublisher;
//  5. the package imported with pnputil, which binds WinUSB to the one
//     interface the INF names.
//
// Windows refuses an OEM INF that carries no signed catalog ("The third-party
// INF does not contain digital signature information"), which is why the plain
// INF the app can write on its own is not enough. The certificate is this
// app's own, not a public authority's: it is trusted because it is installed
// on this machine, and removing the driver removes it again.
const (
	// catalogName is the catalog inside the package, named after the INF so
	// Windows associates the two (the INF also names it explicitly).
	catalogName = "dji_mic_rx_control.cat"
	// certSubject identifies the self-signed certificate in the store.
	certSubject = "CN=DJI Mic Control (self-signed)"
	// pfxPassword protects the exported key only between two steps of the
	// same install, in a directory only this user can read.
	pfxPassword = "dji-mic-rx"
)

// SDK tool names the signed install needs. makecat builds the catalog and
// signtool signs it; both come from the Windows SDK, which is not part of
// Windows itself, so their absence is reported rather than hidden.
const (
	makecatName  = "makecat.exe"
	signtoolName = "signtool.exe"
)

// SDKTools locates the Windows SDK's catalog tools, preferring the newest
// version installed. It returns false when either is missing.
func SDKTools() (makecat, signtool string, ok bool) {
	makecat, okMake := sdkTool(makecatName)
	signtool, okSign := sdkTool(signtoolName)
	return makecat, signtool, okMake && okSign
}

// sdkTool looks for one tool under the Windows Kits bin directories, newest
// version first, and then in PATH.
func sdkTool(name string) (string, bool) {
	roots := []string{
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramFiles"),
	}
	var candidates []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		versions, err := filepath.Glob(filepath.Join(root, "Windows Kits", "10", "bin", "*", "x64", name))
		if err != nil {
			continue
		}
		candidates = append(candidates, versions...)
	}
	// Newest version directory last, so reverse the order of what was found.
	for i := len(candidates) - 1; i >= 0; i-- {
		if fileExists(candidates[i]) {
			return candidates[i], true
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	return "", false
}

// cdfText is the catalog definition file makecat reads: the name of the
// catalog to write, and the files it covers. The INF is named after the
// catalog, as the package layout requires.
func cdfText() string {
	return fmt.Sprintf(`[CatalogHeader]
Name=%s
PublicVersion=0x0000001
EncodingType=0x00010001
CATATTR1=0x10010001:OSAttr:2:6.1,6.2,6.3,6.4,10.0

[CatalogFiles]
<hash>%s=%s
`, catalogName, infFileName, infFileName)
}

// certificateScript is the PowerShell that creates the self-signed code
// signing certificate if the machine does not have one yet, and exports both
// its public part and a password-protected copy with the key.
//
// Windows PowerShell (not PowerShell 7) is used because the certificate
// cmdlets live in its PKI module, which ships with Windows.
func certificateScript() string {
	return `param([Parameter(Mandatory=$true)][string]$WorkDir)
$ErrorActionPreference = 'Stop'
$subject = '` + certSubject + `'
$cert = Get-ChildItem Cert:\LocalMachine\My |
        Where-Object { $_.Subject -eq $subject -and $_.HasPrivateKey } |
        Sort-Object NotAfter -Descending | Select-Object -First 1
if (-not $cert) {
  $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject $subject ` +
		`-CertStoreLocation Cert:\LocalMachine\My -KeyExportPolicy Exportable ` +
		`-KeyLength 2048 -KeyAlgorithm RSA -HashAlgorithm SHA256 ` +
		`-NotAfter (Get-Date).AddYears(10)
}
Export-Certificate -Cert $cert -FilePath (Join-Path $WorkDir 'package.cer') -Force | Out-Null
$secure = ConvertTo-SecureString -String '` + pfxPassword + `' -AsPlainText -Force
Export-PfxCertificate -Cert $cert -FilePath (Join-Path $WorkDir 'package.pfx') -Password $secure -Force | Out-Null
Write-Output $cert.Thumbprint
Write-Output $cert.Subject
`
}

// windowsPowerShell is Windows PowerShell itself, whose PKI module has the
// certificate cmdlets. PowerShell 7 does not ship it.
func windowsPowerShell() (string, bool) {
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if fileExists(path) {
		return path, true
	}
	return "", false
}

// InstallSignedDriver writes the package, signs it, trusts the certificate and
// imports the package. It runs elevated: every step touches the driver store,
// the certificate stores, or both.
func InstallSignedDriver(m duml.Model, logDir string, progress func(string)) (InstallResult, error) {
	dir, err := os.MkdirTemp("", "dji-mic-rx-package-")
	if err != nil {
		return InstallResult{}, err
	}
	if _, err := WriteInf(m, dir); err != nil {
		return InstallResult{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "package.cdf"), []byte(cdfText()), 0o644); err != nil {
		return InstallResult{}, err
	}
	script := filepath.Join(dir, "certificate.ps1")
	if err := os.WriteFile(script, []byte(certificateScript()), 0o644); err != nil {
		return InstallResult{}, err
	}
	if progress != nil {
		progress("驱动包目录：" + dir)
	}
	return runElevated([]string{flagSignedInstall, dir}, logDir, progress)
}

// signAndInstall is the elevated half: everything from the certificate to the
// imported package, with each step's output kept.
func signAndInstall(dir string, logf func(string, ...any)) int {
	makecat, signtool, ok := SDKTools()
	if !ok {
		logf("找不到 Windows SDK 的 %s / %s，无法给驱动包签名。", makecatName, signtoolName)
		logf("请安装 Windows SDK 后重试，或改用 Zadig（它的包自带签名目录）。")
		return 1
	}
	infPath := filepath.Join(dir, infFileName)
	cdfPath := filepath.Join(dir, "package.cdf")
	catPath := filepath.Join(dir, catalogName)
	cerPath := filepath.Join(dir, "package.cer")
	pfxPath := filepath.Join(dir, "package.pfx")
	scriptPath := filepath.Join(dir, "certificate.ps1")

	logf("== 1/5 准备自签名证书")
	if pwsh, ok := windowsPowerShell(); ok {
		if _, err := runCaptured(pwsh, []string{
			"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-WorkDir", dir,
		}, logf); err != nil {
			logf("创建证书失败：%v", err)
			return 1
		}
	} else {
		logf("找不到 Windows PowerShell，无法创建证书")
		return 1
	}

	logf("== 2/5 生成目录文件（makecat）")
	// makecat resolves the member files named in the CDF against its own
	// working directory, so it has to run inside the package directory.
	if _, err := runCapturedIn(dir, makecat, []string{"-v", cdfPath}, logf); err != nil {
		logf("makecat 失败：%v", err)
		return 1
	}
	if !fileExists(catPath) {
		logf("makecat 没有生成 %s", catalogName)
		return 1
	}

	logf("== 3/5 用证书签名目录文件（signtool）")
	if _, err := runCaptured(signtool, []string{
		"sign", "/v", "/fd", "sha256", "/f", pfxPath, "/p", pfxPassword, catPath,
	}, logf); err != nil {
		logf("signtool 失败：%v", err)
		return 1
	}

	logf("== 4/5 把证书装进受信任的根与发布者（机器范围）")
	for _, store := range []string{"Root", "TrustedPublisher"} {
		if _, err := runCaptured("certutil", []string{"-addstore", "-f", store, cerPath}, logf); err != nil {
			logf("把证书加入 %s 失败：%v", store, err)
			return 1
		}
	}

	logf("== 5/5 导入驱动包（pnputil）")
	if _, err := runCaptured("pnputil", []string{"/add-driver", infPath, "/install"}, logf); err != nil {
		logf("导入驱动包失败：%v", err)
		return 1
	}
	// A node that failed to install earlier stays in that state until it is
	// re-enumerated, so ask Windows to scan again.
	runCaptured("pnputil", []string{"/scan-devices"}, logf)
	logf("驱动包已安装并绑定到接口。若界面仍未连上，请重新插拔接收器。")
	return 0
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

// runCaptured runs a helper and returns its output, one line at a time, with a
// timeout so a tool that waits for input cannot hang the install.
func runCaptured(name string, args []string, logf func(string, ...any)) (string, error) {
	return runCapturedIn("", name, args, logf)
}

// runCapturedIn runs a helper in a given working directory.
func runCapturedIn(dir, name string, args []string, logf func(string, ...any)) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// The window is a GUI program with no console, so a helper started
	// without CREATE_NO_WINDOW would flash a console window on screen.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	out, err := cmd.CombinedOutput()
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimRight(line, " \t"); line != "" {
			logf("   %s", line)
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

// SignedInstallAvailable reports whether this machine can sign a package the
// app writes itself, which needs the Windows SDK tools.
func SignedInstallAvailable() bool {
	_, _, ok := SDKTools()
	return ok
}

// signedInstallTimeout is how long any one signing step may take.
const signedInstallTimeout = 5 * time.Minute

var _ = windows.Handle(0)
var _ = signedInstallTimeout
