// LiFePO4 BMS Monitor — read-only battery dashboard for Windows.
//
// The application scans Bluetooth LE for supported BMS devices (HiLink /
// Humsienk HS03 and HS04 families today, other vendors planned), lists them in
// a desktop window and refreshes the data of the selected battery in a loop
// (5 seconds by default). It never writes settings to a battery.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/jchv/go-webview2"
	"tinygo.org/x/bluetooth"
)

const (
	appName    = "LiFePO4 BMS Monitor"
	appVersion = "0.1.0"
	logName    = "lifepo4-bms-monitor.log"
)

func main() {
	defer logPanic("main")

	portFlag := flag.Int("port", 0, "UI server port (0 = free port chosen automatically)")
	listenFlag := flag.String("listen", "127.0.0.1", "interface address for the UI and the read API: 127.0.0.1 (this computer), 0.0.0.0 (the local network) or one address")
	noWindow := flag.Bool("no-window", false, "run the UI server only, without the desktop window")
	intervalFlag := flag.Float64("interval", 5, "how often battery data is refreshed, in seconds")
	langFlag := flag.String("lang", "en", "default interface language: en or pl")
	dataFlag := flag.String("data", "", "data directory (default: %APPDATA%\\LiFePO4BMSMonitor)")
	versionFlag := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("%s %s\n", appName, appVersion)
		return
	}

	setupLogging()
	log.Printf("%s %s", appName, appVersion)

	interval := time.Duration(*intervalFlag * float64(time.Second))
	if interval < time.Second {
		interval = time.Second
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store := NewStore(*dataFlag)
	settings := NewSettings(store.Dir(), interval)
	adapter := bluetooth.DefaultAdapter
	hub := NewHub(adapter, store)
	hub.Start(ctx)
	publisher := newMQTTPublisher(settings, store, appVersion)
	go publisher.Run(ctx)
	session := NewSession(hub, store, settings, publisher)
	go session.StartAutoSelect(ctx)
	window := &windowController{}

	listener, err := net.Listen("tcp", net.JoinHostPort(listenAddress(*listenFlag), strconv.Itoa(*portFlag)))
	if err != nil {
		log.Fatalf("cannot start the UI server: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	// The window always talks to this computer; other computers (if the
	// interface was opened to the network) read the state only.
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	server := NewServer(hub, session, window, settings, store, publisher, serverConfig{
		Language: *langFlag,
		Version:  appVersion,
		Listen:   listenAddress(*listenFlag),
		Network:  networkURLs(*listenFlag, port),
	})
	go func() {
		if err := http.Serve(listener, server.Handler()); err != nil {
			log.Printf("UI server stopped: %v", err)
		}
	}()
	log.Printf("interface available at %s (refresh every %s)", url, settings.Interval())
	for _, address := range server.config.Network {
		log.Printf("other computers can read http://%s/api/batteries (interface: http://%s/)", address, address)
	}

	if *noWindow {
		<-ctx.Done()
		return
	}

	appWindow := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  appName,
			Width:  1280,
			Height: 800,
			Center: true,
			IconId: iconResourceID,
		},
	})
	if appWindow == nil {
		log.Printf("WebView2 is unavailable, opening the interface in Edge instead")
		openEdgeApp(url)
		<-ctx.Done()
		return
	}
	appWindow.Dispatch(func() {
		handle := uintptr(appWindow.Window())
		window.set(handle)
		makeFrameless(handle)
	})
	appWindow.Navigate(url)
	appWindow.Run()
	appWindow.Destroy()
	log.Printf("window closed, exiting")
}

// setupLogging mirrors the log to a file next to the executable — the desktop
// window has no console (the release build uses -H=windowsgui) and diagnostics
// are still useful. The file is written first: stderr of a GUI process is an
// invalid handle and would otherwise swallow every message.
func setupLogging() {
	writers := []io.Writer{}
	executable, err := os.Executable()
	if err == nil {
		logPath := filepath.Join(filepath.Dir(executable), logName)
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err == nil {
			writers = append(writers, file)
		}
	}
	if hasConsole() {
		writers = append(writers, os.Stderr)
	}
	if len(writers) == 0 {
		return
	}
	log.SetOutput(io.MultiWriter(writers...))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

// logPanic records a crashing goroutine so the log file explains an unexpected
// exit instead of ending silently.
func logPanic(where string) {
	if recovered := recover(); recovered != nil {
		log.Printf("panic in %s: %v\n%s", where, recovered, debug.Stack())
		panic(recovered)
	}
}

// openEdgeApp shows the interface in Edge's app mode (no address bar) when the
// WebView2 runtime is not installed.
func openEdgeApp(url string) {
	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			_ = exec.Command(candidate, "--app="+url, "--window-size=1400,900").Start()
			return
		}
	}
	_ = exec.Command("cmd", "/c", "start", "", url).Start()
}
