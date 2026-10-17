#include "mqtt.h"

#include <ArduinoJson.h>
#include <PubSubClient.h>
#include <WiFi.h>
#include <math.h>

#include "config.h"
#include "log.h"

namespace {
WiFiClient wifiClient;
PubSubClient client(wifiClient);
const uint32_t RECONNECT_MS = 10000;

// One entry per Home Assistant entity, exactly as in mqtt.go of the desktop
// application: same names, same templates, same units.
struct DiscoveryEntity {
    const char *component;
    const char *objectId;
    const char *name;
    const char *valueTemplate;
    const char *unit;
    const char *deviceClass;
    const char *icon;
    // suggested_display_precision: without it Home Assistant shows a voltage as
    // "3 V" (its default for volts is zero decimals), which throws away what
    // the BMS actually measured.
    int precision;
    // A text sensor (a name, a state) must not announce state_class.
    bool numeric;
};

const DiscoveryEntity ENTITIES[] = {
    {"sensor", "soc", "State of charge", "{{ value_json.soc }}", "%", "battery", nullptr, 0, true},
    {"sensor", "voltage", "Voltage", "{{ value_json.voltage }}", "V", "voltage", nullptr, 3, true},
    {"sensor", "voltage_mv", "Voltage (mV)", "{{ value_json.voltage_mv }}", "mV", "voltage", nullptr, 0, true},
    {"sensor", "current", "Current", "{{ value_json.current }}", "A", "current", nullptr, 3, true},
    {"sensor", "current_ma", "Current (mA)", "{{ value_json.current_ma }}", "mA", "current", nullptr, 0, true},
    {"sensor", "remaining", "Remaining capacity", "{{ value_json.remaining_ah }}", "Ah", nullptr, nullptr, 2,
     true},
    {"sensor", "design", "Design capacity", "{{ value_json.design_ah }}", "Ah", nullptr, nullptr, 2, true},
    {"sensor", "cycles", "Cycles", "{{ value_json.cycles }}", nullptr, nullptr, nullptr, 0, true},
    {"sensor", "cell_min", "Cell voltage minimum", "{{ value_json.cell_min_v }}", "V", "voltage", nullptr, 3,
     true},
    {"sensor", "cell_max", "Cell voltage maximum", "{{ value_json.cell_max_v }}", "V", "voltage", nullptr, 3,
     true},
    {"sensor", "cell_delta", "Cell voltage spread", "{{ value_json.cell_delta_mv }}", "mV", nullptr, nullptr, 0,
     true},
    {"sensor", "balance_state", "Cell balance", "{{ value_json.balance_state }}", nullptr, nullptr,
     "mdi:scale-balance", 0, false},
    {"sensor", "balance_mv", "Cell balance window", "{{ value_json.balance_mv }}", "mV", nullptr, nullptr, 0,
     true},
    {"sensor", "temp_min", "Temperature minimum", "{{ value_json.temp_min_c }}", "\u00b0C", "temperature",
     nullptr, 1, true},
    {"sensor", "temp_max", "Temperature maximum", "{{ value_json.temp_max_c }}", "\u00b0C", "temperature",
     nullptr, 1, true},
    {"sensor", "problems", "Problem codes", "{{ value_json.problem }}", nullptr, nullptr,
     "mdi:alert-circle-outline", 0, true},
    {"sensor", "problem_hex", "Problem code (hex)", "{{ value_json.problem_hex }}", nullptr, nullptr,
     "mdi:alert-circle-outline", 0, false},
    {"sensor", "problem_text", "Problems", "{{ value_json.problem_text }}", nullptr, nullptr,
     "mdi:alert-circle-outline", 0, false},
    {"sensor", "problem_count", "Problem count", "{{ value_json.problem_count }}", nullptr, nullptr,
     "mdi:counter", 0, true},
    {"sensor", "eta", "Time to full or empty", "{{ value_json.eta_min }}", "min", "duration", nullptr, 0, true},
    {"binary_sensor", "charging", "Charging", "{{ value_json.charging }}", nullptr, "battery_charging",
     nullptr, 0, false},
    {"binary_sensor", "discharging", "Discharging", "{{ value_json.discharging }}", nullptr, "power", nullptr,
     0, false},
    {"binary_sensor", "balancing", "Balancing", "{{ value_json.balancing }}", nullptr, nullptr, nullptr, 0,
     false},
    {"binary_sensor", "problem_active", "Problem active", "{{ value_json.problem_active }}", nullptr, "problem",
     nullptr, 0, false},
};
const size_t ENTITY_COUNT = sizeof(ENTITIES) / sizeof(ENTITIES[0]);

// One sensor per cell, announced for as many cells as the battery reported the
// last time it answered. The names are here so the discovery and the payload
// agree on the key.
const char *const CELL_PREFIX = "cell_";
const char *const CELL_NAME_PREFIX = "Cell ";
const int MAX_DISCOVERED_CELLS = 24;

float minimumTemperature(const Sample &sample) {
    float value = sample.temperaturesC[0];
    for (int index = 1; index < sample.temperatureCount; index++) {
        if (sample.temperaturesC[index] < value) {
            value = sample.temperaturesC[index];
        }
    }
    return value;
}

float maximumTemperature(const Sample &sample) {
    float value = sample.temperaturesC[0];
    for (int index = 1; index < sample.temperatureCount; index++) {
        if (sample.temperaturesC[index] > value) {
            value = sample.temperaturesC[index];
        }
    }
    return value;
}
}  // namespace

String Mqtt::slug(const String &address) const {
    String result = address;
    result.toLowerCase();
    result.replace(":", "");
    result.replace("-", "");
    result.replace(" ", "");
    return result;
}

void Mqtt::begin(AppConfig *config, Bms *bms) {
    config_ = config;
    bms_ = bms;
    // The state document carries one key per cell and the discovery documents
    // list two availability topics, so the default buffer is not enough.
    client.setBufferSize(2048);
}

bool Mqtt::connected() const { return client.connected(); }

void Mqtt::connect() {
    if (config_ == nullptr || !config_->mqtt.enabled || config_->mqtt.host.length() == 0) {
        enabled_ = false;
        return;
    }
    enabled_ = true;
    const String clientId = "lifepo4-bms-" + slug(WiFi.macAddress());
    const String availabilityTopic = config_->mqtt.prefix + "/status";
    client.setServer(config_->mqtt.host.c_str(), config_->mqtt.port);
    client.setKeepAlive(30);

    const bool ok = (config_->mqtt.user.length() > 0)
                        ? client.connect(clientId.c_str(), config_->mqtt.user.c_str(),
                                         config_->mqtt.password.c_str(), availabilityTopic.c_str(),
                                         0, true, "offline")
                        : client.connect(clientId.c_str(), nullptr, nullptr,
                                         availabilityTopic.c_str(), 0, true, "offline");
    if (!ok) {
        logLine("mqtt", "cannot connect to " + config_->mqtt.host);
        nextAttemptAt_ = millis() + RECONNECT_MS;
        return;
    }
    logLine("mqtt", "connected to " + config_->mqtt.host);
    publishAvailability("online");
    // Nothing is known to be online yet: every remembered battery is marked
    // offline, and the loop publishes the one that answers right after.
    for (size_t index = 0; index < config_->knownCount(); index++) {
        publishBatteryAvailability(config_->known(index).address, false);
    }
    availabilityAddress_ = "";
    availabilityOnline_ = false;
}

void Mqtt::disconnect() {
    if (client.connected()) {
        publishAvailability("offline");
        client.disconnect();
    }
}

void Mqtt::loop() {
    if (config_ == nullptr) {
        return;
    }
    const String signature = config_->mqtt.host + ":" + String(config_->mqtt.port) +
                             config_->mqtt.prefix + (config_->mqtt.enabled ? "1" : "0");
    if (signature != activeSignature_) {
        // The settings changed: drop the old connection and build a new one.
        disconnect();
        activeSignature_ = signature;
        discoverySignature_ = "";
        nextAttemptAt_ = 0;
    }
    if (!config_->mqtt.enabled) {
        return;
    }
    if (client.connected()) {
        client.loop();
        // Discovery is published as soon as the broker answers and again
        // whenever the list of known batteries changes, so a battery stays in
        // Home Assistant even while it cannot be reached.
        if (config_->mqtt.discovery) {
            String signature;
            for (size_t index = 0; index < config_->knownCount(); index++) {
                signature += config_->known(index).address;
                signature += '|';
                signature += config_->known(index).name;
                signature += '|';
                signature += String(config_->known(index).cells);
                signature += ';';
            }
            if (signature != discoverySignature_) {
                discoverySignature_ = signature;
                publishDiscovery();
            }
        }
        // The battery reports itself online only while the BMS answers, so Home
        // Assistant shows a sleeping battery as unavailable instead of stale.
        // Addresses are compared by topic slug: the scan reports them in lower
        // case while the settings keep what the user typed.
        const String address = bms_ != nullptr ? bms_->address() : String();
        const bool online = bms_ != nullptr && bms_->connected();
        if (slug(address) != slug(availabilityAddress_) || online != availabilityOnline_) {
            if (availabilityAddress_.length() > 0 && slug(availabilityAddress_) != slug(address)) {
                publishBatteryAvailability(availabilityAddress_, false);
            }
            availabilityAddress_ = address;
            availabilityOnline_ = online;
            publishBatteryAvailability(address, online);
            logLine("mqtt", "battery " + (address.length() > 0 ? address : String("(none)")) + " " +
                                (online ? "online" : "offline"));
        }
        return;
    }
    if (static_cast<int32_t>(millis() - nextAttemptAt_) < 0) {
        return;
    }
    connect();
}

void Mqtt::publishAvailability(const char *state) {
    const String topic = config_->mqtt.prefix + "/status";
    client.publish(topic.c_str(), state, true);
}

void Mqtt::publish(const Monitor &monitor) {
    if (!client.connected() || monitor.sample.timestamp == 0) {
        return;
    }
    const String stateTopic = config_->mqtt.prefix + "/" + slug(monitor.address) + "/state";

    const Sample &sample = monitor.sample;
    int minimum = sample.cellsMv[0];
    int maximum = sample.cellsMv[0];
    for (int index = 0; index < sample.cellCount; index++) {
        if (sample.cellsMv[index] < minimum) minimum = sample.cellsMv[index];
        if (sample.cellsMv[index] > maximum) maximum = sample.cellsMv[index];
    }

    JsonDocument document;
    JsonObject root = document.to<JsonObject>();
    root["address"] = monitor.address;
    root["name"] = monitor.name.length() > 0 ? monitor.name : monitor.model;
    root["model"] = monitor.model;
    root["brand"] = monitor.brand;
    root["protocol"] = sample.protocol;
    // Three decimals everywhere a voltage is published: the BMS measures in
    // millivolts, and rounding a cell to one decimal turns 3.324 V into 3.3 V
    // (and 3.96 V into 4 V), which is useless for spotting a weak cell.
    root["voltage"] = serialized(String(sample.voltageV, 3));
    root["voltage_mv"] = static_cast<long>(lroundf(sample.voltageV * 1000.0f));
    root["current"] = serialized(String(sample.currentA, 3));
    root["current_ma"] = static_cast<long>(lroundf(sample.currentA * 1000.0f));
    root["soc"] = sample.soc;
    if (sample.hasSoh) {
        root["soh"] = sample.soh;
    }
    root["remaining_ah"] = serialized(String(sample.remainingAh, 2));
    root["design_ah"] = serialized(String(sample.designAh, 2));
    // Cycles is the counter the BMS itself keeps (analog frame, u16 at offset
    // 18): it is read, never computed here.
    root["cycles"] = sample.cycles;
    root["cells"] = sample.cellCount;
    root["cell_min_v"] = serialized(String(minimum / 1000.0, 3));
    root["cell_max_v"] = serialized(String(maximum / 1000.0, 3));
    root["cell_delta_mv"] = maximum - minimum;
    // One key per cell, so Home Assistant can chart every cell separately.
    for (int index = 0; index < sample.cellCount; index++) {
        const String key = String(CELL_PREFIX) + String(index + 1);
        root[key] = serialized(String(sample.cellsMv[index] / 1000.0, 3));
    }
    root["temp_min_c"] = serialized(String(minimumTemperature(sample), 1));
    root["temp_max_c"] = serialized(String(maximumTemperature(sample), 1));
    root["charging"] = sample.chargingMosfet;
    root["discharging"] = sample.dischargingMosfet;
    root["balancing"] = sample.balancer;
    // The code stays a number, but the names go with it: a bare 0x0C in the
    // log tells nobody which protection tripped.
    root["problem"] = sample.problemCode;
    char hex[11];
    snprintf(hex, sizeof(hex), "0x%08X", static_cast<unsigned>(sample.problemCode));
    root["problem_hex"] = hex;
    root["problem_text"] = Battery::problemSummary(sample.problemCode);
    root["problem_count"] = Battery::problemCount(sample.problemCode);
    root["problem_active"] = sample.problemCode != 0;
    // A problem is news: it goes out as its own message the moment it appears
    // or clears, so Home Assistant can notify on it, and the text is human
    // readable instead of a bit mask.
    if (sample.problemCode != lastProblemCode_ || !problemPublished_) {
        lastProblemCode_ = sample.problemCode;
        problemPublished_ = true;
        JsonDocument event;
        JsonObject message = event.to<JsonObject>();
        message["address"] = monitor.address;
        message["name"] = monitor.name.length() > 0 ? monitor.name : monitor.model;
        message["code"] = sample.problemCode;
        message["hex"] = hex;
        message["text"] = Battery::problemSummary(sample.problemCode);
        message["count"] = Battery::problemCount(sample.problemCode);
        message["active"] = sample.problemCode != 0;
        message["time"] = sample.timestamp;
        String body;
        serializeJson(event, body);
        const String topic = config_->mqtt.prefix + "/" + slug(monitor.address) + "/problems";
        client.publish(topic.c_str(), body.c_str(), false);
        logLine("mqtt", String("problem news: ") + hex + " " + Battery::problemSummary(sample.problemCode));
    }
    if (sample.etaS > 0) {
        root["eta_min"] = static_cast<int>(sample.etaS / 60);
    }
    if (sample.balance.available) {
        root["balance_mv"] = sample.balance.spreadMv;
        root["balance_state"] = sample.balance.state;
    }
    String payload;
    serializeJson(document, payload);
    // Retained, exactly as the desktop application publishes it: Home Assistant
    // then has the last reading right after a restart instead of waiting.
    if (!client.publish(stateTopic.c_str(), payload.c_str(), true)) {
        logLine("mqtt", "publishing the reading failed (" + String(payload.length()) + " bytes)");
    }
}

void Mqtt::publishDiscovery() {
    // Home Assistant gets every battery the device knows about, exactly as the
    // desktop application does: a battery that sleeps or is held by a phone
    // still shows up and simply reports itself as unavailable.
    int refused = 0;
    for (size_t index = 0; index < config_->knownCount(); index++) {
        const KnownBattery &entry = config_->known(index);
        const bool isCurrent = bms_ != nullptr && bms_->connected() &&
                               entry.address.equalsIgnoreCase(bms_->address());
        publishBatteryDiscovery(entry.address,
                                entry.name.length() > 0 ? entry.name : bms_->name(),
                                isCurrent ? bms_->brand() : "",
                                isCurrent ? bms_->model() : "", entry.cells, refused);
    }
    if (refused > 0) {
        logLine("mqtt", String(refused) +
                           " Home Assistant discovery documents were not accepted");
    } else {
        logLine("mqtt", "Home Assistant discovery published for " +
                            String(config_->knownCount()) + " batteries");
    }
}

void Mqtt::publishBatteryDiscovery(const String &address, const String &name,
                                   const String &brand, const String &model, int cells,
                                   int &refused) {
    const String slugAddress = slug(address);
    const String node = config_->mqtt.prefix + "_" + slugAddress;
    const String stateTopic = config_->mqtt.prefix + "/" + slugAddress + "/state";
    const String bridgeTopic = config_->mqtt.prefix + "/status";
    const String batteryTopic = config_->mqtt.prefix + "/" + slugAddress + "/status";
    const String label = name.length() > 0 ? name : ("BMS " + address);

    // Two availability topics: the bridge, which goes offline when the device
    // or the application stops, and the battery itself, which goes offline when
    // the BMS stops answering while the rest keeps working.
    auto addAvailability = [&](JsonObject message) {
        JsonArray availability = message["availability"].to<JsonArray>();
        for (const String *topic : {&bridgeTopic, &batteryTopic}) {
            JsonObject entry = availability.add<JsonObject>();
            entry["topic"] = *topic;
            entry["payload_available"] = "online";
            entry["payload_not_available"] = "offline";
        }
    };
    auto addDevice = [&](JsonObject message) {
        JsonObject device = message["device"].to<JsonObject>();
        device["identifiers"].to<JsonArray>().add(node);
        device["name"] = label;
        device["manufacturer"] = brand.length() > 0 ? brand : APP_TITLE;
        device["model"] = model.length() > 0 ? model : "Battery management system";
        device["sw_version"] = APP_VERSION;
        JsonArray connections = device["connections"].to<JsonArray>();
        JsonArray mac = connections.add<JsonArray>();
        String lowerAddress = address;
        lowerAddress.toLowerCase();
        mac.add("mac");
        mac.add(lowerAddress);
    };
    auto send = [&](const String &component, const String &objectId, JsonObject message) {
        String payload;
        serializeJson(message, payload);
        const String topic =
            config_->mqtt.discoveryPrefix + "/" + component + "/" + node + "/" + objectId + "/config";
        if (!client.publish(topic.c_str(), payload.c_str(), true)) {
            refused++;
            if (refused <= 2) {
                logLine("mqtt", "discovery " + objectId + " not accepted (" + String(payload.length()) +
                                    " bytes)");
            }
        }
    };

    for (size_t index = 0; index < ENTITY_COUNT; index++) {
        const DiscoveryEntity &entity = ENTITIES[index];
        JsonDocument document;
        JsonObject message = document.to<JsonObject>();
        message["name"] = entity.name;
        message["unique_id"] = node + "_" + entity.objectId;
        message["object_id"] = node + "_" + entity.objectId;
        message["state_topic"] = stateTopic;
        message["value_template"] = entity.valueTemplate;
        addAvailability(message);
        if (entity.unit != nullptr) {
            message["unit_of_measurement"] = entity.unit;
        }
        if (entity.deviceClass != nullptr) {
            message["device_class"] = entity.deviceClass;
        }
        if (String(entity.component) == "sensor" && entity.numeric) {
            message["state_class"] = "measurement";
        }
        if (entity.icon != nullptr) {
            message["icon"] = entity.icon;
        }
        // The number of decimals Home Assistant shows. Its own default for volts
        // is zero, which turned 3.451 V into "3 V"; three decimals is what the
        // BMS measures.
        if (entity.precision > 0 && entity.numeric) {
            message["suggested_display_precision"] = entity.precision;
        }
        addDevice(message);
        send(String(entity.component), String(entity.objectId), message);
    }

    // One sensor per cell. A battery that has not answered yet has no known
    // count, so nothing is announced until it does (the discovery is republished
    // as soon as the count is learned).
    const int announced = (cells > 0 && cells <= MAX_DISCOVERED_CELLS) ? cells : 0;
    for (int cell = 1; cell <= announced; cell++) {
        JsonDocument document;
        JsonObject message = document.to<JsonObject>();
        const String objectId = String(CELL_PREFIX) + String(cell);
        message["name"] = String(CELL_NAME_PREFIX) + String(cell) + " voltage";
        message["unique_id"] = node + "_" + objectId;
        message["object_id"] = node + "_" + objectId;
        message["state_topic"] = stateTopic;
        message["value_template"] = "{{ value_json." + objectId + " }}";
        addAvailability(message);
        message["unit_of_measurement"] = "V";
        message["device_class"] = "voltage";
        message["state_class"] = "measurement";
        message["suggested_display_precision"] = 3;        addDevice(message);
        send("sensor", objectId, message);
    }
}

void Mqtt::publishBatteryAvailability(const String &address, bool online) {
    if (address.length() == 0) {
        return;
    }
    const String topic = config_->mqtt.prefix + "/" + slug(address) + "/status";
    client.publish(topic.c_str(), online ? "online" : "offline", true);
}
