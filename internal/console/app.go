package console

import (
	"strconv"
	"sync"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/duml"
	"dji-mic-rx/internal/session"
)

// page is one entry of the navigation rail.
type page struct {
	id    string
	title string
	label string
	icon  *ui.SVG
}

// pages are the window's pages, in the order the rail shows them.
var pages = []page{
	{id: "status", title: "状态", label: "状态", icon: iconReceiver},
	{id: "settings", title: "设置", label: "设置", icon: iconSliders},
	{id: "driver", title: "驱动", label: "驱动", icon: iconChip},
}

// Source is what the window reads and asks: the device session in the app,
// and a stand-in when a page has to be drawn without a receiver attached.
type Source interface {
	// Snapshot returns everything to draw: the device tree, the decoded
	// receiver state, and the activity log.
	Snapshot() session.Snapshot
	// Rescan asks for an immediate scan of the device tree.
	Rescan()
	// Log records a line in the activity log.
	Log(format string, a ...any)
	// Send writes a shared setting and waits for the receiver's
	// acknowledgement.
	Send(settingID, value string) error
	// SendToTransmitter writes a per-transmitter setting to one unit.
	SendToTransmitter(settingID, value string, unit int) error
	// Install and Uninstall manage the driver, elevated.
	Install(progress func(string)) error
	Uninstall(progress func(string)) error
	// Diagnostics renders the full report for a bug report.
	Diagnostics() string
}

// App is the window's state: what lasts between frames. The receiver's own
// state lives in the session, which the view only reads.
type App struct {
	sess Source

	// page is the index into pages.
	page int

	// mu guards the messages written from the goroutines that send commands
	// and run installers.
	mu          sync.Mutex
	notice      string
	noticeError bool
}

// New returns the app for a source: the device session in the app, or a
// stand-in in tests.
func New(sess Source) *App {
	return &App{sess: sess}
}

// ShowPage selects a page by id, for the driver badge to send the user to
// the driver page.
func (a *App) ShowPage(id string) {
	for i, p := range pages {
		if p.id == id {
			a.page = i
			return
		}
	}
}

// snapshot reads the session, which is safe from the main thread: the session
// guards its own state.
func (a *App) snapshot() session.Snapshot { return a.sess.Snapshot() }

// setSetting writes a shared setting. The command is sent from its own
// goroutine: writing and waiting for the receiver's acknowledgement must not
// hold up the frame that is being drawn, and the receiver's own status pushes
// are what confirm the new value on screen.
func (a *App) setSetting(c *ui.Context, id, value string) {
	setting, known := duml.SettingByID(id)
	if !known {
		return
	}
	label := setting.Label + "：" + valueLabel(setting, value)
	a.notify("已发送 " + label)
	go func() {
		if err := a.sess.Send(id, value); err != nil {
			a.notifyError(err.Error())
			return
		}
		a.sess.Log("已设置 %s", label)
	}()
}

// setVoiceTone writes one transmitter's voice tone.
func (a *App) setVoiceTone(c *ui.Context, unit int, value string) {
	setting, _ := duml.SettingByID("voice-tone")
	label := "发射器 " + strconv.Itoa(unit) + " 音色：" + valueLabel(setting, value)
	a.notify("已发送 " + label)
	go func() {
		if err := a.sess.SendToTransmitter("voice-tone", value, unit); err != nil {
			a.notifyError(err.Error())
			return
		}
		a.sess.Log("已设置 %s", label)
	}()
}

// notify records a message for the view to show, since the work that produced
// it runs off the main thread.
func (a *App) notify(message string) {
	a.mu.Lock()
	a.notice, a.noticeError = message, false
	a.mu.Unlock()
}

// notifyError records a failure to show.
func (a *App) notifyError(message string) {
	a.mu.Lock()
	a.notice, a.noticeError = message, true
	a.mu.Unlock()
}

// showNotice turns a recorded message into a toast, once.
func (a *App) showNotice(c *ui.Context) {
	a.mu.Lock()
	message, failed := a.notice, a.noticeError
	a.notice, a.noticeError = "", false
	a.mu.Unlock()
	if message == "" {
		return
	}
	if failed {
		c.Toast("设置失败：" + message)
		return
	}
	c.Toast(message)
}

// valueLabel renders a value slug with the label the setting gives it.
func valueLabel(setting duml.Setting, value string) string {
	if option, ok := setting.Option(value); ok {
		return option.Label
	}
	return value
}

// install starts the elevated driver install and reports progress through the
// driver page's log.
func (a *App) install(c *ui.Context) {
	if a.sess.Snapshot().Installing {
		return
	}
	go func() {
		if err := a.sess.Install(nil); err != nil {
			// The log on the driver page carries the detail; the toast
			// says that something needs looking at.
			a.sess.Log("安装未完成：%v", err)
		}
	}()
	c.Toast("正在安装驱动：请在系统弹窗里允许以管理员身份运行")
}

// uninstall removes the driver package.
func (a *App) uninstall(c *ui.Context) {
	if a.sess.Snapshot().Installing {
		return
	}
	go func() {
		if err := a.sess.Uninstall(nil); err != nil {
			a.sess.Log("卸载未完成：%v", err)
		}
	}()
	c.Toast("正在移除驱动：请在系统弹窗里允许以管理员身份运行")
}

// copyDiagnostics puts the full report on the clipboard.
func (a *App) copyDiagnostics(c *ui.Context) {
	c.WriteClipboard(a.sess.Diagnostics())
	c.Toast("诊断信息已复制到剪贴板")
}
