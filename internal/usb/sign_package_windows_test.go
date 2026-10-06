//go:build windows

package usb

import (
	"strings"
	"testing"
)

func TestEmbeddedPackageINF(t *testing.T) {
	files, infName, ok := EmbeddedPackage()
	if !ok {
		t.Fatal("missing embedded driver package")
	}
	sections := map[string][]string{}
	section := ""
	for _, line := range strings.Split(string(files[infName]), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		sections[section] = append(sections[section], strings.ReplaceAll(line, " ", ""))
	}
	checks := map[string]string{
		"[Version]":        "CatalogFile=dji_mic_rx_control.cat",
		"[DJIMic.NTamd64]": `%DeviceName%=DJIMic_Install,USB\VID_2CA3&PID_4011&MI_06`,
		"[DJIMic.NTarm64]": `%DeviceName%=DJIMic_Install,USB\VID_2CA3&PID_4011&MI_06`,
	}
	guids := make([]string, len(candidateInterfaceGUIDs))
	for i, guid := range candidateInterfaceGUIDs {
		guids[i] = `"` + guid.String() + `"`
	}
	checks["[DJIMic_AddReg]"] = "HKR,,DeviceInterfaceGUIDs,0x10000," + strings.Join(guids, ",")
	for section, want := range checks {
		found := false
		for _, line := range sections[section] {
			if strings.EqualFold(line, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing %s; got %v", section, want, sections[section])
		}
	}
	if len(files["dji_mic_rx_control.cat"]) == 0 {
		t.Error("referenced catalog is missing or empty")
	}
}
