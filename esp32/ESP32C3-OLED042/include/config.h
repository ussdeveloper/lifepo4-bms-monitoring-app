// Compile time defaults of the device: what it is called, which pins the OLED
// most likely uses and how the access point for the first setup looks.
#pragma once

#include <Arduino.h>

#define APP_TITLE "LiFePO4 BMS Monitor"
// Shown in the interface, in the MQTT discovery document and in the log.
// Every firmware change gets a new version and a build number: the interface
// prints both, so it is always obvious which build is running on the device.
#define APP_VERSION "esp32-0.2.1"
#define APP_BUILD 5
// The version and build history is in esp32/ESP32C3-OLED042/CHANGELOG.md.

// Preferences namespace in NVS: everything the user can change lives here, next
// to a small amount of state (the battery that was found last, the interface
// files version).
#define NVS_NAMESPACE "humsienk"

// Name of the device on the network and in Home Assistant.
#define MDNS_NAME "lifepo4-bms"
// The access point it starts for the first setup is called BATTERY-<number>:
// the number is drawn once and kept, so the network keeps its name across
// restarts while staying unique among neighbours.
#define ACCESS_POINT_PREFIX "BATTERY-"

// The 0.42" OLED is a 72x40 SSD1306 on I2C. Boards wire it to different pins
// (there is no standard), so the firmware probes these pairs at boot and uses
// the first one that answers. The first entry is the most common wiring of the
// ESP32-C3 0.42" boards.
struct I2cCandidate {
    uint8_t sda;
    uint8_t scl;
};

static const I2cCandidate I2C_CANDIDATES[] = {
    {5, 6}, {8, 9}, {4, 5}, {6, 7}, {3, 4}, {1, 2}, {0, 1}, {7, 8}, {2, 3}, {10, 1},
};
static const size_t I2C_CANDIDATE_COUNT = sizeof(I2C_CANDIDATES) / sizeof(I2C_CANDIDATES[0]);

// Address of the display; 0x3C is what almost every module uses, 0x3D the rest.
static const uint8_t DISPLAY_ADDRESSES[] = {0x3C, 0x3D};

// How often the battery is read and how long the device waits before it gives
// up on a connection (the BMS of this family is slow and sometimes sleeps).
static const uint32_t DEFAULT_INTERVAL_MS = 5000;
static const uint32_t MIN_INTERVAL_MS = 1000;
static const uint32_t MAX_INTERVAL_MS = 60000;
static const uint32_t BLE_CONNECT_TIMEOUT_MS = 15000;
static const uint32_t BLE_RETRY_MS = 5000;
// The ESP32-C3 has one radio for Wi-Fi and Bluetooth together, so the scanner
// must not hog it: a short scan with a long pause in between keeps the web
// interface responsive while the device looks for the battery.
static const uint32_t BLE_SCAN_SECONDS = 4;
static const uint32_t BLE_SCAN_PAUSE_MS = 3000;
// Listening is measured in units of 0.625 ms: the scanner is on for 75 ms of
// every 100 ms. The radio is shared with Wi-Fi, and a low share here is what
// makes a battery two metres away invisible: with Wi-Fi up, only the time the
// scan actually listens is left for hearing it.
static const uint16_t BLE_SCAN_INTERVAL = 160;   // 100 ms
static const uint16_t BLE_SCAN_WINDOW = 120;     // 75 ms

// The interface keeps a window of recent readings in memory for the charts: a
// device has no disk to write a long history to. The window is a ring, so it
// never grows; when memory gets tight it shrinks, oldest readings first.
static const size_t HISTORY_POINTS = 360;
static const uint32_t LOW_HEAP_BYTES = 28 * 1024;
static const size_t MIN_HISTORY_POINTS = 60;

// Over the air updates: the device answers both the Arduino IDE/PlatformIO
// protocol (port 3232) and a plain HTTP upload on /api/ota.
static const char *const DEFAULT_OTA_PASSWORD = "lifepo4bms";


