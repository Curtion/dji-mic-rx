package console

import (
	"sync"
	"time"

	"github.com/egoist/mygo"
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

// pages are the window's pages, in the order the rail shows them. The page
// list is the app's structure: what the receiver can do, then how it is
// connected, then what the program is.
var pages = []page{
	{id: "status", title: "状态", label: "状态", icon: iconReceiver},
	{id: "audio", title: "音频", label: "音频", icon: iconSliders},
	{id: "power", title: "电源与开机", label: "电源", icon: iconPower},
	{id: "device", title: "设备", label: "设备", icon: iconTransmitter},
	{id: "driver", title: "驱动", label: "驱动", icon: iconChip},
	{id: "about", title: "关于", label: "关于", icon: iconInfo},
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
	// OpenZadig and OpenWdiSimple run a helper program the user supplied.
	OpenZadig() error
	OpenWdiSimple(path string) error
	// RememberWdiSimple stores the path of a helper program.
	RememberWdiSimple(path string)
	// Diagnostics renders the full report for a bug report.
	Diagnostics() string
}

// App is the window's state: what lasts between frames. The receiver's own
// state lives in the session, which the view only reads.
type App struct {
	sess Source

	// page is the index into pages.
	page int
	// railOpen marks pages the user can reach with a number key, shown in
	// the rail as a hint.
	showKeys bool

	// peaks hold the level meter's peak marks per transmitter, and peakAt
	// when each was set, so the marks fall back once the sound stops.
	peak      [2]float64
	peakAt    [2]time.Time
	pulseSeen int

	// confirmUninstall asks before removing a driver, which needs a
	// replug afterwards.
	confirmUninstall bool
	// showManual expands the manual installation steps on the driver page.
	showManual bool
	// prompted marks that the startup prompt has been shown, so it is one
	// interruption per run rather than one per rescan.
	prompted bool

	// ask is the confirmation the window is waiting for, and askOpen is
	// the dialog that owns it.
	ask     confirm
	askOpen bool

	// window is the app's window, for dialogs that need a parent.
	window *mygo.Window

	// mu guards the messages written from the goroutines that send commands
	// and run installers.
	mu          sync.Mutex
	notice      string
	noticeError bool
}

// confirmKind says which decision a confirmation dialog is asking for.
type confirmKind int

const (
	confirmNone confirmKind = iota
	// confirmUninstall asks before removing the driver package, which
	// needs a replug afterwards.
	confirmUninstall
	// confirmReboot asks before a setting that restarts the receiver.
	confirmReboot
)

// confirm is a pending question.
type confirm struct {
	kind    confirmKind
	setting string
	value   string
}

// SetWindow gives the app its window, so dialogs can be attached to it.
func (a *App) SetWindow(win *mygo.Window) { a.window = win }

// New returns the app for a source: the device session in the app, or a
// stand-in in tests.
func New(sess Source) *App {
	return &App{sess: sess, showKeys: true}
}

// Page returns the id of the page being shown.
func (a *App) Page() string { return pages[a.page].id }

// ShowPage selects a page by id, for the startup prompt to send the user to
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
	label := "发射器 " + itoa(unit) + " 音色：" + valueLabel(setting, value)
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

// uninstall removes the driver package after the confirmation.
func (a *App) uninstall(c *ui.Context) {
	go func() {
		if err := a.sess.Uninstall(nil); err != nil {
			a.sess.Log("卸载未完成：%v", err)
		}
	}()
	c.Toast("正在移除驱动：请在系统弹窗里允许以管理员身份运行")
}

// runWdiSimple runs a user-supplied wdi-simple.exe, libwdi's installer.
func (a *App) runWdiSimple(c *ui.Context) {
	path := a.snapshot().Tools.WdiSimple
	go func() {
		if err := a.sess.OpenWdiSimple(path); err != nil {
			a.sess.Log("wdi-simple 未完成：%v", err)
		}
	}()
	c.Toast("正在用 wdi-simple.exe 安装：请在系统弹窗里允许")
}

// openZadig starts libwdi's graphical installer, for when the automatic path
// is refused and the user wants to do it by hand.
func (a *App) openZadig(c *ui.Context) {
	if err := a.sess.OpenZadig(); err != nil {
		c.Toast(err.Error())
		return
	}
	a.ShowPage("driver")
	c.Toast("已打开 Zadig：选择 Wireless Mic Rx 的 Interface 6，驱动选 WinUSB")
}

// pickWdiSimple asks for the path of a wdi-simple.exe and remembers it.
func (a *App) pickWdiSimple(win *mygo.Window) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: win,
		Title:  "选择 wdi-simple.exe",
		Filters: []mygo.FileFilter{
			{Name: "可执行文件", Extensions: []string{"exe"}},
		},
	})
	if err != nil || len(paths) == 0 {
		return
	}
	a.sess.RememberWdiSimple(paths[0])
	a.sess.Log("已记录 wdi-simple.exe：%s", paths[0])
}

// copyDiagnostics puts the full report on the clipboard.
func (a *App) copyDiagnostics(c *ui.Context) {
	c.WriteClipboard(a.sess.Diagnostics())
	c.Toast("诊断信息已复制到剪贴板")
}

// updatePeaks advances the level meters' peak marks: they rise instantly and
// fall back slowly, as a meter's peak hold does.
func (a *App) updatePeaks(snap session.Snapshot) {
	now := snap.Time
	for i, tx := range snap.State.TX {
		fraction, ok := duml.LevelFraction(tx.Level)
		if !ok || !tx.Present {
			a.peak[i] = 0
			a.peakAt[i] = now
			continue
		}
		if fraction >= a.peak[i] {
			a.peak[i] = fraction
			a.peakAt[i] = now
			continue
		}
		// Hold for a moment, then fall by the time that has passed.
		if elapsed := now.Sub(a.peakAt[i]); elapsed > 900*time.Millisecond {
			fall := float64(elapsed-900*time.Millisecond) / float64(2*time.Second)
			a.peak[i] = max(0, a.peak[i]-fall*0.6)
			if a.peak[i] < fraction {
				a.peak[i] = fraction
			}
		}
	}
}

// pulse is how brightly the activity light burns: it lights when a status
// frame arrives and fades over the next few hundred milliseconds, so the
// window shows that the receiver is talking without any decoration.
func (a *App) pulse(snap session.Snapshot) float32 {
	if !snap.Connected || snap.LastFrame.IsZero() {
		return 0
	}
	age := snap.Time.Sub(snap.LastFrame)
	if age < 0 {
		age = 0
	}
	if age > 400*time.Millisecond {
		return 0.18
	}
	f := 1 - float32(age)/float32(400*time.Millisecond)
	return 0.18 + 0.82*f
}

func itoa(v int) string {
	if v >= 0 && v < 10 {
		return string(rune('0' + v))
	}
	return smallIntText(v)
}

func smallIntText(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
