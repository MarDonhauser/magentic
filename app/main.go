package main

import (
	"context"
	"embed"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func fixPath() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, shell, "-l", "-c", "echo -n $PATH").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			os.Setenv("PATH", p)
		}
	}
	home, _ := os.UserHomeDir()
	path := os.Getenv("PATH")
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", home + "/.local/bin"} {
		if !strings.Contains(path, d) {
			path += ":" + d
		}
	}
	os.Setenv("PATH", path)
}

func main() {
	fixPath()
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "magentic",
		Width:     1360,
		Height:    860,
		MinWidth:  700,
		MinHeight: 400,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:  &options.RGBA{R: 32, G: 36, B: 43, A: 1},
		HideWindowOnClose: true,
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
