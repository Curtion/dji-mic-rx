// Command djiprobe reports what the app can see of a DJI receiver: which
// device nodes exist, which driver each one has, and, when the vendor
// interface is openable, the status frames it pushes.
//
// It is the hardware half of the app's diagnostics: the same code paths the
// window uses, printed to a terminal.
//
//	go run ./cmd/djiprobe           # scan and read for 5 seconds
//	go run ./cmd/djiprobe -t 15s    # read longer
//	go run ./cmd/djiprobe -scan     # only report the device tree
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/usb"
)

func main() {
	var (
		readFor  = flag.Duration("t", 5*time.Second, "how long to read status frames")
		onlyScan = flag.Bool("scan", false, "only report the device tree")
		set      = flag.String("set", "", "after reading, send one setting as id=value and wait for its acknowledgement")
		install  = flag.Bool("install-driver", false, "install the WinUSB driver (asks for administrator rights) and print the log")
		uninst   = flag.Bool("uninstall-driver", false, "remove the WinUSB driver package and print the log")
	)
	flag.Parse()

	status, err := usb.Scan()
	if err != nil {
		fmt.Fprintln(os.Stderr, "扫描失败:", err)
		os.Exit(1)
	}

	fmt.Printf("扫描时间 %s\n", status.Scanned.Format("15:04:05"))
	fmt.Printf("已知接收器节点 %d 个\n", len(status.Devices))
	sort.Slice(status.Devices, func(i, j int) bool {
		return status.Devices[i].InstanceID < status.Devices[j].InstanceID
	})
	for _, d := range status.Devices {
		state := "缺失"
		if d.Present {
			state = "在线"
		}
		driver := "无驱动"
		if d.Driver.Service != "" {
			driver = d.Driver.Service
			if d.Driver.InfPath != "" {
				driver += " (" + d.Driver.InfPath + ")"
			}
			if d.Driver.Provider != "" {
				driver += " · " + d.Driver.Provider
			}
		}
		fmt.Printf("  %-52s %s  接口=%d  %s\n", d.InstanceID, state, d.Interface, driver)
		if d.Description != "" {
			fmt.Printf("      %s\n", d.Description)
		}
	}

	if model, ok := status.Model(); ok {
		fmt.Printf("型号表: %s（接口 %d，端点 0x%02x/0x%02x）\n",
			model.Name, model.Interface, model.BulkOut, model.BulkIn)
		if model.AltSetting != 0 {
			fmt.Printf("        注意：需要 alternate setting %d\n", model.AltSetting)
		}
		if !model.Verified {
			fmt.Printf("        注意：%s\n", model.Note)
		}
	} else {
		fmt.Println("型号表: 没有匹配的型号")
	}

	if status.Control == nil {
		fmt.Println("控制接口: 未出现（接口 " + fmt.Sprint(controlInterfaceOf(status)) + " 不在设备树上）")
		fmt.Println("提示: 装好驱动后请重新插拔接收器；若仍未出现，说明该固件在当前模式下没有暴露厂商接口。")
	} else {
		ready, why := status.Control.ReadyToOpen()
		fmt.Printf("控制接口: %s\n", status.Control.InstanceID)
		fmt.Printf("  可打开: %v   %s\n", ready, why)
	}

	if *onlyScan {
		return
	}

	// Driver work happens before opening the device: installing a driver
	// means the connection has to be made again afterwards anyway.
	if *install {
		if err := installDriver(); err != nil {
			os.Exit(1)
		}
		return
	}
	if *uninst {
		if status.Control == nil {
			fmt.Fprintln(os.Stderr, "厂商接口没有出现，没有可移除的驱动")
			os.Exit(1)
		}
		if err := uninstallDriver(*status.Control); err != nil {
			os.Exit(1)
		}
		return
	}

	if status.Control == nil {
		fmt.Println("跳过读取：控制接口不存在")
		return
	}
	if ready, why := status.Control.ReadyToOpen(); !ready {
		fmt.Println("跳过读取：" + why)
		return
	}

	conn, err := usb.Open(*status.Control)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开失败:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("已打开 %s\n", conn.Path())
	for _, note := range conn.Notes() {
		fmt.Println("  ", note)
	}

	state := duml.NewState()
	buf := make([]byte, 512)
	var pending []byte
	deadline := time.Now().Add(*readFor)
	frames, timeouts := 0, 0
	lastReport := time.Now()
	fmt.Println("开始读取状态帧…")

	for time.Now().Before(deadline) {
		n, err := conn.Read(buf)
		if err == usb.ErrTimeout {
			timeouts++
			continue
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取失败:", err)
			break
		}
		pending = append(pending, buf[:n]...)
		for {
			frame, rest := duml.TakeFrame(pending)
			if frame == nil {
				pending = rest
				break
			}
			pending = rest
			frames++
			if next, ok := duml.Decode(state, frame); ok {
				state = next
			} else if frames <= 3 {
				fmt.Printf("  未识别的帧（%d 字节）: % x\n", len(frame), frame)
			}
		}
		if time.Since(lastReport) > time.Second || frames <= 3 {
			report(state, frames, timeouts)
			lastReport = time.Now()
		}
	}

	fmt.Println("\n最终状态")
	report(state, frames, timeouts)

	if *set != "" {
		if err := sendSetting(conn, state, *set); err != nil {
			fmt.Fprintln(os.Stderr, "发送设置失败:", err)
			os.Exit(1)
		}
	}
}

// installDriver runs the same elevated install the window runs, and prints
// the installer's output so it can be read without the window open. It is the
// half of the diagnostics that needs administrator rights.
func installDriver() error {
	fmt.Println("开始安装 WinUSB 驱动（会弹一次管理员授权）…")
	result, err := usb.InstallEmbeddedPackage("", func(line string) {
		fmt.Println("  " + line)
	})
	for _, line := range result.Log {
		fmt.Println("  " + line)
	}
	if err != nil {
		if errors.Is(err, usb.ErrCanceled) {
			fmt.Fprintln(os.Stderr, "提权被取消或未获批准，驱动没有安装。")
			return err
		}
		fmt.Fprintf(os.Stderr, "安装失败：%v\n", err)
		return err
	}
	fmt.Println("安装完成。请重新插拔接收器，然后运行 -scan 或直接读状态。")
	return nil
}

// uninstallDriver removes the driver package bound to a node, printing the
// same output.
func uninstallDriver(node usb.Info) error {
	fmt.Println("开始移除驱动包（会弹一次管理员授权）…")
	result, err := usb.UninstallDriver(node, "", func(line string) {
		fmt.Println("  " + line)
	})
	for _, line := range result.Log {
		fmt.Println("  " + line)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "卸载失败：%v\n", err)
		return err
	}
	fmt.Println("驱动已移除。请重新插拔接收器。")
	return nil
}

// sendSetting writes one setting and waits for the receiver to acknowledge it,
// which is how a command is verified against real hardware without the window.
// Passing the value the device already reports makes the check harmless: the
// frame goes out, the acknowledgement proves it was accepted, and nothing
// about the receiver changes.
func sendSetting(conn *usb.Conn, state duml.State, spec string) error {
	id, value, ok := strings.Cut(spec, "=")
	if !ok {
		return fmt.Errorf("要用 id=value 的形式，例如 低切=on / low-cut=on")
	}
	setting, known := duml.SettingByID(id)
	if !known {
		return fmt.Errorf("未知设置 %q", id)
	}
	if reason := setting.ReadOnlyReason(state.Dialect, state.RX.Name); reason != "" {
		return fmt.Errorf("%s：%s", setting.Label, reason)
	}
	if setting.PerTransmitter {
		return fmt.Errorf("%s 需要指定发射器，请在控制台对应的发射器卡片中调整", setting.Label)
	}
	if !setting.Available(state.Dialect, state.RX.Name) {
		return fmt.Errorf("%s 在当前设备上不可用", setting.Label)
	}
	command, target, ok := setting.Command(state.Dialect)
	if !ok {
		return fmt.Errorf("%s 在当前固件（%s）上没有命令", setting.Label, state.Dialect)
	}
	wire, ok := setting.WireOf(value)
	if !ok {
		return fmt.Errorf("%s 没有取值 %q", setting.Label, value)
	}

	const seq = 42
	frame := duml.BuildCommand(state.Dialect, seq, target, command, wire)
	fmt.Printf("发送 %s = %s（命令 0x%04x，目标 0x%04x）：% x\n",
		setting.Label, value, command, uint16(target), frame)
	if _, err := conn.Write(frame); err != nil {
		return err
	}

	deadline := time.Now().Add(2 * time.Second)
	buf := make([]byte, 512)
	var pending []byte
	for time.Now().Before(deadline) {
		n, err := conn.Read(buf)
		if err == usb.ErrTimeout {
			continue
		}
		if err != nil {
			return err
		}
		pending = append(pending, buf[:n]...)
		for {
			got, rest := duml.TakeFrame(pending)
			if got == nil {
				pending = rest
				break
			}
			pending = rest
			if echoed, ok := duml.AckSeq(got); ok {
				if echoed != seq {
					fmt.Printf("收到别的 ACK（seq=%d），继续等\n", echoed)
					continue
				}
				fmt.Printf("收到 ACK：命令被接收（% x）\n", got)
				return nil
			}
		}
	}
	return fmt.Errorf("2 秒内没有收到 ACK")
}

func controlInterfaceOf(status usb.Status) int {
	if model, ok := status.Model(); ok {
		return int(model.Interface)
	}
	return -1
}

func report(state duml.State, frames, timeouts int) {
	fmt.Printf("  帧 %d（超时 %d）协议 %s 更新 %s\n", frames, timeouts,
		dialectName(state), state.Updated.Format("15:04:05"))
	fmt.Printf("  接收器: %s  序列号 %s  固件 %s\n",
		blank(state.RX.Name, "未知"), blank(state.RX.Serial, "未知"), blank(state.RX.Firmware, "未知"))
	if pct, ok := state.RX.BatteryPercent(); ok {
		fmt.Printf("          电量 %d%%  充电 %v\n", pct, state.RX.Charging)
	}
	if state.RX.HasGain {
		fmt.Printf("          增益旋钮 %+d dB\n", state.RX.GainDial)
	}
	for i, tx := range state.TX {
		if !tx.Present {
			fmt.Printf("  发射器 %d: 未连接\n", i+1)
			continue
		}
		line := fmt.Sprintf("  发射器 %d: %s  电平 %d", i+1, blank(tx.Serial, "未知"), tx.Level)
		if pct, ok := tx.BatteryPercent(); ok {
			line += fmt.Sprintf("  电量 %d%%", pct)
		}
		if tx.Charging {
			line += " 充电中"
		}
		if tx.VoiceTone != "" {
			line += "  音色 " + tx.VoiceTone
		}
		if tx.Firmware != "" {
			line += "  固件 " + tx.Firmware
		}
		fmt.Println(line)
	}
	settings := make([]string, 0, len(state.Settings))
	for id, value := range state.Settings {
		settings = append(settings, id+"="+value)
	}
	sort.Strings(settings)
	if len(settings) > 0 {
		fmt.Println("  设置:", strings.Join(settings, " "))
	}
}

func dialectName(state duml.State) string {
	if !state.DialectKnown {
		return "未知"
	}
	return state.Dialect.String()
}

func blank(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
