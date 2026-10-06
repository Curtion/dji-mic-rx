// Command dji-mic-rx shows a DJI Mic receiver's state and settings on
// Windows.
//
// The receiver's vendor interface has no driver out of the box, so the app
// offers to install Microsoft's in-box WinUSB driver on that one interface,
// and then reads the status stream the receiver pushes about ten times a
// second.
package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/console"
	"dji-mic-rx/internal/session"
	"dji-mic-rx/internal/usb"
)

// helperFlags are the arguments the elevated half of a driver install runs
// with. When they are present this process is not the window: it performs the
// privileged step and exits.
var helperFlags = []string{"--driver-install-package", "--driver-uninstall"}

func main() {
	args := os.Args[1:]
	if wantsHelper(args) {
		os.Exit(usb.RunHelper(args))
	}

	sess := session.New(session.Options{})
	app := console.New(sess)

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:    "DJI Mic 接收器",
			Width:    1080,
			Height:   760,
			MinWidth: 940, MinHeight: 620,
			StateKey: "main",
			// The window follows the desktop's appearance; these are the
			// same two plates the view paints, so nothing flashes.
			BackgroundColor: "light-dark(#f0f2f1, #15191b)",
			Content:         ui.View(app.View),
		})
		sess.SetOnChange(win.Invalidate)
		sess.Start()
	})

	if err := mygo.App.Run(); err != nil {
		sess.Stop()
		log.Fatal(err)
	}
	sess.Stop()
}

func wantsHelper(args []string) bool {
	for _, a := range args {
		for _, flag := range helperFlags {
			if a == flag {
				return true
			}
		}
	}
	return false
}
