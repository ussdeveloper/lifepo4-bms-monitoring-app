package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed ui/*
var uiFiles embed.FS

// serverConfig carries the settings the interface reads once at start-up.
type serverConfig struct {
	Language string
	Version  string
	// Listen is the address the interface was bound to and Network lists the
	// URLs other computers can use (empty when only this computer is served).
	Listen  string
	Network []string
}

// Server serves the interface (files from ui/) and the JSON API used by the
// application window. The public read API can be switched off in the settings.
type Server struct {
	hub       *Hub
	session   *Session
	window    *windowController
	settings  *Settings
	store     *Store
	publisher *mqttPublisher
	config    serverConfig
	mux       *http.ServeMux
}

func NewServer(hub *Hub, session *Session, window *windowController, settings *Settings, store *Store, publisher *mqttPublisher, config serverConfig) *Server {
	server := &Server{
		hub:       hub,
		session:   session,
		window:    window,
		settings:  settings,
		store:     store,
		publisher: publisher,
		config:    config,
		mux:       http.NewServeMux(),
	}
	static, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err)
	}
	server.mux.Handle("/", http.FileServer(http.FS(static)))
	server.mux.HandleFunc("/api/state", server.handleState)
	server.mux.HandleFunc("/api/select", server.handleSelect)
	server.mux.HandleFunc("/api/deepscan", server.handleDeepScan)
	server.mux.HandleFunc("/api/release", server.handleRelease)
	server.mux.HandleFunc("/api/add", server.handleAdd)
	server.mux.HandleFunc("/api/window", server.handleWindow)
	server.mux.HandleFunc("/api/settings", server.handleSettings)
	server.mux.HandleFunc("/api/batteries", server.handleBatteries)
	return server
}

func (s *Server) Handler() http.Handler { return s.gate(s.mux) }

// gate keeps the interface read-only for other computers: only the JSON state
// and the read API answer them, everything that changes something (and the
// settings, which carry the broker password) stays on this machine.
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !remoteAllowed(request.RemoteAddr, request.Method, request.URL.Path) {
			http.Error(writer, "read only: the computer that monitors the battery owns this interface", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// isLoopbackAddress reports whether a "host:port" remote address is this
// computer itself.
func isLoopbackAddress(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return true
	}
	address := net.ParseIP(host)
	if address == nil {
		return false
	}
	return address.IsLoopback()
}

// remoteAllowed decides what a client from another computer may do.
func remoteAllowed(remote, method, path string) bool {
	if isLoopbackAddress(remote) {
		return true
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	switch path {
	case "/api/state", "/api/batteries":
		return true
	}
	// The interface itself is served (it reads the state above); every other
	// API call is local only.
	return !strings.HasPrefix(path, "/api/")
}

type configResponse struct {
	Version       string         `json:"version"`
	Language      string         `json:"language"`
	IntervalS     float64        `json:"intervalS"`
	DataDir       string         `json:"dataDir"`
	Platform      string         `json:"platform"`
	MQTTConnected bool           `json:"mqttConnected"`
	Radios        []radioSummary `json:"radios"`
	Settings      settingsView   `json:"settings"`
	Remote        bool           `json:"remote"`
	Listen        string         `json:"listen"`
	Network       []string       `json:"network"`
}

type stateResponse struct {
	Hub     HubState       `json:"hub"`
	Session SessionState   `json:"session"`
	Config  configResponse `json:"config"`
}

func (s *Server) handleState(writer http.ResponseWriter, request *http.Request) {
	language := strings.ToLower(strings.TrimSpace(s.config.Language))
	if language != "pl" {
		language = "en"
	}
	settings := s.settings.View()
	remote := !isLoopbackAddress(request.RemoteAddr)
	if remote {
		// Another computer reads the state; it has no business seeing the
		// broker password, and it cannot change anything anyway.
		settings.MQTT.Password = ""
	}
	payload := stateResponse{
		Hub:     s.hub.State(),
		Session: s.session.State(),
		Config: configResponse{
			Version:       s.config.Version,
			Language:      language,
			IntervalS:     s.settings.Interval().Seconds(),
			DataDir:       s.hub.StorageDir(),
			Platform:      platformLabel(),
			MQTTConnected: s.publisher.Connected(),
			Radios:        s.hub.Radios(),
			Settings:      settings,
			Remote:        remote,
			Listen:        s.config.Listen,
			Network:       s.config.Network,
		},
	}
	writeJSON(writer, payload)
}

// handleSettings reads or updates the configuration.
func (s *Server) handleSettings(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		writeJSON(writer, s.settings.View())
		return
	}
	var patch settingsPatch
	if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	view, before, after := s.settings.Update(patch)
	if before != after {
		log.Printf("refresh interval changed to %s", after)
	}
	writeJSON(writer, view)
}

// batteryState is one entry of the public read API.
type batteryState struct {
	Address    string  `json:"addr"`
	Name       string  `json:"name"`
	Model      string  `json:"model"`
	Brand      string  `json:"brand"`
	Protocol   string  `json:"protocol"`
	Online     bool    `json:"online"`
	LastSeen   string  `json:"lastSeen,omitempty"`
	LastSample string  `json:"lastSampleAt,omitempty"`
	Sample     *Sample `json:"sample,omitempty"`
}

// handleBatteries exposes the batteries with their newest reading — including
// batteries that are asleep right now, so integrations can still show the last
// known state and when it was measured.
func (s *Server) handleBatteries(writer http.ResponseWriter, _ *http.Request) {
	if !s.settings.APIEnabled() {
		http.Error(writer, "the read API is disabled in the settings", http.StatusForbidden)
		return
	}
	batteries := s.store.Batteries()
	response := struct {
		GeneratedAt string         `json:"generatedAt"`
		Count       int            `json:"count"`
		Batteries   []batteryState `json:"batteries"`
	}{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Count:       len(batteries),
		Batteries:   make([]batteryState, 0, len(batteries)),
	}
	for _, battery := range batteries {
		entry := batteryState{
			Address:  battery.Address,
			Name:     battery.Name,
			Model:    battery.Model,
			Brand:    battery.Brand,
			Protocol: battery.Protocol,
			Sample:   battery.LastSample,
			LastSeen: battery.SeenAt,
		}
		if info, ok := s.hub.Device(battery.Address); ok {
			entry.Online = info.Online
			entry.Protocol = nonEmpty(entry.Protocol, info.Protocol)
			entry.Name = nonEmpty(entry.Name, info.Name)
		}
		if battery.LastSample != nil {
			entry.LastSample = battery.LastSample.Timestamp.Format(time.RFC3339)
		}
		response.Batteries = append(response.Batteries, entry)
	}
	writeJSON(writer, response)
}

func (s *Server) handleSelect(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Address string `json:"addr"`
		Manual  *bool  `json:"manual"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	manual := true
	if body.Manual != nil {
		manual = *body.Manual
	}
	s.session.Select(body.Address, manual)
	writeJSON(writer, map[string]string{"status": "ok"})
}

func (s *Server) handleDeepScan(writer http.ResponseWriter, _ *http.Request) {
	s.hub.DeepScan()
	writeJSON(writer, map[string]string{"status": "ok"})
}

// handleRelease stops monitoring for a while so another device (a phone, a
// second computer) can connect to the battery.
func (s *Server) handleRelease(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Minutes float64 `json:"minutes"`
	}
	_ = json.NewDecoder(request.Body).Decode(&body)
	if body.Minutes <= 0 || body.Minutes > maxReleaseMins {
		body.Minutes = s.settings.ReleaseDuration().Minutes()
	}
	s.session.Release(time.Duration(body.Minutes * float64(time.Minute)))
	writeJSON(writer, map[string]float64{"releasedS": body.Minutes * 60, "minutes": body.Minutes})
}

// handleAdd lists (or removes) a device the user typed in by hand, so a battery
// that never shows up in a scan can still be monitored.
func (s *Server) handleAdd(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Address string `json:"addr"`
		Name    string `json:"name"`
		Remove  bool   `json:"remove"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	if body.Remove {
		if err := s.hub.RemoveManual(body.Address); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(writer, map[string]string{"status": "removed", "addr": strings.ToUpper(body.Address)})
		return
	}
	address, err := s.hub.AddManual(body.Address, body.Name)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(writer, map[string]string{"status": "added", "addr": address})
}

func (s *Server) handleWindow(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Action string `json:"action"`
		Edge   string `json:"edge"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "minimize", "maximize", "close", "drag", "resize":
		s.window.handleAction(body.Action, body.Edge)
		writeJSON(writer, map[string]string{"status": "ok"})
	default:
		http.Error(writer, "unknown window action", http.StatusBadRequest)
	}
}

// platformLabel describes the host in the JSON API, which is handy in bug
// reports coming from release builds.
func platformLabel() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// listenAddress turns the -listen value into something net.Listen understands.
func listenAddress(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "local", "localhost", "loopback":
		return "127.0.0.1"
	case "lan", "all", "any", "*", "network":
		return "0.0.0.0"
	}
	return strings.TrimSpace(value)
}

// networkURLs lists the addresses other computers can use, so the log always
// says what to type on the other machine.
func networkURLs(listen string, port int) []string {
	addresses := []string{}
	add := func(address string) {
		for _, known := range addresses {
			if known == address {
				return
			}
		}
		addresses = append(addresses, address)
	}
	listen = listenAddress(listen)
	if listen != "0.0.0.0" && listen != "::" {
		if !isLoopbackAddress(listen) {
			add(net.JoinHostPort(listen, strconv.Itoa(port)))
		}
		return addresses
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return addresses
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		assigned, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, entry := range assigned {
			ipnet, ok := entry.(*net.IPNet)
			if !ok {
				continue
			}
			address := ipnet.IP.To4()
			if address == nil || address.IsLoopback() || address.IsLinkLocalUnicast() {
				continue
			}
			add(net.JoinHostPort(address.String(), strconv.Itoa(port)))
		}
	}
	return addresses
}

func writeJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}
