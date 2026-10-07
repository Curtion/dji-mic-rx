package console

import (
	"context"
	"fmt"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// updateState is where a self-update check stands.
type updateState int

const (
	updateIdle updateState = iota
	updateChecking
	updateCurrent
	updateAvailable
	updateInstalling
	updateReady
	updateFailed
)

// updater holds the state of self-update checks, guarded by mu. The checks
// run off the main thread and ask for a frame through invalidate.
type updater struct {
	mu         sync.Mutex
	state      updateState
	update     *mygo.Update
	err        string
	downloaded int64
	total      int64
	invalidate func()

	// enabled caches Updater.Enabled, which probes the install directory.
	enabledOnce sync.Once
	enabled     bool
}

// Enabled reports once whether this build can update itself.
func (u *updater) Enabled() bool {
	u.enabledOnce.Do(func() {
		u.enabled = mygo.Updater.Enabled()
	})
	return u.enabled
}

// SetInvalidate registers the window's Invalidate, so update checks running
// off the main thread can ask for a frame.
func (a *App) SetInvalidate(fn func()) { a.updater.SetInvalidate(fn) }

// CheckUpdates asks the update feed for a newer build, when this build can
// update itself.
func (a *App) CheckUpdates() { a.updater.Check() }

// SetInvalidate registers the window's Invalidate, so update checks running
// off the main thread can ask for a frame.
func (u *updater) SetInvalidate(fn func()) {
	u.mu.Lock()
	u.invalidate = fn
	u.mu.Unlock()
}

func (u *updater) invalidateView() {
	u.mu.Lock()
	fn := u.invalidate
	u.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Check asks the update feed for a newer build, when this build can update
// itself. It no-ops while a check or an install is running.
func (u *updater) Check() {
	if !u.Enabled() {
		return
	}
	u.mu.Lock()
	if u.state == updateChecking || u.state == updateInstalling || u.state == updateReady {
		u.mu.Unlock()
		return
	}
	u.state, u.err = updateChecking, ""
	u.mu.Unlock()
	u.invalidateView()

	go func() {
		update, err := mygo.Updater.Check(context.Background())
		u.mu.Lock()
		switch {
		case err != nil:
			u.state, u.err = updateFailed, err.Error()
		case update == nil:
			u.state = updateCurrent
		default:
			u.state, u.update = updateAvailable, update
		}
		u.mu.Unlock()
		u.invalidateView()
	}()
}

// install downloads the update Check found and swaps it in; the new version
// runs after Relaunch.
func (u *updater) install() {
	u.mu.Lock()
	update := u.update
	if update == nil || u.state != updateAvailable {
		u.mu.Unlock()
		return
	}
	u.state, u.downloaded, u.total = updateInstalling, 0, 0
	u.mu.Unlock()
	u.invalidateView()

	go func() {
		lastPercent := -1
		err := update.Install(context.Background(), func(downloaded, total int64) {
			percent := 0
			if total > 0 {
				percent = int(downloaded * 100 / total)
			}
			u.mu.Lock()
			u.downloaded, u.total = downloaded, total
			u.mu.Unlock()
			if percent != lastPercent {
				lastPercent = percent
				u.invalidateView()
			}
		})
		u.mu.Lock()
		if err != nil {
			u.state, u.err = updateFailed, err.Error()
		} else {
			u.state = updateReady
		}
		u.mu.Unlock()
		u.invalidateView()
	}()
}

// updateStatus is the rail's self-update row: the running version on the
// left, and the state of the last check with its next action on the right.
// Builds that cannot update themselves — development, plain go build — show
// the version alone.
func (a *App) updateStatus(c *ui.Context, p palette) {
	version := mygo.App.Version()
	if version == "" {
		version = "开发构建"
	} else {
		version = "v" + version
	}

	u := &a.updater
	u.mu.Lock()
	state, update := u.state, u.update
	downloaded, total := u.downloaded, u.total
	u.mu.Unlock()

	if !u.Enabled() {
		ui.Row(c).Children(func() {
			ui.Text(c, version).FontSize(sizeUnit).TextColor(p.inkFaint).SingleLine()
		})
		return
	}

	label, color, icon, clickable := "", p.inkDim, iconRefresh, false
	switch state {
	case updateIdle:
		label, clickable = "检查更新", true
	case updateChecking:
		label = "正在检查更新…"
	case updateCurrent:
		label = "已是最新"
	case updateAvailable:
		label, color, icon, clickable = "下载更新 v"+update.Version, p.signal, iconDownload, true
	case updateInstalling:
		icon = iconDownload
		if total > 0 {
			label = fmt.Sprintf("正在下载更新 %d%%", downloaded*100/total)
		} else {
			label = fmt.Sprintf("正在下载更新 %.1f MB", float64(downloaded)/1e6)
		}
	case updateReady:
		label, color, clickable = "重启完成更新", p.signal, true
	case updateFailed:
		label, color, clickable = "检查更新失败，点击重试", p.alarm, true
	}

	hairline(c, p)
	row := ui.Row(c).Gap(6).AlignItems(ui.Center)
	if clickable {
		row.Cursor(ui.CursorPointer)
	}
	row.Children(func() {
		ui.Text(c, version).FontSize(sizeUnit).TextColor(p.inkFaint).SingleLine()
		ui.Spacer(c)
		ui.Icon(c, icon).FontSize(12).TextColor(color)
		ui.Text(c, label).FontSize(sizeUnit).TextColor(color).SingleLine()
	})
	switch {
	case !clickable:
	case state == updateAvailable:
		if row.Clicked() {
			u.install()
		}
	case state == updateReady:
		if row.Clicked() {
			mygo.App.Relaunch()
		}
	default:
		if row.Clicked() {
			u.Check()
		}
	}
}
