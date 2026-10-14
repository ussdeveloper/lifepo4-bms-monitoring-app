package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// The MQTT publisher mirrors battery readings to a broker and (optionally)
// announces every battery to Home Assistant through MQTT discovery, so each
// battery shows up there as a device with its sensors.
type mqttPublisher struct {
	settings *Settings
	store    *Store
	version  string

	samples chan storeDevice
	mu      sync.Mutex
	client  mqtt.Client
	active  string // signature of the connection currently in use
	pubList string // signature of the discovered battery list

	availability      bool
	knownAvailability bool
}

func newMQTTPublisher(settings *Settings, store *Store, version string) *mqttPublisher {
	return &mqttPublisher{
		settings: settings,
		store:    store,
		version:  version,
		samples:  make(chan storeDevice, 64),
	}
}

// Sample queues a battery state for publication. It never blocks: MQTT must not
// slow down reading the batteries.
func (p *mqttPublisher) Sample(battery storeDevice) {
	select {
	case p.samples <- battery:
	default:
	}
}

// Run supervises the broker connection and publishes samples until the context
// is cancelled.
func (p *mqttPublisher) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.disconnect()
			return
		case battery := <-p.samples:
			p.publishState(battery)
		case <-ticker.C:
			p.sync()
		}
	}
}

// Connected reports whether the broker connection is up.
func (p *mqttPublisher) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client != nil && p.client.IsConnected()
}

// sync connects, disconnects or republishes discovery depending on the current
// settings and the known battery list.
func (p *mqttPublisher) sync() {
	config := p.settings.MQTT()
	signature := mqttSignature(config)
	if !config.Enabled || config.Host == "" {
		if p.client != nil {
			p.disconnect()
		}
		return
	}
	if p.client == nil || p.active != signature {
		p.disconnect()
		if err := p.connect(config); err != nil {
			log.Printf("MQTT: cannot connect to %s:%d: %v", config.Host, config.Port, err)
			return
		}
		log.Printf("MQTT: connected to %s:%d, topic prefix %q", config.Host, config.Port, config.TopicPrefix)
	}

	batteries := p.store.Batteries()
	listSignature := batteryListSignature(batteries)
	if config.Discovery && listSignature != p.pubList {
		p.publishDiscovery(config, batteries)
		p.pubList = listSignature
		// Publish what we already know, so Home Assistant gets values even when
		// the battery is asleep right now.
		for _, battery := range batteries {
			p.publishState(battery)
		}
	}
	p.setAvailability(config, true)
}

func (p *mqttPublisher) connect(config mqttSettings) error {
	options := mqtt.NewClientOptions()
	options.AddBroker(fmt.Sprintf("tcp://%s:%d", config.Host, config.Port))
	options.SetClientID("lifepo4-bms-monitor")
	if config.Username != "" {
		options.SetUsername(config.Username)
		options.SetPassword(config.Password)
	}
	options.SetAutoReconnect(true)
	options.SetConnectTimeout(5 * time.Second)
	options.SetKeepAlive(30 * time.Second)
	options.SetWill(p.topic(config, "status"), "offline", 1, true)

	client := mqtt.NewClient(options)
	token := client.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("connection timed out")
	}
	if err := token.Error(); err != nil {
		return err
	}
	p.mu.Lock()
	p.client = client
	p.active = mqttSignature(config)
	p.mu.Unlock()
	p.pubList = ""
	return nil
}

func (p *mqttPublisher) disconnect() {
	p.mu.Lock()
	client := p.client
	p.client = nil
	p.active = ""
	p.pubList = ""
	p.availability = false
	p.knownAvailability = false
	p.mu.Unlock()
	if client == nil {
		return
	}
	client.Disconnect(250)
}

func (p *mqttPublisher) publishState(battery storeDevice) {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()
	if client == nil || battery.LastSample == nil {
		return
	}
	config := p.settings.MQTT()
	payload, err := buildStatePayload(battery)
	if err != nil {
		log.Printf("MQTT: cannot build the payload for %s: %v", battery.Address, err)
		return
	}
	client.Publish(p.topic(config, mqttSlug(battery.Address)+"/state"), 0, true, payload)
}

func (p *mqttPublisher) publishDiscovery(config mqttSettings, batteries []storeDevice) {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()
	if client == nil {
		return
	}
	for _, battery := range batteries {
		for topic, payload := range buildDiscoveryPayloads(battery, config, p.version) {
			client.Publish(topic, 1, true, payload)
		}
	}
	log.Printf("MQTT: published Home Assistant discovery for %d batteries", len(batteries))
}

// setAvailability publishes the availability topic only when it changes.
func (p *mqttPublisher) setAvailability(config mqttSettings, online bool) {
	p.mu.Lock()
	client := p.client
	if client == nil || p.availability == online {
		p.mu.Unlock()
		return
	}
	p.availability = online
	p.knownAvailability = true
	p.mu.Unlock()
	payload := "offline"
	if online {
		payload = "online"
	}
	client.Publish(p.topic(config, "status"), 1, true, payload)
}

// topic builds a topic inside the configured prefix. An empty name returns the
// prefix itself.
func (p *mqttPublisher) topic(config mqttSettings, name string) string {
	if name == "" {
		return config.TopicPrefix
	}
	return config.TopicPrefix + "/" + name
}

func mqttSignature(config mqttSettings) string {
	return fmt.Sprintf("%v|%s|%d|%s|%s|%s|%v|%s",
		config.Enabled, config.Host, config.Port, config.Username, config.Password,
		config.TopicPrefix, config.Discovery, config.DiscoveryPrefix)
}

func batteryListSignature(batteries []storeDevice) string {
	parts := make([]string, 0, len(batteries))
	for _, battery := range batteries {
		parts = append(parts, battery.Address+"|"+battery.Name+"|"+battery.Model+"|"+battery.Brand)
	}
	sort.Strings(parts)
	return strings.Join(parts, "#")
}

// mqttSlug turns a Bluetooth address into a topic and object id fragment.
func mqttSlug(address string) string {
	return strings.ToLower(strings.NewReplacer(":", "", "-", "", " ", "").Replace(address))
}

// statePayload is the JSON document published for every reading. Home Assistant
// templates pick single fields out of it.
type statePayload struct {
	Address     string    `json:"address"`
	Name        string    `json:"name"`
	Model       string    `json:"model"`
	Brand       string    `json:"brand"`
	Protocol    string    `json:"protocol"`
	Timestamp   time.Time `json:"timestamp"`
	Voltage     float64   `json:"voltage"`
	Current     float64   `json:"current"`
	SoC         int       `json:"soc"`
	SoH         *int      `json:"soh"`
	RemainingAh float64   `json:"remaining_ah"`
	DesignAh    float64   `json:"design_ah"`
	Cycles      int       `json:"cycles"`
	Cells       int       `json:"cells"`
	CellMinV    float64   `json:"cell_min_v"`
	CellMaxV    float64   `json:"cell_max_v"`
	CellDeltaMv int       `json:"cell_delta_mv"`
	TempMinC    float64   `json:"temp_min_c"`
	TempMaxC    float64   `json:"temp_max_c"`
	Charging    bool      `json:"charging"`
	Discharging bool      `json:"discharging"`
	Balancing   bool      `json:"balancing"`
	Problem     uint32    `json:"problem"`
}

func buildStatePayload(battery storeDevice) ([]byte, error) {
	sample := battery.LastSample
	if sample == nil {
		return nil, fmt.Errorf("no reading available")
	}
	payload := statePayload{
		Address:     battery.Address,
		Name:        battery.Name,
		Model:       battery.Model,
		Brand:       battery.Brand,
		Protocol:    sample.Protocol,
		Timestamp:   sample.Timestamp,
		Voltage:     round(sample.VoltageV, 3),
		Current:     round(sample.CurrentA, 3),
		SoC:         sample.SoC,
		SoH:         sample.SoH,
		RemainingAh: round(sample.RemainingAh, 2),
		DesignAh:    round(sample.DesignAh, 2),
		Cycles:      sample.Cycles,
		Cells:       len(sample.CellsMv),
		Problem:     0,
	}
	if minimum := minCell(sample.CellsMv); minimum != nil {
		payload.CellMinV = round(float64(*minimum)/1000, 3)
	}
	if maximum := maxCell(sample.CellsMv); maximum != nil {
		payload.CellMaxV = round(float64(*maximum)/1000, 3)
	}
	if minimum, maximum := minCell(sample.CellsMv), maxCell(sample.CellsMv); minimum != nil && maximum != nil {
		payload.CellDeltaMv = *maximum - *minimum
	}
	if minimum := minTemp(sample.TemperaturesC); minimum != nil {
		payload.TempMinC = round(*minimum, 1)
	}
	if maximum := maxTemp(sample.TemperaturesC); maximum != nil {
		payload.TempMaxC = round(*maximum, 1)
	}
	if sample.ChargingMosfet != nil {
		payload.Charging = *sample.ChargingMosfet
	}
	if sample.DischargingMosfet != nil {
		payload.Discharging = *sample.DischargingMosfet
	}
	if sample.Balancer != nil {
		payload.Balancing = *sample.Balancer
	}
	if sample.ProblemCode != nil {
		payload.Problem = *sample.ProblemCode
	}
	return json.Marshal(payload)
}

// discoveryEntity describes one Home Assistant entity of a battery.
type discoveryEntity struct {
	component   string
	objectID    string
	name        string
	template    string
	unit        string
	deviceClass string
	icon        string
}

var discoveryEntities = []discoveryEntity{
	{component: "sensor", objectID: "soc", name: "State of charge", template: "{{ value_json.soc }}", unit: "%", deviceClass: "battery"},
	{component: "sensor", objectID: "voltage", name: "Voltage", template: "{{ value_json.voltage }}", unit: "V", deviceClass: "voltage"},
	{component: "sensor", objectID: "current", name: "Current", template: "{{ value_json.current }}", unit: "A", deviceClass: "current"},
	{component: "sensor", objectID: "remaining", name: "Remaining capacity", template: "{{ value_json.remaining_ah }}", unit: "Ah"},
	{component: "sensor", objectID: "design", name: "Design capacity", template: "{{ value_json.design_ah }}", unit: "Ah"},
	{component: "sensor", objectID: "cycles", name: "Cycles", template: "{{ value_json.cycles }}"},
	{component: "sensor", objectID: "cell_min", name: "Cell voltage minimum", template: "{{ value_json.cell_min_v }}", unit: "V", deviceClass: "voltage"},
	{component: "sensor", objectID: "cell_max", name: "Cell voltage maximum", template: "{{ value_json.cell_max_v }}", unit: "V", deviceClass: "voltage"},
	{component: "sensor", objectID: "cell_delta", name: "Cell voltage spread", template: "{{ value_json.cell_delta_mv }}", unit: "mV"},
	{component: "sensor", objectID: "temp_min", name: "Temperature minimum", template: "{{ value_json.temp_min_c }}", unit: "\u00b0C", deviceClass: "temperature"},
	{component: "sensor", objectID: "temp_max", name: "Temperature maximum", template: "{{ value_json.temp_max_c }}", unit: "\u00b0C", deviceClass: "temperature"},
	{component: "sensor", objectID: "problems", name: "Problem codes", template: "{{ value_json.problem }}"},
	{component: "binary_sensor", objectID: "charging", name: "Charging", template: "{{ value_json.charging }}", deviceClass: "battery_charging"},
	{component: "binary_sensor", objectID: "discharging", name: "Discharging", template: "{{ value_json.discharging }}", deviceClass: "power"},
	{component: "binary_sensor", objectID: "balancing", name: "Balancing", template: "{{ value_json.balancing }}"},
}

// buildDiscoveryPayloads returns topic -> payload for every Home Assistant
// discovery message of one battery.
func buildDiscoveryPayloads(battery storeDevice, config mqttSettings, version string) map[string][]byte {
	slug := mqttSlug(battery.Address)
	node := config.TopicPrefix + "_" + slug
	stateTopic := config.TopicPrefix + "/" + slug + "/state"
	availabilityTopic := config.TopicPrefix + "/status"
	name := strings.TrimSpace(battery.Name)
	if name == "" {
		name = "BMS " + battery.Address
	}

	device := map[string]any{
		"identifiers":  []string{node},
		"name":         name,
		"manufacturer": nonEmpty(battery.Brand, "LiFePO4 BMS Monitor"),
		"model":        nonEmpty(battery.Model, "Battery management system"),
		"sw_version":   version,
	}
	if battery.Address != "" {
		device["connections"] = [][]string{{"mac", strings.ToLower(battery.Address)}}
	}

	payloads := map[string][]byte{}
	for _, entity := range discoveryEntities {
		message := map[string]any{
			"name":                  entity.name,
			"unique_id":             node + "_" + entity.objectID,
			"object_id":             node + "_" + entity.objectID,
			"state_topic":           stateTopic,
			"value_template":        entity.template,
			"availability_topic":    availabilityTopic,
			"payload_available":     "online",
			"payload_not_available": "offline",
			"device":                device,
		}
		if entity.unit != "" {
			message["unit_of_measurement"] = entity.unit
		}
		if entity.deviceClass != "" {
			message["device_class"] = entity.deviceClass
		}
		if entity.component == "sensor" {
			message["state_class"] = "measurement"
		}
		data, err := json.Marshal(message)
		if err != nil {
			continue
		}
		topic := fmt.Sprintf("%s/%s/%s/%s/config",
			config.DiscoveryPrefix, entity.component, node, entity.objectID)
		payloads[topic] = data
	}
	return payloads
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func round(value float64, digits int) float64 {
	factor := 1.0
	for index := 0; index < digits; index++ {
		factor *= 10
	}
	return math.Round(value*factor) / factor
}
