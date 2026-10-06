//go:build windows

package usb

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// embeddedPackage carries the driver package signed before the build, so the
// app only has to import it. The directory holds whatever scripts\build.cmd
// put there:
//
//	dji_mic_rx_control.inf  the package
//	dji_mic_rx_control.cat  its catalog, signed at build time
//	dji_mic_rx_control.cer  optional: the public half of the signing
//	                        certificate, present when the package was signed
//	                        with the app's own self-signed certificate. When
//	                        it is there the installer trusts it on the target
//	                        machine, which is an in-box certutil call. A
//	                        package signed by a public authority carries no
//	                        .cer and asks nothing of the machine.
//
//go:embed package
var embeddedPackage embed.FS

// packageDir is where the embedded files live inside the module.
const packageDir = "package"

// EmbeddedPackage returns the files of the signed package, keyed by file name,
// and its INF. ok is false when no package was embedded at build time.
func EmbeddedPackage() (files map[string][]byte, infName string, ok bool) {
	entries, err := fs.ReadDir(embeddedPackage, packageDir)
	if err != nil {
		return nil, "", false
	}
	files = map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch strings.ToLower(filepath.Ext(name)) {
		case ".inf", ".cat", ".cer":
		default:
			continue
		}
		data, err := embeddedPackage.ReadFile(packageDir + "/" + name)
		if err != nil {
			continue
		}
		files[name] = data
		if strings.EqualFold(filepath.Ext(name), ".inf") {
			infName = name
		}
	}
	// A package without a catalog is an unsigned package, which Windows
	// refuses; treat it as absent.
	hasCatalog := false
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".cat") {
			hasCatalog = true
		}
	}
	if infName == "" || !hasCatalog {
		return nil, "", false
	}
	return files, infName, true
}

// HasEmbeddedPackage reports whether this build carries a signed package.
func HasEmbeddedPackage() bool {
	_, _, ok := EmbeddedPackage()
	return ok
}

// InstallEmbeddedPackage extracts the signed package to a temporary directory
// and imports it, elevated. It touches nobody's trust store: the package was
// signed before it was shipped.
func InstallEmbeddedPackage(logDir string, progress func(string)) (InstallResult, error) {
	files, infName, ok := EmbeddedPackage()
	if !ok {
		return InstallResult{}, errors.New("这个版本里没有内置已签名的驱动包")
	}
	dir, err := os.MkdirTemp("", "dji-mic-rx-package-")
	if err != nil {
		return InstallResult{}, err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return InstallResult{}, err
		}
	}
	if progress != nil {
		progress("使用构建时签名的驱动包：" + infName)
	}
	return runElevated([]string{flagPackageInstall, dir, infName}, logDir, progress)
}

// installEmbeddedPackage is the elevated half: trust the signing certificate
// when the package carries one, import the package, then ask Windows to look
// at the device again, since a node that failed to install earlier keeps that
// state until it is re-enumerated.
func installEmbeddedPackage(dir, infName string, logf func(string, ...any)) int {
	infPath := filepath.Join(dir, infName)

	if cer, err := filepath.Glob(filepath.Join(dir, "*.cer")); err == nil {
		// A self-signed package: the machine has to trust the certificate
		// that signed it. This is the only system-wide change the install
		// makes, and uninstalling the driver takes it back out.
		for _, path := range cer {
			logf("这个包用自签名证书签名，先把证书装进本机信任：%s", filepath.Base(path))
			for _, store := range []string{"Root", "TrustedPublisher"} {
				if _, err := runCaptured("certutil", []string{"-addstore", "-f", store, path}, logf); err != nil {
					logf("把证书加入 %s 失败：%v", store, err)
					return 1
				}
			}
		}
	}

	logf("导入驱动包：%s", infPath)
	if _, err := runCaptured("pnputil", []string{"/add-driver", infPath, "/install"}, logf); err != nil {
		logf("导入失败：%v", err)
		return 1
	}
	runCaptured("pnputil", []string{"/scan-devices"}, logf)
	logf("驱动包已安装。若界面仍未连上，请重新插拔接收器。")
	return 0
}
