package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"
)

//go:embed ui/*
var uiFiles embed.FS

// serverConfig carries the settings the interface reads once at start-up.
type serverConfig struct {
	Language string
	Version  string
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
	server.mux.HandleFunc("/api/window", server.handleWindow)
	server.mux.HandleFunc("/api/settings", server.handleSettings)
	server.mux.HandleFunc("/api/batteries", server.handleBatteries)
	return server
}

func (s *Server) Handler() http.Handler { return s.mux }

type configResponse struct {
	Version       string       `json:"version"`
	Language      string       `json:"language"`
	IntervalS     float64      `json:"intervalS"`
	DataDir       string       `json:"dataDir"`
	Platform      string       `json:"platform"`
	MQTTConnected bool         `json:"mqttConnected"`
	Settings      settingsView `json:"settings"`
}

type stateResponse struct {
	Hub     HubState       `json:"hub"`
	Session SessionState   `json:"session"`
	Config  configResponse `json:"config"`
}

func (s *Server) handleState(writer http.ResponseWriter, _ *http.Request) {
	language := strings.ToLower(strings.TrimSpace(s.config.Language))
	if language != "pl" {
		language = "en"
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
			Settings:      s.settings.View(),
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

func (s *Server) handleWindow(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "minimize", "maximize", "close", "drag":
		s.window.handleAction(body.Action)
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

func writeJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}
