// Command uirender draws the console's pages without a window, so the layout
// can be looked at as pictures. It is a development tool: the app itself never
// renders off screen.
//
//	go run ./cmd/uirender -out screenshots
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"dji-mic-rx/internal/console"
)

func main() {
	out := flag.String("out", "screenshots", "where to write the images")
	width := flag.Int("w", 1180, "window width in DIPs")
	height := flag.Int("h", 820, "window height in DIPs")
	scale := flag.Float64("scale", 1.5, "device pixel ratio")
	pages := flag.String("pages", "status,settings,driver", "pages to draw")
	flag.Parse()

	situations := []struct {
		name string
		kind console.DemoKind
	}{
		{"live", console.DemoLive},
		{"v1", console.DemoV1},
		{"nointerface", console.DemoNoInterface},
		{"nodriver", console.DemoNoDriver},
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	drawn := 0
	for _, situation := range situations {
		app := console.New(console.NewDemo(situation.kind))
		for _, page := range splitList(*pages) {
			app.ShowPage(page)
			for _, dark := range []bool{true, false} {
				name := fmt.Sprintf("%s-%s-%s.png", situation.name, page, themeName(dark))
				path := filepath.Join(*out, name)
				if err := draw(app, *width, *height, float32(*scale), dark, path); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				drawn++
			}
		}
	}
	fmt.Printf("wrote %d images to %s\n", drawn, *out)
}

func draw(app *console.App, width, height int, scale float32, dark bool, path string) error {
	tester := ui.NewTester(app.View, width, height)
	tester.SetScale(scale)
	tester.SetDark(dark)
	tester.Frame()
	img := tester.Image()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func themeName(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

func splitList(s string) []string {
	var out []string
	current := ""
	for _, r := range s {
		if r == ',' {
			if current != "" {
				out = append(out, current)
			}
			current = ""
			continue
		}
		current += string(r)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}
