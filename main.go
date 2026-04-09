package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name: "KiCad Library Manager",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Setting ActivationPolicyAccessory hides the dock icon natively on macOS
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	// Create main window (hidden by default)
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "KiCad Library Manager",
		Width:  500,
		Height: 660,
		Hidden: true,
	})

	// Create our service and register it
	appService := NewApp(app, window)
	app.RegisterService(application.NewService(appService))

	// Native Wails v3 System Tray Setup
	systray := app.SystemTray.New()
	systray.SetIcon(trayIcon) // trayIcon comes from autostart_*.go OS specifics
	systray.SetLabel("KiCad Lib Mgr")

	menu := app.NewMenu()
	menu.Add("Open Settings").OnClick(func(ctx *application.Context) {
		window.Show()
	})
	menu.AddSeparator()
	menu.Add("Undo Last Import").OnClick(func(ctx *application.Context) {
		conf := LoadConfig()
		if len(conf.History) > 0 {
			lastItem := conf.History[len(conf.History)-1]
			appService.UndoAction(lastItem.ID)
		}
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(ctx *application.Context) {
		app.Quit()
	})

	systray.SetMenu(menu)

	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}
