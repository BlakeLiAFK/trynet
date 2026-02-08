package tray

import (
	_ "embed"

	"github.com/efeenesc/systray"
)

//go:embed icon.png
var iconData []byte

// Callbacks 托盘回调函数
type Callbacks struct {
	OnShow func() // 点击"显示窗口"
	OnQuit func() // 点击"退出"
}

// Init 初始化系统托盘（非阻塞）
func Init(cb Callbacks) {
	systray.Register(func() {
		systray.SetIcon(iconData)
		systray.SetTitle("")
		systray.SetTooltip("TryNet")

		mShow := systray.AddMenuItem("显示窗口", "Show Window")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "Quit")

		go func() {
			for {
				select {
				case <-mShow.ClickedCh:
					if cb.OnShow != nil {
						cb.OnShow()
					}
				case <-mQuit.ClickedCh:
					if cb.OnQuit != nil {
						cb.OnQuit()
					}
					return
				}
			}
		}()
	}, func() {
		// 托盘退出时的清理
	})
}
