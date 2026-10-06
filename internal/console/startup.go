package console

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// PromptForDriver is the app's one interruption: shortly after the window
// opens, and only when a receiver is plugged in whose vendor interface still
// needs a driver, it asks whether to install it now.
//
// It never installs anything by itself. Installing a driver is a system
// change, so it happens only after this question and the administrator prompt
// that follows it.
func (a *App) PromptForDriver(win *mygo.Window) {
	if a.prompted {
		return
	}
	a.prompted = true

	// Let the window paint first: a dialog over a blank window reads as an
	// error rather than as a step.
	time.Sleep(900 * time.Millisecond)

	snap := a.snapshot()
	if !snap.HaveModel || snap.Status.Ready() {
		return
	}

	message, detail := startupPrompt(snap)
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:  win,
		Type:    mygo.MessageInfo,
		Message: message,
		Detail:  detail,
		Buttons: []string{"立即安装", "在设置中管理", "稍后"},
	})
	if err != nil {
		a.sess.Log("启动提示未能显示：%v", err)
		return
	}

	switch result.Button {
	case 0:
		win.Update(func() { a.ShowPage("driver") })
		go func() {
			if err := a.sess.Install(nil); err != nil {
				a.sess.Log("安装未完成：%v", err)
			}
		}()
	case 1:
		win.Update(func() { a.ShowPage("driver") })
	}
}

// startupPrompt writes the question for the state the device is in. The two
// cases worth interrupting for are an interface with no driver, and an
// interface that has not appeared at all.
func startupPrompt(snap session.Snapshot) (message, detail string) {
	message = "接收器需要安装一次驱动"
	interfaceNumber := interfaceOf(snap)
	common := "驱动只绑定接收器的厂商接口（接口 " + itoa(interfaceNumber) +
		"），录音用的音频接口和按键接口保持系统驱动，不会受影响。安装会在系统弹窗里请求管理员权限，之后建议重新插拔接收器。"

	switch {
	case snap.Status.Control != nil && snap.Status.Control.Present:
		detail = "厂商接口还没有绑定 WinUSB 驱动，所以程序读不到麦克风的状态。" + common
	default:
		detail = "接收器已经连上，但它的厂商接口还没有出现在 Windows 的设备列表里。" +
			"可以先安装驱动，然后重新插拔接收器让接口出现。" + common
	}
	return message, detail
}

// dialogs draws the confirmations the app is waiting for. They sit above
// everything else, so a decision never hides behind a page.
func (a *App) dialogs(c *ui.Context, p palette) {
	switch a.ask.kind {
	case confirmUninstall:
		a.askOpen = true
		switch ui.AlertDialog(c, &a.askOpen,
			"移除 WinUSB 驱动？",
			"只移除接收器厂商接口的驱动绑定。接收器的音频接口不受影响，移除后需要重新插拔接收器，Windows 才会回到默认驱动。",
			"取消", "移除驱动") {
		case 0:
			a.ask = confirm{}
		case 1:
			a.ask = confirm{}
			a.uninstall(c)
		}

	case confirmReboot:
		setting, known := duml.SettingByID(a.ask.setting)
		if !known {
			a.ask = confirm{}
			return
		}
		a.askOpen = true
		switch ui.AlertDialog(c, &a.askOpen,
			"接收器会重启",
			"更改「"+setting.Label+"」后接收器会重新启动，连接会中断几秒钟，然后自动恢复。",
			"取消", "继续") {
		case 0:
			a.ask = confirm{}
		case 1:
			pending := a.ask
			a.ask = confirm{}
			a.setSetting(c, pending.setting, pending.value)
		}
	}
}
