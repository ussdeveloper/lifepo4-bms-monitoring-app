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
	// online is the availability published per battery, seenAt when its last
	// reading arrived. A battery that stops answering is marked offline.
	online map[string]bool
	seenAt map[string]time.Time
	// problem is the last problem code published as news for a battery.
	problem      map[string]uint32
	problemKnown map[string]bool
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
	p.refreshAvailability(config, batteries)
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
	p.online = map[string]bool{}
	p.seenAt = map[string]time.Time{}
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
	p.publishProblemNews(config, client, battery)
	p.markOnline(config, client, battery.Address, true)
}

// publishProblemNews sends the problem code and its name as its own message
// whenever it changes, so Home Assistant can notify on a new fault instead of
// polling a sensor.
func (p *mqttPublisher) publishProblemNews(config mqttSettings, client mqtt.Client, battery storeDevice) {
	sample := battery.LastSample
	if sample == nil {
		return
	}
	code := uint32(0)
	if sample.ProblemCode != nil {
		code = *sample.ProblemCode
	}
	p.mu.Lock()
	if p.problem == nil {
		p.problem = map[string]uint32{}
	}
	if p.problemKnown == nil {
		p.problemKnown = map[string]bool{}
	}
	changed := !p.problemKnown[battery.Address] || p.problem[battery.Address] != code
	p.problem[battery.Address] = code
	p.problemKnown[battery.Address] = true
	p.mu.Unlock()
	if !changed {
		return
	}
	message := map[string]any{
		"address": battery.Address,
		"name":    nonEmpty(battery.Name, battery.Model),
		"code":    code,
		"hex":     fmt.Sprintf("0x%08X", code),
		"text":    problemSummary(code),
		"count":   problemCount(code),
		"active":  code != 0,
		"time":    sample.Timestamp,
	}
	body, err := json.Marshal(message)
	if err != nil {
		return
	}
	client.Publish(p.topic(config, mqttSlug(battery.Address)+"/problems"), 1, false, body)
	if code != 0 {
		log.Printf("MQTT: %s: problem %s (%s)", battery.Address, fmt.Sprintf("0x%08X", code), problemSummary(code))
	}
}

// markOnline publishes the per-battery availability topic, and only when it
// changes, so Home Assistant shows a battery that stopped answering as
// unavailable instead of holding on to the last reading.
func (p *mqttPublisher) markOnline(config mqttSettings, client mqtt.Client, address string, online bool) {
	if address == "" {
		return
	}
	p.mu.Lock()
	if p.online == nil {
		p.online = map[string]bool{}
	}
	if p.seenAt == nil {
		p.seenAt = map[string]time.Time{}
	}
	already := p.online[address]
	if online {
		p.seenAt[address] = time.Now()
	}
	if already == online {
		p.mu.Unlock()
		return
	}
	p.online[address] = online
	p.mu.Unlock()
	state := "offline"
	if online {
		state = "online"
	}
	client.Publish(p.topic(config, mqttSlug(address)+"/status"), 1, true, state)
}

// refreshAvailability marks batteries that stopped reporting as offline.
func (p *mqttPublisher) refreshAvailability(config mqttSettings, batteries []storeDevice) {
	p.mu.Lock()
	client := p.client
	stale := make([]string, 0, len(p.online))
	for address, online := range p.online {
		if !online {
			continue
		}
		if seen, known := p.seenAt[address]; known && time.Since(seen) > 60*time.Second {
			stale = append(stale, address)
		}
	}
	p.mu.Unlock()
	if client == nil {
		return
	}
	for _, address := range stale {
		p.markOnline(config, client, address, false)
	}
	// A battery removed from the list must not stay "online" in the broker.
	present := map[string]bool{}
	for _, battery := range batteries {
		present[battery.Address] = true
	}
	p.mu.Lock()
	gone := make([]string, 0)
	for address := range p.online {
		if !present[address] {
			gone = append(gone, address)
		}
	}
	p.mu.Unlock()
	for _, address := range gone {
		p.markOnline(config, client, address, false)
	}
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
		cells := 0
		if battery.LastSample != nil {
			cells = len(battery.LastSample.CellsMv)
		}
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%s|%d",
			battery.Address, battery.Name, battery.Model, battery.Brand, cells))
	}
	sort.Strings(parts)
	return strings.Join(parts, "#")
}

// mqttSlug turns a Bluetooth address into a topic and object id fragment.
func mqttSlug(address string) string {
	return strings.ToLower(strings.NewReplacer(":", "", "-", "", " ", "").Replace(address))
}

// statePayload is the JSON document published for every reading. Home Assistant
// templates pick single fields out of it. It is a map because the per-cell keys
// (cell_1, cell_2, …) depend on how many cells the battery has.
type statePayload map[string]any

// problemBit describes one protection bit of the status frame. The names are
// what the interface and Home Assistant show, so a code never reaches the user
// as a bare number.
type problemBit struct {
	mask uint32
	name string
}

var problemBits = []problemBit{
	{0x00000001, "cell over voltage"},
	{0x00000002, "cell under voltage"},
	{0x00000004, "pack over voltage"},
	{0x00000008, "pack under voltage"},
	{0x00000010, "charge over temperature"},
	{0x00000020, "discharge over temperature"},
	{0x00000040, "charge low temperature"},
	{0x00000080, "discharge low temperature"},
	{0x00000100, "charge over current"},
	{0x00000200, "discharge over current"},
	{0x00000400, "short circuit"},
	{0x00000800, "AFE error"},
	{0x00001000, "software lock"},
}

// problemNames lists the protections named by a problem code.
func problemNames(code uint32) []string {
	names := make([]string, 0, len(problemBits))
	for _, bit := range problemBits {
		if code&bit.mask != 0 {
			names = append(names, bit.name)
		}
	}
	return names
}

// problemSummary names the protections in a code, joined with ", ".
func problemSummary(code uint32) string {
	names := make([]string, 0, len(problemBits))
	for _, bit := range problemBits {
		if code&bit.mask != 0 {
			names = append(names, bit.name)
		}
	}
	if len(names) == 0 {
		if code == 0 {
			return "none"
		}
		return "unknown problem"
	}
	return strings.Join(names, ", ")
}

// problemCount is how many protections a code reports.
func problemCount(code uint32) int {
	count := 0
	known := uint32(0)
	for _, bit := range problemBits {
		known |= bit.mask
		if code&bit.mask != 0 {
			count++
		}
	}
	if code&^known != 0 {
		count++
	}
	return count
}

func buildStatePayload(battery storeDevice) ([]byte, error) {
	sample := battery.LastSample
	if sample == nil {
		return nil, fmt.Errorf("no reading available")
	}
	problem := uint32(0)
	if sample.ProblemCode != nil {
		problem = *sample.ProblemCode
	}
	payload := statePayload{
		"address":       battery.Address,
		"name":          battery.Name,
		"model":         battery.Model,
		"brand":         battery.Brand,
		"protocol":      sample.Protocol,
		"timestamp":     sample.Timestamp,
		"voltage":       round(sample.VoltageV, 3),
		"voltage_mv":    int(math.Round(sample.VoltageV * 1000)),
		"current":       round(sample.CurrentA, 3),
		"current_ma":    int(math.Round(sample.CurrentA * 1000)),
		"soc":           sample.SoC,
		"remaining_ah":  round(sample.RemainingAh, 2),
		"design_ah":     round(sample.DesignAh, 2),
		"cycles":        sample.Cycles,
		"cells":         len(sample.CellsMv),
		"problem":       problem,
		"problem_hex":   fmt.Sprintf("0x%08X", problem),
		"problem_text":  problemSummary(problem),
		"problem_count": problemCount(problem),
		// problem_active is what an automation should trigger on.
		"problem_active": problem != 0,
	}
	if sample.SoH != nil {
		payload["soh"] = *sample.SoH
	}
	// One key per cell, with the full millivolt resolution of the BMS: three
	// decimals is exactly what the reading carries, one decimal is not.
	for index, millivolts := range sample.CellsMv {
		payload[fmt.Sprintf("cell_%d", index+1)] = round(float64(millivolts)/1000, 3)
	}
	if minimum := minCell(sample.CellsMv); minimum != nil {
		payload["cell_min_v"] = round(float64(*minimum)/1000, 3)
	}
	if maximum := maxCell(sample.CellsMv); maximum != nil {
		payload["cell_max_v"] = round(float64(*maximum)/1000, 3)
	}
	if minimum, maximum := minCell(sample.CellsMv), maxCell(sample.CellsMv); minimum != nil && maximum != nil {
		payload["cell_delta_mv"] = *maximum - *minimum
	}
	if minimum := minTemp(sample.TemperaturesC); minimum != nil {
		payload["temp_min_c"] = round(*minimum, 1)
	}
	if maximum := maxTemp(sample.TemperaturesC); maximum != nil {
		payload["temp_max_c"] = round(*maximum, 1)
	}
	if sample.ChargingMosfet != nil {
		payload["charging"] = *sample.ChargingMosfet
	}
	if sample.DischargingMosfet != nil {
		payload["discharging"] = *sample.DischargingMosfet
	}
	if sample.Balancer != nil {
		payload["balancing"] = *sample.Balancer
	}
	if sample.EtaS > 0 {
		payload["eta_min"] = int(sample.EtaS / 60)
	}
	if sample.Balance != nil {
		payload["balance_mv"] = sample.Balance.SpreadMv
		payload["balance_state"] = sample.Balance.State
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
	// precision becomes suggested_display_precision. Home Assistant's own
	// default for volts is zero decimals, which turns 3.451 V into "3 V".
	precision int
	// numeric is false for a text sensor, which must not claim state_class.
	numeric bool
}

var discoveryEntities = []discoveryEntity{
	{component: "sensor", objectID: "soc", name: "State of charge", template: "{{ value_json.soc }}", unit: "%", deviceClass: "battery", numeric: true},
	{component: "sensor", objectID: "voltage", name: "Voltage", template: "{{ value_json.voltage }}", unit: "V", deviceClass: "voltage", precision: 3, numeric: true},
	{component: "sensor", objectID: "voltage_mv", name: "Voltage (mV)", template: "{{ value_json.voltage_mv }}", unit: "mV", deviceClass: "voltage", numeric: true},
	{component: "sensor", objectID: "current", name: "Current", template: "{{ value_json.current }}", unit: "A", deviceClass: "current", precision: 3, numeric: true},
	{component: "sensor", objectID: "current_ma", name: "Current (mA)", template: "{{ value_json.current_ma }}", unit: "mA", deviceClass: "current", numeric: true},
	{component: "sensor", objectID: "remaining", name: "Remaining capacity", template: "{{ value_json.remaining_ah }}", unit: "Ah", precision: 2, numeric: true},
	{component: "sensor", objectID: "design", name: "Design capacity", template: "{{ value_json.design_ah }}", unit: "Ah", precision: 2, numeric: true},
	{component: "sensor", objectID: "cycles", name: "Cycles", template: "{{ value_json.cycles }}", numeric: true},
	{component: "sensor", objectID: "cell_min", name: "Cell voltage minimum", template: "{{ value_json.cell_min_v }}", unit: "V", deviceClass: "voltage", precision: 3, numeric: true},
	{component: "sensor", objectID: "cell_max", name: "Cell voltage maximum", template: "{{ value_json.cell_max_v }}", unit: "V", deviceClass: "voltage", precision: 3, numeric: true},
	{component: "sensor", objectID: "cell_delta", name: "Cell voltage spread", template: "{{ value_json.cell_delta_mv }}", unit: "mV", numeric: true},
	{component: "sensor", objectID: "balance_state", name: "Cell balance", template: "{{ value_json.balance_state }}", icon: "mdi:scale-balance"},
	{component: "sensor", objectID: "balance_mv", name: "Cell balance window", template: "{{ value_json.balance_mv }}", unit: "mV", numeric: true},
	{component: "sensor", objectID: "temp_min", name: "Temperature minimum", template: "{{ value_json.temp_min_c }}", unit: "\u00b0C", deviceClass: "temperature", precision: 1, numeric: true},
	{component: "sensor", objectID: "temp_max", name: "Temperature maximum", template: "{{ value_json.temp_max_c }}", unit: "\u00b0C", deviceClass: "temperature", precision: 1, numeric: true},
	{component: "sensor", objectID: "problems", name: "Problem codes", template: "{{ value_json.problem }}", icon: "mdi:alert-circle-outline", numeric: true},
	{component: "sensor", objectID: "problem_hex", name: "Problem code (hex)", template: "{{ value_json.problem_hex }}", icon: "mdi:alert-circle-outline"},
	{component: "sensor", objectID: "problem_text", name: "Problems", template: "{{ value_json.problem_text }}", icon: "mdi:alert-circle-outline"},
	{component: "sensor", objectID: "problem_count", name: "Problem count", template: "{{ value_json.problem_count }}", icon: "mdi:counter", numeric: true},
	{component: "sensor", objectID: "eta", name: "Time to full or empty", template: "{{ value_json.eta_min }}", unit: "min", deviceClass: "duration", numeric: true},
	{component: "binary_sensor", objectID: "charging", name: "Charging", template: "{{ value_json.charging }}", deviceClass: "battery_charging"},
	{component: "binary_sensor", objectID: "discharging", name: "Discharging", template: "{{ value_json.discharging }}", deviceClass: "power"},
	{component: "binary_sensor", objectID: "balancing", name: "Balancing", template: "{{ value_json.balancing }}"},
	{component: "binary_sensor", objectID: "problem_active", name: "Problem active", template: "{{ value_json.problem_active }}", deviceClass: "problem"},
}

// buildDiscoveryPayloads returns topic -> payload for every Home Assistant
// discovery message of one battery.
func buildDiscoveryPayloads(battery storeDevice, config mqttSettings, version string) map[string][]byte {
	slug := mqttSlug(battery.Address)
	node := config.TopicPrefix + "_" + slug
	stateTopic := config.TopicPrefix + "/" + slug + "/state"
	// Two availability topics: the application itself (gone when it stops, via
	// the last will) and the battery (offline while the BMS does not answer).
	availability := []map[string]any{
		{
			"topic":                 config.TopicPrefix + "/status",
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
		{
			"topic":                 config.TopicPrefix + "/" + slug + "/status",
			"payload_available":     "online",
			"payload_not_available": "offline",
		},
	}
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

	// announce adds one entity message to the result.
	announce := func(component, objectID, label, template, unit, deviceClass, icon string, precision int, numeric bool) {
		message := map[string]any{
			"name":           label,
			"unique_id":      node + "_" + objectID,
			"object_id":      node + "_" + objectID,
			"state_topic":    stateTopic,
			"value_template": template,
			"availability":   availability,
			"device":         device,
		}
		if unit != "" {
			message["unit_of_measurement"] = unit
		}
		if deviceClass != "" {
			message["device_class"] = deviceClass
		}
		// A text sensor has no state_class; everything numeric here does.
		if component == "sensor" && numeric {
			message["state_class"] = "measurement"
		}
		if icon != "" {
			message["icon"] = icon
		}
		if precision > 0 && numeric {
			message["suggested_display_precision"] = precision
		}
		data, err := json.Marshal(message)
		if err != nil {
			return
		}
		payloads[fmt.Sprintf("%s/%s/%s/%s/config",
			config.DiscoveryPrefix, component, node, objectID)] = data
	}

	for _, entity := range discoveryEntities {
		announce(entity.component, entity.objectID, entity.name, entity.template,
			entity.unit, entity.deviceClass, entity.icon, entity.precision, entity.numeric)
	}

	// One sensor per cell: Home Assistant charts every cell on its own, with the
	// three decimals the BMS actually measures.
	if battery.LastSample != nil {
		for index := range battery.LastSample.CellsMv {
			objectID := fmt.Sprintf("cell_%d", index+1)
			announce("sensor", objectID, fmt.Sprintf("Cell %d voltage", index+1),
				"{{ value_json."+objectID+" }}", "V", "voltage", "", 3, true)
		}
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
