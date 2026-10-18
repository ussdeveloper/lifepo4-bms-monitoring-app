// LiFePO4 BMS Monitor on an ESP32-C3 with a 0.42" OLED.
//
// The device is a second home for the same application: it reads the battery
// over Bluetooth LE, serves the same interface and the same API on port 80,
// publishes the same MQTT topics to Home Assistant, updates itself over the
// air, and shows what it is doing on the small display.
//
// Boot order matters: the configuration first (it decides whether the device
// joins a network or starts its own), then the display (so the state is visible
// while Wi-Fi comes up), then everything that needs the network.
#include <Arduino.h>
#include <LittleFS.h>
#include <esp_coexist.h>
#include <time.h>

#include "appconfig.h"
#include "battery.h"
#include "bms.h"
#include "config.h"
#include "display.h"
#include "log.h"
#include "mqtt.h"
#include "net.h"
#include "ota.h"
#include "stats.h"
#include "webui.h"

namespace {
// A counter that survives a software reset: a firmware that crashes while
// starting would otherwise restart forever and never be reachable over the air.
// After a few bad boots the pieces most likely to be at fault are skipped.
RTC_NOINIT_ATTR uint32_t bootGuardMagic;
RTC_NOINIT_ATTR uint32_t bootCount;
const uint32_t BOOT_GUARD_MAGIC = 0x424F4F54;  // "BOOT"
const uint32_t BOOT_GUARD_LIMIT = 5;

AppConfig config;
Network network;
Bms bms;
Display display;
Ota ota;
Mqtt mqtt;
WebUi web;
History history;
Stats stats;
Monitor monitor;

uint32_t lastReadingAt = 0;
uint32_t lastDisplayAt = 0;
uint32_t lastPublishAt = 0;
const uint32_t DISPLAY_INTERVAL_MS = 250;
// A heap this small cannot survive the next allocation: the device answers for
// a while and then stops somewhere it cannot recover from, so it restarts while
// it still can.
const uint32_t CRITICAL_HEAP_BYTES = 10 * 1024;
const uint32_t CRITICAL_HEAP_MS = 3UL * 60UL * 1000UL;
// How long the device has to keep running before the bad-boot counter is
// cleared.
const uint32_t HEALTHY_UPTIME_MS = 3UL * 60UL * 1000UL;

// The display redraws from the current state: the project keeps no other copy
// of it, so the screens can never show something that was true a minute ago.
Display::Status statusForDisplay() {
    Display::Status status;
    const NetworkStatus &networkStatus = network.status();
    status.accessPoint = networkStatus.accessPoint;
    status.wifiConnected = networkStatus.connected;
    status.ssid = networkStatus.ssid;
    status.ip = networkStatus.ip;
    status.accessPointSsid = networkStatus.accessPointSsid;
    status.accessPointPassword = networkStatus.accessPointPassword;
    status.accessPointClients = networkStatus.accessPointClients;
    status.wifiError = networkStatus.error;
    status.batteryConnected = bms.connected();
    status.takeover = bms.takeover();
    status.batteryName = bms.name();
    status.batteryError = bms.error();
    status.devicesSeen = bms.devicesSeen();
    status.releaseLeft = bms.releasedSeconds();
    return status;
}

void onSample(const Sample &sample) {
    // One reading, three consumers: the interface (through the monitor), the
    // chart window and MQTT.
    monitor.connected = true;
    monitor.sample = sample;
    monitor.samples = bms.samples();
    monitor.updatedAt = millis();
    monitor.address = bms.address();
    monitor.name = bms.name();
    monitor.model = bms.model();
    monitor.brand = bms.brand();
    monitor.protocol = bms.protocol();
    // The cell count is remembered with the battery, so its per-cell sensors in
    // Home Assistant exist even while the BMS is asleep.
    config.setBatteryCells(monitor.address, sample.cellCount);
    const float eta = history.estimate(sample);
    monitor.sample.etaS = eta;
    history.add(sample);
    // The same reading also goes to the statistics in flash, where it stays
    // after a restart; the window in memory only covers the last minutes.
    if (history.count() > 0) {
        stats.record(history.at(history.count() - 1));
    }
    lastReadingAt = millis();
    lastPublishAt = millis();
    mqtt.publish(monitor);
}

void onStateChange() {
    monitor.connected = bms.connected();
    if (!monitor.connected) {
        // Keep the last reading: the interface shows it as the last known
        // state, exactly like the desktop application does when the battery
        // sleeps or another device took it.
        monitor.updatedAt = millis();
    }
}

void setupClock() {
    // The timestamps of the readings and the charts are real times, so the
    // device asks for the time once it is on a network. Asking again every
    // second would only fill the log.
    static bool asked = false;
    static bool reported = false;
    if (asked && reported) {
        return;
    }
    if (!asked) {
        configTime(0, 0, "pool.ntp.org", "time.nist.gov");
        logLine("time", "asking the network for the time");
        asked = true;
        return;
    }
    if (time(nullptr) > 1000000) {
        reported = true;
        logLine("time", "the clock is set");
    }
}
// guardMemory keeps the chart window in check and, if the heap stays critically
// low, restarts the device: a restart that is logged is much better than a
// device that stops answering and needs the cable.
void guardMemory() {
    static uint32_t lastCheck = 0;
    static uint32_t lowSince = 0;
    if (millis() - lastCheck < 5000) {
        return;
    }
    lastCheck = millis();
    if (history.guardMemory()) {
        logLine("mem", "little memory left (" + String(ESP.getFreeHeap() / 1024) +
                           " KB), chart window is now " + String(history.capacity()) +
                           " readings, oldest dropped");
    }
    const uint32_t free = ESP.getFreeHeap();
    if (free < CRITICAL_HEAP_BYTES) {
        if (lowSince == 0) {
            lowSince = millis();
            logLine("mem", "free memory fell to " + String(free / 1024) + " KB, watching");
        } else if (millis() - lowSince > CRITICAL_HEAP_MS) {
            logLine("mem", "free memory stayed under " + String(CRITICAL_HEAP_BYTES / 1024) +
                               " KB for " + String(CRITICAL_HEAP_MS / 60000) +
                               " minutes, restarting");
            delay(300);
            ESP.restart();
        }
    } else {
        lowSince = 0;
    }
    if (free < LOW_HEAP_BYTES) {
        static uint32_t lastWarn = 0;
        if (millis() - lastWarn > 60000) {
            lastWarn = millis();
            logLine("mem", "free memory is " + String(free / 1024) + " KB (minimum seen " +
                               String(ESP.getMinFreeHeap() / 1024) + " KB)");
        }
    }
}
// heartbeat prints one line a minute so the log shows whether the main loop is
// still running, and how much memory is left while it does.
void heartbeat() {
    static uint32_t last = 0;
    if (millis() - last < 60000) {
        return;
    }
    last = millis();
    const NetworkStatus &networkStatus = network.status();
    logLine("beat", String(millis() / 1000) + " s, " + String(ESP.getFreeHeap() / 1024) +
                        " KB free, chart " + String(history.count()) + "/" +
                        String(history.capacity()) + ", clients " +
                        String(networkStatus.accessPointClients) + ", wifi " +
                        (networkStatus.connected ? networkStatus.ip
                                                 : networkStatus.accessPointSsid));
}
}  // namespace

void setup() {
    Serial.begin(115200);
    delay(200);
    Serial.println();
    Serial.println(APP_TITLE);
    Serial.println(APP_VERSION);

    // How many times the device has started without managing to stay up for a
    // few minutes. A firmware that crashes while starting would restart forever
    // and could only be fixed with the cable; after a few bad boots the flash
    // statistics are skipped so the rest can come up and be updated over the
    // air.
    if (bootGuardMagic != BOOT_GUARD_MAGIC) {
        bootGuardMagic = BOOT_GUARD_MAGIC;
        bootCount = 0;
    }
    bootCount++;
    const bool unsafeBoot = bootCount > BOOT_GUARD_LIMIT;

    config.begin();
    logSetVerbose(config.debugLog);
    logLine("boot", "starting " + String(APP_VERSION) + " build " + String(APP_BUILD));
    if (unsafeBoot) {
        logLine("boot", "starting for the " + String(bootCount) +
                            " time without staying up: statistics and Bluetooth are skipped");
    }

    // The display first: if the Wi-Fi takes a while, the reason is already
    // visible on the device.
    display.begin(&config);
    display.draw(statusForDisplay(), monitor.sample, false);

    if (!LittleFS.begin(true)) {
        logLine("boot", "the interface files are missing, run pio run -t uploadfs");
    } else {
        logLine("boot", String(LittleFS.totalBytes() / 1024) + " KB filesystem, " +
                            String(Stats::freeFlash() / 1024) + " KB free");
    }
    // The readings that outlive a restart: the file is a ring of a fixed size,
    // so it can never take the space the interface needs.
    if (!unsafeBoot) {
        stats.begin();
    }

    // Bluetooth before Wi-Fi: the ESP32-C3 has one radio for both, and the
    // coexistence module has to be told about Bluetooth before the station
    // starts connecting — enabling Bluetooth afterwards aborts the device.
    // Skipped after repeated bad boots: a device without Bluetooth still serves
    // the interface and can be updated, which is what matters then.
    if (!unsafeBoot) {
        bms.begin(&config);
        bms.onSample = onSample;
        bms.onState = onStateChange;
    }
    logLine("boot", String(ESP.getFreeHeap() / 1024) + " KB free after Bluetooth");

    network.begin(&config);
    logLine("boot", String(ESP.getFreeHeap() / 1024) + " KB free after Wi-Fi");

    // The radio is shared, and this board runs from a power supply: Bluetooth
    // gets priority. Without it the Wi-Fi side wins the arbitration often
    // enough that a battery at the edge of the range is never heard.
    esp_coex_preference_set(ESP_COEX_PREFER_BT);
    mqtt.begin(&config, &bms);
    web.begin(&config, &network, &bms, &display, &history, &stats, &monitor);
    ota.begin(&config);
    logLine("boot", String(ESP.getFreeHeap() / 1024) + " KB free, ready");

    display.draw(statusForDisplay(), monitor.sample, false);
}

void loop() {
    network.loop();
    // While the setup access point is up, Bluetooth stays quiet: one radio
    // serves both, and the access point has to be easy to find. The same during
    // an update, which needs the radio to itself to be quick.
    bms.pause(network.status().accessPoint || web.updating());
    web.loop();
    mqtt.loop();
    web.noteMqttConnection(mqtt.connected());
    ota.loop();
    // The statistics are written in batches: the flash is not touched for every
    // single reading.
    stats.loop();

    if (network.status().connected) {
        setupClock();
    }

    // The display is the slowest part: redrawing it ten times a second would
    // cost more than everything else together. A draw that takes absurdly long
    // means the display (or its bus) is stuck, and it is switched off rather
    // than blocking everything else.
    if (millis() - lastDisplayAt >= DISPLAY_INTERVAL_MS) {
        lastDisplayAt = millis();
        const uint32_t before = millis();
        display.draw(statusForDisplay(), monitor.sample, monitor.sample.timestamp > 0);
        const uint32_t spent = millis() - before;
        if (spent > 2000) {
            display.giveUp("a redraw took more than two seconds");
        }
    }

    guardMemory();
    heartbeat();

    // The device stayed up long enough to be trusted again: a later restart
    // starts counting from zero instead of skipping half the firmware.
    if (bootCount > 0 && millis() > HEALTHY_UPTIME_MS) {
        bootCount = 0;
    }

    // MQTT is published per reading; if the broker was away, send the last
    // reading again once it comes back.
    if (mqtt.connected() && monitor.sample.timestamp > 0 && millis() - lastPublishAt > 60000) {
        lastPublishAt = millis();
        mqtt.publish(monitor);
    }
    delay(2);
}
