package console

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The rail's update row spells out each state of a check, and stays out of
// the way of builds that cannot update themselves.
func TestUpdateStatusStates(t *testing.T) {
	render := func(app *App) *ui.Tester {
		tester := ui.NewTester(app.View, 1180, 820)
		tester.SetScale(1)
		tester.Frame()
		return tester
	}

	// A development build shows the version and nothing else.
	dev := render(New(NewDemo(DemoLive)))
	if !dev.HasText("开发构建") {
		t.Error("a build without a version does not say what it is")
	}
	if dev.HasText("检查更新") {
		t.Error("a development build offers to check for updates")
	}

	app := New(NewDemo(DemoLive))
	app.updater.enabledOnce.Do(func() { app.updater.enabled = true })

	states := []struct {
		state updateState
		text  string
	}{
		{updateIdle, "检查更新"},
		{updateChecking, "正在检查更新…"},
		{updateCurrent, "已是最新"},
		{updateAvailable, "下载更新 v1.0.6"},
		{updateInstalling, "正在下载更新 42%"},
		{updateReady, "重启完成更新"},
		{updateFailed, "检查更新失败，点击重试"},
	}
	for _, s := range states {
		app.updater.state = s.state
		app.updater.update = &mygo.Update{Version: "1.0.6"}
		app.updater.downloaded, app.updater.total = 42, 100
		tester := render(app)
		if !tester.HasText(s.text) {
			t.Errorf("state %d does not show %q", s.state, s.text)
		}
	}
}
