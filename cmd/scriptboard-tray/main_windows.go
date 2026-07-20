//go:build windows

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"

	"scriptboard/internal/config"
	"scriptboard/internal/platformservice"
)

var loaded config.Config

func main() {
	mutexName, _ := windows.UTF16PtrFromString("Local\\ScriptBoardTray")
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		return
	}
	defer windows.CloseHandle(mutex)
	loaded, err = config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		return
	}
	systray.Run(onReady, func() {})
}

func onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("ScriptBoard")
	systray.SetTooltip("ScriptBoard 服务控制器")
	status := systray.AddMenuItem("正在检查状态…", "服务进程与 HTTP 就绪状态")
	status.Disable()
	systray.AddSeparator()
	openWeb := systray.AddMenuItem("打开管理页面", "在默认浏览器中打开 ScriptBoard")
	start := systray.AddMenuItem("启动服务", "启动 Windows 服务")
	stop := systray.AddMenuItem("停止服务", "停止服务及活动 Run")
	restart := systray.AddMenuItem("重启服务", "重启 Windows 服务")
	lifecycleItems := []*systray.MenuItem{start, stop, restart}
	systray.AddSeparator()
	openManaged := systray.AddMenuItem("打开 Managed Root", loaded.ManagedRoot)
	openState := systray.AddMenuItem("打开 State Root", loaded.StateRoot)
	openLogs := systray.AddMenuItem("打开服务日志目录", filepath.Join(loaded.StateRoot, "logs"))
	systray.AddSeparator()
	quit := systray.AddMenuItem("退出托盘", "只退出托盘，不停止服务")

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			processRunning, ready := readiness()
			switch {
			case processRunning && ready:
				status.SetTitle("服务运行中 · HTTP 已就绪")
			case processRunning:
				status.SetTitle("服务运行中 · HTTP 未就绪")
			default:
				status.SetTitle("服务未运行")
			}
			<-ticker.C
		}
	}()
	go menuAction(openWeb.ClickedCh, func() { openURL(serviceURL()) })
	actions := make(chan trayAction)
	go func() {
		for action := range actions {
			for _, item := range lifecycleItems {
				item.Disable()
			}
			status.SetTitle(action.label)
			if err := action.run(); err != nil {
				status.SetTitle("服务操作失败 · 请打开日志")
			}
			for _, item := range lifecycleItems {
				item.Enable()
			}
		}
	}()
	go queueAction(start.ClickedCh, actions, trayAction{label: "正在启动 ScriptBoard 服务…", run: platformservice.Start})
	go queueAction(stop.ClickedCh, actions, trayAction{label: "正在停止 ScriptBoard 服务及活动 Run…", run: platformservice.Stop})
	go queueAction(restart.ClickedCh, actions, trayAction{label: "正在重启 ScriptBoard 服务…", run: platformservice.Restart})
	go menuAction(openManaged.ClickedCh, func() { openFolder(loaded.ManagedRoot) })
	go menuAction(openState.ClickedCh, func() { openFolder(loaded.StateRoot) })
	go menuAction(openLogs.ClickedCh, func() { openFolder(filepath.Join(loaded.StateRoot, "logs")) })
	go func() { <-quit.ClickedCh; systray.Quit() }()
}

func menuAction(clicked <-chan struct{}, action func()) {
	for range clicked {
		action()
	}
}

type trayAction struct {
	label string
	run   func() error
}

func queueAction(clicked <-chan struct{}, actions chan<- trayAction, action trayAction) {
	for range clicked {
		actions <- action
	}
}

func readiness() (bool, bool) {
	output, _ := platformservice.Status()
	running := strings.Contains(output, "RUNNING")
	client := &http.Client{Timeout: 2 * time.Second}
	if loaded.TLSCert != "" {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // local readiness probe only
	}
	response, err := client.Get(serviceURL() + "/login")
	if err != nil {
		return running, false
	}
	_ = response.Body.Close()
	return running, response.StatusCode < 500
}

func serviceURL() string {
	_, port, err := net.SplitHostPort(loaded.Listen)
	if err != nil {
		port = "8787"
	}
	scheme := "http"
	if loaded.TLSCert != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://127.0.0.1:%s", scheme, port)
}

func openURL(value string) {
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", value).Start()
}
func openFolder(path string) {
	_ = os.MkdirAll(path, 0o755)
	_ = exec.Command("explorer.exe", path).Start()
}

func trayIcon() []byte {
	buffer := new(bytes.Buffer)
	_ = binary.Write(buffer, binary.LittleEndian, []uint16{0, 1, 1})
	buffer.Write([]byte{16, 16, 0, 0})
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(32))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(1128))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(22))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(40))
	_ = binary.Write(buffer, binary.LittleEndian, int32(16))
	_ = binary.Write(buffer, binary.LittleEndian, int32(32))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(32))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(0))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(1024))
	_ = binary.Write(buffer, binary.LittleEndian, [4]uint32{})
	for range 16 * 16 {
		buffer.Write([]byte{0x86, 0x63, 0x0f, 0xff})
	}
	buffer.Write(make([]byte, 64))
	return buffer.Bytes()
}
