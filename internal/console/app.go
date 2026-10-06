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
	// Send waits until the device reports the requested value.
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

	// scroll keeps each page's own place in the scroll view, so moving
	// between pages and back does not lose it.
	scroll []ui.ScrollState

	// mu guards the messages written from the goroutines that send commands
	// and run installers.
	mu          sync.Mutex
	notice      string
	noticeError bool
	pending     map[settingKey]bool
}

type settingKey struct {
	id   string
	unit int
}

// New returns the app for a source: the device session in the app, or a
// stand-in in tests.
func New(sess Source) *App {
	return &App{sess: sess, scroll: make([]ui.ScrollState, len(pages)), pending: make(map[settingKey]bool)}
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

// Device confirmation must not block the UI thread.
func (a *App) setSetting(c *ui.Context, id, value string) {
	a.sendSetting(id, value, 0)
}

func (a *App) setVoiceTone(c *ui.Context, unit int, value string) {
	a.sendSetting("voice-tone", value, unit)
}

func (a *App) sendSetting(id, value string, unit int) {
	setting, known := duml.SettingByID(id)
	if !known {
		return
	}
	key := settingKey{id: id, unit: unit}
	a.mu.Lock()
	if a.pending[key] {
		a.mu.Unlock()
		return
	}
	a.pending[key] = true
	a.mu.Unlock()

	label := setting.Label + "：" + valueLabel(setting, value)
	if unit > 0 {
		label = "发射器 " + strconv.Itoa(unit) + " " + label
	}
	a.sess.Log("正在设置 %s", label)
	go func() {
		var err error
		if unit > 0 {
			err = a.sess.SendToTransmitter(id, value, unit)
		} else {
			err = a.sess.Send(id, value)
		}
		a.mu.Lock()
		delete(a.pending, key)
		a.mu.Unlock()
		if err != nil {
			a.notifyError(err.Error())
			a.sess.Log("设置未确认：%v", err)
			return
		}
		a.notify("已设置 " + label)
		a.sess.Log("已设置 %s", label)
	}()
}

func (a *App) settingPending(id string, unit int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending[settingKey{id: id, unit: unit}]
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
		c.Toast("设置未完成：" + message)
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
