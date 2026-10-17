// Everything the user can change, stored in NVS (survives reboots and firmware
// updates) and edited from the same settings panel as the desktop application.
#pragma once

#include <Arduino.h>

struct MqttSettings {
    bool enabled = false;
    String host;
    uint16_t port = 1883;
    String user;
    String password;
    String prefix = "lifepo4bms";
    bool discovery = true;
    String discoveryPrefix = "homeassistant";
};

// A battery the device has found at least once. The list is kept in flash, so
// the same batteries are looked for after every restart and stay on the list
// even while they sleep or another device holds them.
struct KnownBattery {
    String address;
    String name;
    // How many cells the battery reported the last time it answered: the
    // per-cell sensors in Home Assistant are announced from this number.
    uint8_t cells = 0;
    uint32_t lastSeen = 0;  // seconds since the epoch, RAM only
};

static const size_t MAX_KNOWN_BATTERIES = 8;

class AppConfig {
public:
    void begin();
    void save();

    // Wi-Fi the device should join; empty means "start the access point".
    String wifiSsid;
    String wifiPassword;

    // How often the battery is read, in milliseconds.
    uint32_t intervalMs = 5000;

    // How long the unplug button frees the battery for another device, in
    // minutes. The default every release uses when it is not told otherwise.
    uint16_t releaseMinutes = 5;

    // Public read API (GET /api/batteries).
    bool apiEnabled = true;

    // Verbose serial log; the device counterpart of the desktop console switch.
    bool debugLog = false;

    // Interface language default, "en" or "pl".
    String language = "en";

    // Password for the over the air update endpoints.
    String otaPassword;

    // Broker and Home Assistant discovery.
    MqttSettings mqtt;

    // The battery that was read last, so the device reconnects to it directly
    // instead of scanning everything again.
    String batteryAddress;
    String batteryName;

    // Every battery ever found, newest first; the first one is preferred.
    size_t knownCount() const { return knownCount_; }
    const KnownBattery &known(size_t index) const { return known_[index]; }
    // rememberBattery adds a battery or updates its name; true when the stored
    // list changed (and was written to flash).
    bool rememberBattery(const String &address, const String &name = "");
    // forgetBattery drops one entry (used by the interface).
    bool forgetBattery(const String &address);
    // touchBattery notes that a battery answered just now (memory only).
    void touchBattery(const String &address);
    // isKnownAddress tells whether this address is on the list already.
    bool isKnownAddress(const String &address) const;
    // setBatteryCells remembers how many cells the battery reported, so Home
    // Assistant gets one sensor per cell even while it is asleep.
    bool setBatteryCells(const String &address, int cells);
    // promote moves a battery to the front, so it is tried first.
    void promote(const String &address);

    // Pins of the OLED that answered at boot (0xFF = not found yet).
    uint8_t displaySda = 0xFF;
    uint8_t displayScl = 0xFF;
    uint8_t displayAddress = 0x3C;

    // The number in the name of the access point (BATTERY-1234), drawn once.
    uint32_t accessPointNumber = 0;

    // True after the factory reset helper wiped the settings.
    bool isConfigured() const { return wifiSsid.length() > 0; }

private:
    void load();
    void saveKnown();

    KnownBattery known_[MAX_KNOWN_BATTERIES];
    size_t knownCount_ = 0;
};
