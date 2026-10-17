#include "appconfig.h"

#include <ArduinoJson.h>
#include <Preferences.h>

#include "config.h"
#include "log.h"

namespace {
Preferences preferences;
const char *const KEY_KNOWN = "batteries";
const char *const KEY_SSID = "wifiSsid";
const char *const KEY_PASS = "wifiPass";
const char *const KEY_INTERVAL = "intervalMs";
const char *const KEY_RELEASE_MIN = "releaseMin";
const char *const KEY_API = "apiEnabled";
const char *const KEY_DEBUG = "debugLog";
const char *const KEY_LANG = "language";
const char *const KEY_OTA = "otaPass";
const char *const KEY_BATTERY = "battery";
const char *const KEY_BATTERY_NAME = "batteryName";
const char *const KEY_SDA = "dispSda";
const char *const KEY_SCL = "dispScl";
const char *const KEY_DISPLAY = "dispAddr";
const char *const KEY_AP_NUMBER = "apNumber";
const char *const KEY_MQTT_ON = "mqttOn";
const char *const KEY_MQTT_HOST = "mqttHost";
const char *const KEY_MQTT_PORT = "mqttPort";
const char *const KEY_MQTT_USER = "mqttUser";
const char *const KEY_MQTT_PASS = "mqttPass";
const char *const KEY_MQTT_PREFIX = "mqttPrefix";
const char *const KEY_MQTT_DISCO = "mqttDisco";
const char *const KEY_MQTT_DISCO_PREFIX = "mqttDiscoPrefix";

// One line per battery that joins the list, so the log shows what the device
// remembers without digging in the flash.
void logNewBattery(const String &address, const String &name) {
    logLine("battery", "added to the permanent list: " + address +
                           (name.length() > 0 ? " (" + name + ")" : ""));
}
}  // namespace

void AppConfig::begin() {
    preferences.begin(NVS_NAMESPACE, false);
    load();
}

void AppConfig::load() {
    wifiSsid = preferences.getString(KEY_SSID, "");
    wifiPassword = preferences.getString(KEY_PASS, "");
    intervalMs = preferences.getUInt(KEY_INTERVAL, DEFAULT_INTERVAL_MS);
    if (intervalMs < MIN_INTERVAL_MS || intervalMs > MAX_INTERVAL_MS) {
        intervalMs = DEFAULT_INTERVAL_MS;
    }
    releaseMinutes = preferences.getUShort(KEY_RELEASE_MIN, 5);
    if (releaseMinutes < 1 || releaseMinutes > 120) {
        releaseMinutes = 5;
    }
    apiEnabled = preferences.getBool(KEY_API, true);
    debugLog = preferences.getBool(KEY_DEBUG, false);
    language = preferences.getString(KEY_LANG, "en");
    otaPassword = preferences.getString(KEY_OTA, DEFAULT_OTA_PASSWORD);
    batteryAddress = preferences.getString(KEY_BATTERY, "");
    batteryName = preferences.getString(KEY_BATTERY_NAME, "");
    displaySda = preferences.getUChar(KEY_SDA, 0xFF);
    displayScl = preferences.getUChar(KEY_SCL, 0xFF);
    displayAddress = preferences.getUChar(KEY_DISPLAY, 0x3C);
    accessPointNumber = preferences.getULong(KEY_AP_NUMBER, 0);
    mqtt.enabled = preferences.getBool(KEY_MQTT_ON, false);
    mqtt.host = preferences.getString(KEY_MQTT_HOST, "");
    mqtt.port = static_cast<uint16_t>(preferences.getUShort(KEY_MQTT_PORT, 1883));
    if (mqtt.port == 0) {
        mqtt.port = 1883;
    }
    mqtt.user = preferences.getString(KEY_MQTT_USER, "");
    mqtt.password = preferences.getString(KEY_MQTT_PASS, "");
    mqtt.prefix = preferences.getString(KEY_MQTT_PREFIX, "lifepo4bms");
    mqtt.discovery = preferences.getBool(KEY_MQTT_DISCO, true);
    mqtt.discoveryPrefix = preferences.getString(KEY_MQTT_DISCO_PREFIX, "homeassistant");

    // The batteries found so far: address and the name they reported.
    knownCount_ = 0;
    const bool hasStoredList = preferences.isKey(KEY_KNOWN);
    JsonDocument document;
    if (deserializeJson(document, preferences.getString(KEY_KNOWN, "[]")) ==
        DeserializationError::Ok) {
        for (JsonObject entry : document.as<JsonArray>()) {
            if (knownCount_ >= MAX_KNOWN_BATTERIES) {
                break;
            }
            const String address = String(entry["a"] | "");
            if (address.length() == 0) {
                continue;
            }
            known_[knownCount_].address = address;
            known_[knownCount_].name = String(entry["n"] | "");
            known_[knownCount_].cells = static_cast<uint8_t>(entry["c"] | 0);
            knownCount_++;
        }
    }
    // Settings written by an older version kept a single battery and no list at
    // all. Migrating is a one-time thing: an empty list is a real state (the
    // batteries were removed in the interface) and must not be undone here.
    if (!hasStoredList && batteryAddress.length() > 0) {
        rememberBattery(batteryAddress, batteryName);
    }
    // A selection that is not on the list would be searched for on every start
    // and never answer.
    if (batteryAddress.length() > 0 && !isKnownAddress(batteryAddress)) {
        batteryAddress = "";
        batteryName = "";
        save();
    }
}

bool AppConfig::isKnownAddress(const String &address) const {
    for (size_t index = 0; index < knownCount_; index++) {
        if (known_[index].address.equalsIgnoreCase(address)) {
            return true;
        }
    }
    return false;
}

bool AppConfig::rememberBattery(const String &address, const String &name) {
    if (address.length() == 0) {
        return false;
    }
    for (size_t index = 0; index < knownCount_; index++) {
        if (!known_[index].address.equalsIgnoreCase(address)) {
            continue;
        }
        // Known already: only the name can be new (the firmware reports it once
        // per connection), and only a real name replaces an empty one.
        if (name.length() > 0 && name != known_[index].name) {
            known_[index].name = name;
            saveKnown();
            return true;
        }
        return false;
    }
    if (knownCount_ >= MAX_KNOWN_BATTERIES) {
        // The list is full: the oldest entry gives way (the last one).
        for (size_t index = MAX_KNOWN_BATTERIES - 1; index > 0; index--) {
            known_[index] = known_[index - 1];
        }
        knownCount_ = MAX_KNOWN_BATTERIES - 1;
    } else {
        for (size_t index = knownCount_; index > 0; index--) {
            known_[index] = known_[index - 1];
        }
    }
    known_[0].address = address;
    known_[0].name = name;
    known_[0].cells = 0;
    known_[0].lastSeen = 0;
    knownCount_++;
    saveKnown();
    logNewBattery(address, name);
    return true;
}

bool AppConfig::setBatteryCells(const String &address, int cells) {
    if (cells <= 0 || cells > 24) {
        return false;
    }
    for (size_t index = 0; index < knownCount_; index++) {
        if (!known_[index].address.equalsIgnoreCase(address)) {
            continue;
        }
        if (known_[index].cells == static_cast<uint8_t>(cells)) {
            return false;
        }
        // Written to flash once per battery, not once per reading: the number
        // only changes when a different pack is connected.
        known_[index].cells = static_cast<uint8_t>(cells);
        saveKnown();
        return true;
    }
    return false;
}

bool AppConfig::forgetBattery(const String &address) {
    for (size_t index = 0; index < knownCount_; index++) {
        if (!known_[index].address.equalsIgnoreCase(address)) {
            continue;
        }
        for (size_t move = index; move + 1 < knownCount_; move++) {
            known_[move] = known_[move + 1];
        }
        knownCount_--;
        saveKnown();
        // The battery that was just forgotten must not stay selected, or the
        // device keeps trying to reach something that is no longer on the list
        // (it would look like the entry came back after a restart).
        if (batteryAddress.equalsIgnoreCase(address)) {
            batteryAddress = "";
            batteryName = "";
            save();
        }
        return true;
    }
    return false;
}

void AppConfig::touchBattery(const String &address) {
    for (size_t index = 0; index < knownCount_; index++) {
        if (known_[index].address.equalsIgnoreCase(address)) {
            known_[index].lastSeen = static_cast<uint32_t>(time(nullptr));
            return;
        }
    }
}

void AppConfig::promote(const String &address) {
    for (size_t index = 1; index < knownCount_; index++) {
        if (!known_[index].address.equalsIgnoreCase(address)) {
            continue;
        }
        const KnownBattery chosen = known_[index];
        for (size_t move = index; move > 0; move--) {
            known_[move] = known_[move - 1];
        }
        known_[0] = chosen;
        return;
    }
}

void AppConfig::saveKnown() {
    JsonDocument document;
    JsonArray array = document.to<JsonArray>();
    for (size_t index = 0; index < knownCount_; index++) {
        JsonObject entry = array.add<JsonObject>();
        entry["a"] = known_[index].address;
        entry["n"] = known_[index].name;
        entry["c"] = known_[index].cells;
    }
    String payload;
    serializeJson(document, payload);
    preferences.putString(KEY_KNOWN, payload);
}

void AppConfig::save() {
    preferences.putString(KEY_SSID, wifiSsid);
    preferences.putString(KEY_PASS, wifiPassword);
    preferences.putUInt(KEY_INTERVAL, intervalMs);
    preferences.putUShort(KEY_RELEASE_MIN, releaseMinutes);
    preferences.putBool(KEY_API, apiEnabled);
    preferences.putBool(KEY_DEBUG, debugLog);
    preferences.putString(KEY_LANG, language);
    preferences.putString(KEY_OTA, otaPassword);
    preferences.putString(KEY_BATTERY, batteryAddress);
    preferences.putString(KEY_BATTERY_NAME, batteryName);
    preferences.putUChar(KEY_SDA, displaySda);
    preferences.putUChar(KEY_SCL, displayScl);
    preferences.putUChar(KEY_DISPLAY, displayAddress);
    preferences.putULong(KEY_AP_NUMBER, accessPointNumber);
    preferences.putBool(KEY_MQTT_ON, mqtt.enabled);
    preferences.putString(KEY_MQTT_HOST, mqtt.host);
    preferences.putUShort(KEY_MQTT_PORT, mqtt.port);
    preferences.putString(KEY_MQTT_USER, mqtt.user);
    preferences.putString(KEY_MQTT_PASS, mqtt.password);
    preferences.putString(KEY_MQTT_PREFIX, mqtt.prefix);
    preferences.putBool(KEY_MQTT_DISCO, mqtt.discovery);
    preferences.putString(KEY_MQTT_DISCO_PREFIX, mqtt.discoveryPrefix);
    saveKnown();

    // The settings store is small and silently drops values it cannot fit, so
    // what was just written is read back once: a broker that stops answering
    // after a restart is impossible to diagnose from the outside otherwise.
    if (mqtt.host.length() > 0 && preferences.getString(KEY_MQTT_HOST, "") != mqtt.host) {
        logLine("cfg", "the MQTT settings did not fit in the settings store (" +
                           String(preferences.freeEntries()) + " entries free)");
    }
}
