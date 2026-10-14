package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Settings holds the user configuration of the application. It is stored as
// settings.json next to the device list and can be changed at runtime from the
// settings panel in the interface.
type Settings struct {
	mutex sync.RWMutex
	path  string
	value settingsFile
}

type settingsFile struct {
	Version    int          `json:"version"`
	IntervalS  float64      `json:"intervalS"`
	APIEnabled bool         `json:"apiEnabled"`
	MQTT       mqttSettings `json:"mqtt"`
}

type mqttSettings struct {
	Enabled         bool   `json:"enabled"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	TopicPrefix     string `json:"topicPrefix"`
	Discovery       bool   `json:"discovery"`
	DiscoveryPrefix string `json:"discoveryPrefix"`
}

// Limits of the refresh interval (the interface offers a slider inside these
// bounds plus a free "custom" value).
const (
	minIntervalS     = 1.0
	maxIntervalS     = 3600.0
	defaultIntervalS = 5.0
)

func defaultSettings() settingsFile {
	return settingsFile{
		Version:    1,
		IntervalS:  defaultIntervalS,
		APIEnabled: true,
		MQTT: mqttSettings{
			Port:            1883,
			TopicPrefix:     "lifepo4bms",
			Discovery:       true,
			DiscoveryPrefix: "homeassistant",
		},
	}
}

// NewSettings loads settings.json from the data directory, creating defaults
// when the file does not exist yet.
func NewSettings(directory string, fallbackInterval time.Duration) *Settings {
	settings := &Settings{value: defaultSettings()}
	if fallbackInterval > 0 {
		settings.value.IntervalS = fallbackInterval.Seconds()
	}
	if directory == "" {
		return settings
	}
	settings.path = filepath.Join(directory, "settings.json")
	data, err := os.ReadFile(settings.path)
	if err != nil {
		return settings
	}
	var loaded settingsFile
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("ignoring unreadable %s: %v", settings.path, err)
		return settings
	}
	settings.value = sanitizeSettings(loaded)
	return settings
}

func sanitizeSettings(value settingsFile) settingsFile {
	defaults := defaultSettings()
	value.Version = defaults.Version
	if value.IntervalS < minIntervalS || value.IntervalS > maxIntervalS {
		value.IntervalS = defaults.IntervalS
	}
	if value.MQTT.Port < 1 || value.MQTT.Port > 65535 {
		value.MQTT.Port = defaults.MQTT.Port
	}
	value.MQTT.Host = strings.TrimSpace(value.MQTT.Host)
	value.MQTT.Username = strings.TrimSpace(value.MQTT.Username)
	value.MQTT.TopicPrefix = sanitizeTopicPart(value.MQTT.TopicPrefix, defaults.MQTT.TopicPrefix)
	value.MQTT.DiscoveryPrefix = sanitizeTopicPart(value.MQTT.DiscoveryPrefix, defaults.MQTT.DiscoveryPrefix)
	return value
}

// sanitizeTopicPart keeps only characters that are safe inside an MQTT topic.
func sanitizeTopicPart(value, fallback string) string {
	cleaned := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z':
			return character
		case character >= 'A' && character <= 'Z':
			return character
		case character >= '0' && character <= '9':
			return character
		case character == '-' || character == '_' || character == '/':
			return character
		}
		return -1
	}, strings.TrimSpace(value))
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

// Interval returns the configured refresh interval.
func (s *Settings) Interval() time.Duration {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return time.Duration(s.value.IntervalS * float64(time.Second))
}

// APIEnabled reports whether the public read API is switched on.
func (s *Settings) APIEnabled() bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.value.APIEnabled
}

// MQTT returns a copy of the MQTT configuration.
func (s *Settings) MQTT() mqttSettings {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.value.MQTT
}

// View is the JSON representation handed to the interface.
type settingsView struct {
	IntervalS  float64      `json:"intervalS"`
	MinS       float64      `json:"minIntervalS"`
	MaxS       float64      `json:"maxIntervalS"`
	APIEnabled bool         `json:"apiEnabled"`
	MQTT       mqttSettings `json:"mqtt"`
	Path       string       `json:"path"`
}

func (s *Settings) View() settingsView {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return settingsView{
		IntervalS:  s.value.IntervalS,
		MinS:       minIntervalS,
		MaxS:       maxIntervalS,
		APIEnabled: s.value.APIEnabled,
		MQTT:       s.value.MQTT,
		Path:       s.path,
	}
}

// settingsPatch carries the fields a settings update may change. Pointers make
// "not provided" distinguishable from "set to zero".
type settingsPatch struct {
	IntervalS  *float64      `json:"intervalS"`
	APIEnabled *bool         `json:"apiEnabled"`
	MQTT       *mqttSettings `json:"mqtt"`
}

// Update applies a patch, stores it and returns the resulting view together
// with the interval before and after the change.
func (s *Settings) Update(patch settingsPatch) (settingsView, time.Duration, time.Duration) {
	s.mutex.Lock()
	before := time.Duration(s.value.IntervalS * float64(time.Second))
	if patch.IntervalS != nil {
		interval := *patch.IntervalS
		if interval < minIntervalS {
			interval = minIntervalS
		}
		if interval > maxIntervalS {
			interval = maxIntervalS
		}
		s.value.IntervalS = interval
	}
	if patch.APIEnabled != nil {
		s.value.APIEnabled = *patch.APIEnabled
	}
	if patch.MQTT != nil {
		incoming := *patch.MQTT
		if incoming.Port == 0 {
			incoming.Port = s.value.MQTT.Port
		}
		if strings.TrimSpace(incoming.TopicPrefix) == "" {
			incoming.TopicPrefix = s.value.MQTT.TopicPrefix
		}
		if strings.TrimSpace(incoming.DiscoveryPrefix) == "" {
			incoming.DiscoveryPrefix = s.value.MQTT.DiscoveryPrefix
		}
		s.value.MQTT = sanitizeSettings(settingsFile{
			Version:   s.value.Version,
			IntervalS: s.value.IntervalS,
			MQTT:      incoming,
		}).MQTT
	}
	after := time.Duration(s.value.IntervalS * float64(time.Second))
	view := settingsView{
		IntervalS:  s.value.IntervalS,
		MinS:       minIntervalS,
		MaxS:       maxIntervalS,
		APIEnabled: s.value.APIEnabled,
		MQTT:       s.value.MQTT,
		Path:       s.path,
	}
	s.mutex.Unlock()
	s.save()
	return view, before, after
}

func (s *Settings) save() {
	if s.path == "" {
		return
	}
	s.mutex.RLock()
	data, err := json.MarshalIndent(s.value, "", "  ")
	s.mutex.RUnlock()
	if err != nil {
		return
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		log.Printf("cannot write %s: %v", temporary, err)
		return
	}
	if err := os.Rename(temporary, s.path); err != nil {
		log.Printf("cannot replace %s: %v", s.path, err)
	}
}
