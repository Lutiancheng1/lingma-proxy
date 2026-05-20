package main

import (
	"embed"
	"os"
	goruntime "runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	enableInspector := os.Getenv("LINGMA_DESKTOP_DEBUG") == "1"

	err := wails.Run(&options.App{
		Title:             "Lingma Proxy",
		Width:             1160,
		Height:            760,
		MinWidth:          900,
		MinHeight:         600,
		StartHidden:       true,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		EnableDefaultContextMenu: enableInspector,
		Debug: options.Debug{
			OpenInspectorOnStartup: enableInspector,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 1},
		Menu:             appMenu(app),
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnDomReady:       app.onDomReady,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "lingma-proxy-desktop",
			OnSecondInstanceLaunch: app.onSecondInstanceLaunch,
		},
		Bind: []interface{}{
			app,
		},
		Frameless: false,
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: false,
				HideTitle:                  false,
				HideTitleBar:               false,
				FullSizeContent:            false,
				UseToolbar:                 false,
				HideToolbarSeparator:       true,
			},
			About: &mac.AboutInfo{
				Title:   "Lingma Proxy",
				Message: "A desktop GUI for Lingma Proxy",
			},
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

func appMenu(app *App) *menu.Menu {
	quitAccelerator := keys.OptionOrAlt("f4")
	closeWindowAccelerator := keys.CmdOrCtrl("w")
	minimizeWindowAccelerator := keys.CmdOrCtrl("m")
	if goruntime.GOOS == "darwin" {
		quitAccelerator = keys.CmdOrCtrl("q")
		closeWindowAccelerator = keys.CmdOrCtrl("w")
		minimizeWindowAccelerator = keys.CmdOrCtrl("m")
	}

	appMenu := menu.NewMenu()
	appMenu.AddText("关闭窗口", closeWindowAccelerator, func(_ *menu.CallbackData) {
		app.HideWindow()
	})
	appMenu.AddText("最小化窗口", minimizeWindowAccelerator, func(_ *menu.CallbackData) {
		app.MinimizeWindow()
	})
	appMenu.AddSeparator()
	appMenu.AddText("退出 Lingma Proxy", quitAccelerator, func(_ *menu.CallbackData) {
		app.RequestQuitShortcut()
	})

	return menu.NewMenuFromItems(
		menu.SubMenu("Lingma Proxy", appMenu),
		menu.EditMenu(),
	)
}
