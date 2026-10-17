#include "webui.h"

#include <LittleFS.h>
#include <Update.h>

#include "config.h"
#include "log.h"

namespace {
const uint32_t POWER_WINDOW_S = 600;  // the power graph shows ten minutes
const uint32_t HISTORY_MAX_POINTS = 600;

String contentTypeOf(const String &path) {
    if (path.endsWith(".html")) return "text/html; charset=utf-8";
    if (path.endsWith(".js")) return "application/javascript; charset=utf-8";
    if (path.endsWith(".css")) return "text/css; charset=utf-8";
    if (path.endsWith(".json")) return "application/json; charset=utf-8";
    if (path.endsWith(".svg")) return "image/svg+xml";
    if (path.endsWith(".ico")) return "image/x-icon";
    return "text/plain; charset=utf-8";
}
}  // namespace

String WebUi::isoTime(uint32_t seconds) const {
    if (seconds == 0) {
        return "1970-01-01T00:00:00Z";
    }
    time_t raw = static_cast<time_t>(seconds);
    struct tm parts;
    gmtime_r(&raw, &parts);
    char buffer[24];
    strftime(buffer, sizeof(buffer), "%Y-%m-%dT%H:%M:%SZ", &parts);
    return String(buffer);
}

void WebUi::begin(AppConfig *config, Network *network, Bms *bms, Display *display, History *history,
                  Stats *stats, Monitor *monitor) {
    config_ = config;
    network_ = network;
    bms_ = bms;
    display_ = display;
    history_ = history;
    stats_ = stats;
    monitor_ = monitor;

    server_.on("/api/state", HTTP_GET, [this]() { handleState(); });
    server_.on("/api/batteries", HTTP_GET, [this]() { handleBatteries(); });
    server_.on("/api/settings", HTTP_GET, [this]() { handleSettings(); });
    server_.on("/api/settings", HTTP_POST, [this]() { handleSettings(); });
    server_.on("/api/select", HTTP_POST, [this]() { handleSelect(); });
    server_.on("/api/deepscan", HTTP_POST, [this]() { handleDeepScan(); });
    server_.on("/api/connect", HTTP_POST, [this]() { handleConnect(); });
    server_.on("/api/release", HTTP_POST, [this]() { handleRelease(); });
    server_.on("/api/add", HTTP_POST, [this]() { handleAdd(); });
    server_.on("/api/window", HTTP_POST, [this]() { handleWindow(); });
    server_.on("/api/cells/history", HTTP_GET, [this]() { handleCellHistory(); });
    server_.on("/api/stats/clear", HTTP_POST, [this]() { handleStatsClear(); });
    server_.on("/api/log", HTTP_GET, [this]() { handleLog(); });
    server_.on("/api/wifi", HTTP_GET, [this]() { handleWifi(); });
    server_.on("/api/wifi", HTTP_POST, [this]() { handleWifiSave(); });
    server_.on("/api/wifi/forget", HTTP_POST, [this]() { handleWifiForget(); });
    server_.on("/api/ota", HTTP_POST, [this]() { handleOta(); },
               [this]() {
                   HTTPUpload &upload = server_.upload();
                   // ?fs=1 writes the interface partition instead of the
                   // application: the same upload, so the files can be updated
                   // over the air too and the device never needs the cable.
                   const bool filesystem = server_.hasArg("fs") && server_.arg("fs") == "1";
                   if (upload.status == UPLOAD_FILE_START) {
                       filesystemUpload_ = filesystem;
                       logLine("ota", String("receiving ") + (filesystem ? "the interface" : "firmware") +
                                          " " + upload.filename);
                       if (filesystem) {
                           // The filesystem is unmounted while its partition is
                           // being rewritten; a restart remounts it.
                           LittleFS.end();
                       }
                       const int command = filesystem ? U_SPIFFS : U_FLASH;
                       if (!Update.begin(UPDATE_SIZE_UNKNOWN, command)) {
                           Update.printError(Serial);
                       }
                   } else if (upload.status == UPLOAD_FILE_WRITE) {
                       if (Update.write(upload.buf, upload.currentSize) != upload.currentSize) {
                           Update.printError(Serial);
                       }
                   } else if (upload.status == UPLOAD_FILE_END) {
                       if (Update.end(true)) {
                           logLine("ota", String(filesystemUpload_ ? "interface" : "firmware") +
                                              " written, restarting");
                       } else {
                           Update.printError(Serial);
                       }
                   }
               });
    server_.on("/api/reboot", HTTP_POST, [this]() { handleReboot(); });

    // The interface itself: the same three files as the desktop application.
    // They are registered one by one, so a missing file is an honest 404 and
    // not the interface served in its place.
    server_.serveStatic("/", LittleFS, "/index.html");
    server_.serveStatic("/index.html", LittleFS, "/index.html");
    server_.serveStatic("/app.js", LittleFS, "/app.js");
    server_.serveStatic("/style.css", LittleFS, "/style.css");

    server_.onNotFound([this]() { handleNotFound(); });
    server_.begin();
    logLine("http", "interface on port 80");
}

void WebUi::loop() { server_.handleClient(); }

void WebUi::addBalanceJson(JsonObject target, const Balance &balance) const {
    if (!balance.available) {
        return;
    }
    target["spreadMv"] = balance.spreadMv;
    target["zone"] = balance.zone;
    target["state"] = balance.state;
    target["toleranceMv"] = balance.toleranceMv;
    target["baseMv"] = balance.baseMv;
    target["adjustMv"] = balance.adjustMv;
    target["newPack"] = balance.newPack;
    target["largeCells"] = balance.largeCells;
    target["centreMv"] = balance.centreMv;
    target["lowMv"] = balance.lowMv;
    target["highMv"] = balance.highMv;
    target["weak"] = balance.weak;
    target["strong"] = balance.strong;
}

void WebUi::addSampleJson(JsonObject target, const Sample &sample) const {
    target["protocol"] = sample.protocol;
    target["voltageV"] = serialized(String(sample.voltageV, 3));
    target["currentA"] = serialized(String(sample.currentA, 3));
    target["soc"] = sample.soc;
    if (sample.hasSoh) {
        target["soh"] = sample.soh;
    }
    target["remainingAh"] = serialized(String(sample.remainingAh, 3));
    target["designAh"] = serialized(String(sample.designAh, 3));
    target["cycles"] = sample.cycles;
    JsonArray cells = target["cellsMv"].to<JsonArray>();
    for (int index = 0; index < sample.cellCount; index++) {
        cells.add(sample.cellsMv[index]);
    }
    JsonArray temperatures = target["temperaturesC"].to<JsonArray>();
    for (int index = 0; index < sample.temperatureCount; index++) {
        temperatures.add(serialized(String(sample.temperaturesC[index], 1)));
    }
    target["chargingMosfet"] = sample.chargingMosfet;
    target["dischargingMosfet"] = sample.dischargingMosfet;
    target["balancer"] = sample.balancer;
    target["problemCode"] = sample.problemCode;
    // The names travel with the code: the interface shows what tripped, not a
    // number the user has to look up.
    target["problemText"] = Battery::problemSummary(sample.problemCode);
    target["problemCount"] = Battery::problemCount(sample.problemCode);
    JsonArray problems = target["problems"].to<JsonArray>();
    if (sample.problemCode != 0) {
        const String summary = Battery::problemSummary(sample.problemCode);
        int start = 0;
        while (start < static_cast<int>(summary.length())) {
            int comma = summary.indexOf(", ", start);
            if (comma < 0) {
                comma = summary.length();
            }
            problems.add(summary.substring(start, comma));
            start = comma + 2;
        }
    }
    target["timestamp"] = isoTime(sample.timestamp);
    if (sample.etaS > 0) {
        target["etaS"] = serialized(String(sample.etaS, 1));
    }
    addBalanceJson(target["balance"].to<JsonObject>(), sample.balance);
}

void WebUi::addSettingsJson(JsonObject target) const {
    target["intervalS"] = config_->intervalMs / 1000.0;
    target["minIntervalS"] = MIN_INTERVAL_MS / 1000.0;
    target["maxIntervalS"] = MAX_INTERVAL_MS / 1000.0;
    // The device hands the battery over for a fixed five minutes, like the
    // desktop default.
    target["releaseMinutes"] = config_ != nullptr ? config_->releaseMinutes : 5;
    target["minReleaseMinutes"] = 1;
    target["maxReleaseMinutes"] = 120;
    // The history lives in RAM (a device has no disk to write years of data
    // to), so the database switch reports the truth: nothing is stored on
    // flash.
    target["storeSamples"] = false;
    target["apiEnabled"] = config_->apiEnabled;
    target["console"] = config_->debugLog;
    target["language"] = config_->language;
    target["otaPassword"] = config_->otaPassword;
    JsonObject mqtt = target["mqtt"].to<JsonObject>();
    mqtt["enabled"] = config_->mqtt.enabled;
    mqtt["host"] = config_->mqtt.host;
    mqtt["port"] = config_->mqtt.port;
    mqtt["username"] = config_->mqtt.user;
    mqtt["password"] = config_->mqtt.password;
    mqtt["topicPrefix"] = config_->mqtt.prefix;
    mqtt["discovery"] = config_->mqtt.discovery;
    mqtt["discoveryPrefix"] = config_->mqtt.discoveryPrefix;
    target["path"] = "";
}

void WebUi::handleState() {
    if (logVerbose()) {
        logLine("http", "state requested from " + server_.client().remoteIP().toString());
    }
    const NetworkStatus &network = network_->status();
    const bool connected = monitor_->connected;
    const uint32_t age = connected ? (millis() - monitor_->updatedAt) / 1000 : 0;

    JsonDocument document;
    JsonObject root = document.to<JsonObject>();

    JsonObject hub = root["hub"].to<JsonObject>();
    hub["scanning"] = !connected;
    hub["enabled"] = true;
    hub["seen"] = bms_->devicesSeen();
    hub["adverts"] = bms_->adverts();
    uint32_t named = 0;
    for (size_t index = 0; index < bms_->seenCount(); index++) {
        if (bms_->seen(index).name.length() > 0) {
            named++;
        }
    }
    hub["named"] = named;
    hub["scanRestarts"] = bms_->scanRestarts();
    hub["batteriesCount"] = connected ? 1 : 0;
    hub["identified"] = connected ? 1 : 0;
    hub["rejected"] = 0;
    hub["pending"] = 0;
    hub["storage"] = "flash";
    JsonArray batteries = hub["batteries"].to<JsonArray>();
    // Every battery ever found, straight from the list in flash: they stay
    // visible while they sleep or while another device holds them.
    const size_t known = config_->knownCount();
    for (size_t index = 0; index < known; index++) {
        const KnownBattery &entry = config_->known(index);
        const bool isCurrent = connected && entry.address.equalsIgnoreCase(bms_->address());
        JsonObject battery = batteries.add<JsonObject>();
        battery["addr"] = entry.address;
        battery["name"] = entry.name.length() > 0 ? entry.name : bms_->name();
        battery["brand"] = isCurrent ? bms_->brand() : "";
        battery["rssi"] = isCurrent ? bms_->rssi() : 0;
        battery["protocol"] = isCurrent ? bms_->protocol() : "";
        battery["status"] = isCurrent ? "bms" : "unavailable";
        battery["model"] = isCurrent ? bms_->model() : "";
        battery["firmware"] = "";
        battery["cells"] = entry.cells;
        battery["lastSeen"] = entry.lastSeen > 0 ? isoTime(entry.lastSeen) : "";
        battery["online"] = isCurrent;
        battery["hits"] = isCurrent ? bms_->samples() : 0;
    }
    hub["checking"].to<JsonArray>();
    // The device list the interface shows: what the scanner heard in the last
    // round, which is the answer to "why does it not find my battery".
    JsonArray devices = hub["devices"].to<JsonArray>();
    for (size_t index = 0; index < bms_->seenCount(); index++) {
        const Bms::SeenDevice &entry = bms_->seen(index);
        JsonObject device = devices.add<JsonObject>();
        device["addr"] = entry.address;
        device["name"] = entry.name;
        device["rssi"] = entry.rssi;
        device["protocol"] = "";
        device["status"] = entry.battery ? "candidate" : "notBMS";
        device["model"] = "";
        device["firmware"] = "";
        device["online"] = true;
        device["hits"] = 1;
    }
    hub["manual"].to<JsonArray>();

    JsonObject session = root["session"].to<JsonObject>();
    session["addr"] = bms_->address();
    session["status"] = connected ? "connected"
                                 : (bms_->address().length() > 0 ? "connecting" : "idle");
    session["error"] = bms_->error();
    session["brand"] = connected ? bms_->brand() : "";
    session["model"] = bms_->model();
    session["firmware"] = "";
    session["protocol"] = connected ? bms_->protocol() : "";
    session["samples"] = bms_->samples();
    session["attempt"] = bms_->attempts();
    session["auto"] = true;
    session["updatedAgoS"] = serialized(String(static_cast<double>(age), 1));
    session["takeover"] = bms_->takeover();
    session["releasedS"] = bms_->releasedSeconds();
    if (monitor_->sample.timestamp > 0) {
        addSampleJson(session["sample"].to<JsonObject>(), monitor_->sample);
    } else {
        session["sample"] = nullptr;
    }

    // The power graph: the last ten minutes of readings, read straight out of
    // the ring (copying it would not fit on the stack of this task).
    JsonArray power = session["power"].to<JsonArray>();
    if (history_ != nullptr) {
        const uint32_t now = static_cast<uint32_t>(time(nullptr));
        const uint32_t from = now > POWER_WINDOW_S ? now - POWER_WINDOW_S : 0;
        for (size_t index = 0; index < history_->count(); index++) {
            const HistoryPoint &point = history_->at(index);
            if (point.time < from) {
                continue;
            }
            JsonObject entry = power.add<JsonObject>();
            entry["t"] = static_cast<uint32_t>(point.time) * 1000UL;
            entry["w"] = serialized(String(point.watts, 2));
            entry["s"] = point.soc;
        }
    }

    JsonObject config = root["config"].to<JsonObject>();
    config["version"] = APP_VERSION;
    config["build"] = APP_BUILD;
    config["language"] = config_->language;
    config["intervalS"] = config_->intervalMs / 1000.0;
    config["dataDir"] = "/littlefs";
    config["platform"] = "esp32-c3";
    config["mqttConnected"] = mqttConnected_;
    config["radios"].to<JsonArray>();
    config["remote"] = false;
    config["listen"] = "0.0.0.0";
    JsonArray networkUrls = config["network"].to<JsonArray>();
    if (network.ip.length() > 0) {
        networkUrls.add(network.hostname);
        networkUrls.add(network.ip);
    }
    addSettingsJson(config["settings"].to<JsonObject>());

    // The long term database of the desktop application does not exist here;
    // saying so is better than an empty chart that never fills.
    JsonObject history = config["history"].to<JsonObject>();
    history["path"] = "";
    history["samples"] = history_ != nullptr ? history_->count() : 0;
    history["batteries"] = connected ? 1 : 0;

    // Everything the interface needs to show the device section: the settings
    // panel stays the same everywhere, only this block differs.
    JsonObject device = config["device"].to<JsonObject>();
    device["name"] = APP_TITLE;
    device["version"] = APP_VERSION;
    device["build"] = APP_BUILD;
    device["platform"] = "ESP32-C3";
    device["ip"] = network.ip;
    device["hostname"] = network.hostname;
    device["uptimeS"] = millis() / 1000;
    device["heap"] = ESP.getFreeHeap();
    device["wifiSsid"] = network.ssid;
    device["wifiRssi"] = network.rssi;
    device["accessPoint"] = network.accessPoint;
    device["accessPointSsid"] = network.accessPointSsid;
    device["accessPointClients"] = network.accessPointClients;
    device["wifiError"] = network.error;
    device["configured"] = config_->isConfigured();
    JsonObject display = device["display"].to<JsonObject>();
    display["available"] = display_ != nullptr && display_->available();
    display["sda"] = config_->displaySda;
    display["scl"] = config_->displayScl;
    device["ota"] = true;
    device["log"] = true;
    // Memory and the chart window: the interface shows both, so it is visible
    // how close the device runs to its limits and how much of the window is
    // left after the oldest readings were dropped.
    device["heapMin"] = ESP.getMinFreeHeap();
    device["historyPoints"] = history_ != nullptr ? history_->count() : 0;
    device["historyCapacity"] = history_ != nullptr ? history_->capacity() : 0;
    device["historyDropped"] = history_ != nullptr ? history_->dropped() : 0;
    // What is kept in flash, so the size limit is visible from the interface.
    device["historyStored"] = stats_ != nullptr ? stats_->count() : 0;
    device["historyStoredCapacity"] = stats_ != nullptr ? stats_->capacity() : 0;
    device["historyBytes"] = stats_ != nullptr ? stats_->bytes() : 0;
    device["flashFree"] = static_cast<uint32_t>(LittleFS.totalBytes() - LittleFS.usedBytes());

    String payload;
    serializeJson(document, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleBatteries() {
    if (!config_->apiEnabled) {
        server_.send(403, "text/plain; charset=utf-8", "the read API is disabled in the settings");
        return;
    }
    const String wanted = server_.hasArg("addr") ? server_.arg("addr") : String();
    JsonDocument document;
    JsonObject root = document.to<JsonObject>();
    root["generatedAt"] = isoTime(static_cast<uint32_t>(time(nullptr)));
    JsonArray batteries = root["batteries"].to<JsonArray>();
    int count = 0;
    if (monitor_->sample.timestamp > 0 &&
        (wanted.length() == 0 || wanted.equalsIgnoreCase(bms_->address()))) {
        JsonObject battery = batteries.add<JsonObject>();
        battery["addr"] = bms_->address();
        battery["name"] = bms_->name().length() > 0 ? bms_->name() : bms_->model();
        battery["model"] = bms_->model();
        battery["brand"] = bms_->brand();
        battery["protocol"] = bms_->protocol();
        battery["online"] = monitor_->connected;
        battery["lastSeen"] = isoTime(monitor_->sample.timestamp);
        battery["lastSampleAt"] = isoTime(monitor_->sample.timestamp);
        addSampleJson(battery["sample"].to<JsonObject>(), monitor_->sample);
        count = 1;
    }
    root["count"] = count;
    String payload;
    serializeJson(document, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleSettings() {
    if (server_.method() == HTTP_GET) {
        JsonDocument document;
        JsonObject root = document.to<JsonObject>();
        addSettingsJson(root);
        String payload;
        serializeJson(document, payload);
        server_.send(200, "application/json; charset=utf-8", payload);
        return;
    }

    JsonDocument document;
    if (deserializeJson(document, server_.arg("plain")) != DeserializationError::Ok) {
        server_.send(400, "text/plain; charset=utf-8", "invalid request");
        return;
    }
    JsonObject patch = document.as<JsonObject>();
    if (patch["intervalS"].is<double>()) {
        double seconds = patch["intervalS"].as<double>();
        if (seconds * 1000.0 < MIN_INTERVAL_MS) {
            seconds = MIN_INTERVAL_MS / 1000.0;
        }
        if (seconds * 1000.0 > MAX_INTERVAL_MS) {
            seconds = MAX_INTERVAL_MS / 1000.0;
        }
        config_->intervalMs = static_cast<uint32_t>(seconds * 1000.0);
    }
    if (patch["releaseMinutes"].is<int>()) {
        int minutes = patch["releaseMinutes"].as<int>();
        if (minutes < 1) {
            minutes = 1;
        }
        if (minutes > 120) {
            minutes = 120;
        }
        config_->releaseMinutes = static_cast<uint16_t>(minutes);
    }
    if (patch["apiEnabled"].is<bool>()) {
        config_->apiEnabled = patch["apiEnabled"].as<bool>();
    }
    if (patch["console"].is<bool>()) {
        config_->debugLog = patch["console"].as<bool>();
        logSetVerbose(config_->debugLog);
    }
    if (patch["language"].is<const char *>()) {
        config_->language = String(patch["language"].as<const char *>());
    }
    if (patch["otaPassword"].is<const char *>()) {
        config_->otaPassword = String(patch["otaPassword"].as<const char *>());
    }
    if (patch["mqtt"].is<JsonObject>()) {
        JsonObject mqtt = patch["mqtt"].as<JsonObject>();
        if (mqtt["enabled"].is<bool>()) config_->mqtt.enabled = mqtt["enabled"].as<bool>();
        if (mqtt["host"].is<const char *>()) config_->mqtt.host = String(mqtt["host"].as<const char *>());
        if (mqtt["port"].is<int>()) config_->mqtt.port = mqtt["port"].as<uint16_t>();
        if (mqtt["username"].is<const char *>())
            config_->mqtt.user = String(mqtt["username"].as<const char *>());
        if (mqtt["password"].is<const char *>())
            config_->mqtt.password = String(mqtt["password"].as<const char *>());
        if (mqtt["topicPrefix"].is<const char *>())
            config_->mqtt.prefix = String(mqtt["topicPrefix"].as<const char *>());
        if (mqtt["discovery"].is<bool>()) config_->mqtt.discovery = mqtt["discovery"].as<bool>();
        if (mqtt["discoveryPrefix"].is<const char *>())
            config_->mqtt.discoveryPrefix = String(mqtt["discoveryPrefix"].as<const char *>());
    }
    config_->save();
    logLine("http", "settings saved");

    JsonDocument response;
    JsonObject root = response.to<JsonObject>();
    addSettingsJson(root);
    String payload;
    serializeJson(response, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleSelect() {
    JsonDocument document;
    deserializeJson(document, server_.arg("plain"));
    const String address = String(document["addr"] | "");
    if (address.length() > 0) {
        // Picking a battery on the list makes it the preferred one.
        config_->promote(address);
        config_->batteryAddress = address;
        config_->save();
    }
    bms_->watch(address);
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"ok\"}");
}

void WebUi::handleDeepScan() {
    bms_->deepScan();
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"ok\"}");
}

void WebUi::handleConnect() {
    JsonDocument document;
    deserializeJson(document, server_.arg("plain"));
    const String address = String(document["addr"] | "");
    if (address.length() >= 11) {
        // The button on a battery card names the battery it belongs to; that
        // battery becomes the one the device reads.
        config_->batteryAddress = address;
        config_->promote(address);
        config_->save();
        bms_->watch(address);
    } else {
        bms_->forceConnect();
    }
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"ok\"}");
}

void WebUi::handleRelease() {
    JsonDocument document;
    deserializeJson(document, server_.arg("plain"));
    double minutes = document["minutes"] | static_cast<double>(config_->releaseMinutes);
    if (minutes <= 0 || minutes > 120) {
        minutes = 5;
    }
    bms_->release(static_cast<uint32_t>(minutes * 60.0));
    JsonDocument response;
    JsonObject root = response.to<JsonObject>();
    root["releasedS"] = minutes * 60.0;
    root["minutes"] = minutes;
    String payload;
    serializeJson(response, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleAdd() {
    JsonDocument document;
    deserializeJson(document, server_.arg("plain"));
    const String address = String(document["addr"] | "");
    const bool remove = document["remove"] | false;
    if (address.length() < 11) {
        server_.send(400, "text/plain; charset=utf-8",
                     "that is not a Bluetooth address (use AA:BB:CC:DD:EE:FF)");
        return;
    }
    if (remove) {
        config_->forgetBattery(address);
        JsonDocument response;
        JsonObject root = response.to<JsonObject>();
        root["status"] = "removed";
        root["addr"] = address;
        String payload;
        serializeJson(response, payload);
        server_.send(200, "application/json; charset=utf-8", payload);
        return;
    }
    // A hand written address joins the permanent list, so the device keeps
    // asking it even before it was ever heard.
    const String name = String(document["name"] | "");
    config_->rememberBattery(address, name);
    config_->promote(address);
    config_->batteryAddress = address;
    config_->save();
    bms_->watch(address);
    JsonDocument response;
    JsonObject root = response.to<JsonObject>();
    root["status"] = "added";
    root["addr"] = address;
    String payload;
    serializeJson(response, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleWindow() {
    // There is no desktop window on the device; the interface still asks.
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"ok\"}");
}

void WebUi::handleCellHistory() {
    const String address = server_.hasArg("addr") ? server_.arg("addr") : bms_->address();
    uint32_t minutes = server_.hasArg("minutes") ? server_.arg("minutes").toInt() : 60;
    if (minutes == 0) {
        minutes = 60;
    }
    const uint32_t now = static_cast<uint32_t>(time(nullptr));
    JsonDocument document;
    JsonObject root = document.to<JsonObject>();
    const size_t storedCount = stats_ != nullptr ? stats_->count() : 0;
    root["enabled"] = storedCount > 0 || (history_ != nullptr && history_->count() > 0);
    root["addr"] = address;
    root["minutes"] = minutes;
    JsonArray points = root["points"].to<JsonArray>();
    const uint32_t from = now > minutes * 60 ? now - minutes * 60 : 0;
    uint32_t newestStored = 0;
    // Everything that was written to flash comes first (it is what survives a
    // restart), then the readings still only in memory, which are newer.
    for (size_t index = 0; stats_ != nullptr && index < storedCount; index++) {
        HistoryPoint stored;
        if (!stats_->at(index, stored)) {
            break;
        }
        newestStored = stored.time;
        if (stored.time < from) {
            continue;
        }
        JsonObject point = points.add<JsonObject>();
        point["t"] = isoTime(stored.time);
        point["soc"] = stored.soc;
        JsonArray cells = point["cells"].to<JsonArray>();
        for (int cell = 0; cell < stored.cellCount; cell++) {
            cells.add(stored.cellsMv[cell]);
        }
    }
    if (history_ != nullptr) {
        for (size_t index = 0; index < history_->count(); index++) {
            const HistoryPoint &stored = history_->at(index);
            if (stored.time < from || stored.time <= newestStored) {
                continue;
            }
            JsonObject point = points.add<JsonObject>();
            point["t"] = isoTime(stored.time);
            point["soc"] = stored.soc;
            JsonArray cells = point["cells"].to<JsonArray>();
            for (int cell = 0; cell < stored.cellCount; cell++) {
                cells.add(stored.cellsMv[cell]);
            }
        }
    }
    root["capacity"] = history_ != nullptr ? history_->capacity() : 0;
    root["dropped"] = history_ != nullptr ? history_->dropped() : 0;
    root["stored"] = storedCount;
    root["storedCapacity"] = stats_ != nullptr ? stats_->capacity() : 0;
    root["storedBytes"] = stats_ != nullptr ? stats_->bytes() : 0;
    String payload;
    serializeJson(document, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleStatsClear() {
    if (stats_ != nullptr) {
        stats_->clear();
    }
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"ok\"}");
}

void WebUi::handleLog() {
    server_.send(200, "text/plain; charset=utf-8", logTail(40));
}

void WebUi::handleWifi() {
    JsonDocument document;
    JsonObject root = document.to<JsonObject>();
    const NetworkStatus &network = network_->status();
    root["accessPoint"] = network.accessPoint;
    root["connected"] = network.connected;
    root["ssid"] = network.ssid;
    root["ip"] = network.ip;
    root["rssi"] = network.rssi;
    root["accessPointSsid"] = network.accessPointSsid;
    root["error"] = network.error;
    if (server_.hasArg("scan")) {
        // Scanning takes a moment and the radio is busy with the battery, so it
        // only happens when the interface asks for it.
        const int found = WiFi.scanNetworks();
        JsonArray networks = root["networks"].to<JsonArray>();
        for (int index = 0; index < found; index++) {
            JsonObject entry = networks.add<JsonObject>();
            entry["ssid"] = WiFi.SSID(index);
            entry["rssi"] = WiFi.RSSI(index);
            entry["secure"] = WiFi.encryptionType(index) != WIFI_AUTH_OPEN;
        }
        WiFi.scanDelete();
    }
    String payload;
    serializeJson(document, payload);
    server_.send(200, "application/json; charset=utf-8", payload);
}

void WebUi::handleWifiSave() {
    JsonDocument document;
    deserializeJson(document, server_.arg("plain"));
    const String ssid = String(document["ssid"] | "");
    const String password = String(document["password"] | "");
    if (ssid.length() == 0) {
        server_.send(400, "text/plain; charset=utf-8", "a network name is required");
        return;
    }
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"saved\"}");
    delay(300);
    network_->save(ssid, password);
}

void WebUi::handleWifiForget() {
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"forgotten\"}");
    delay(300);
    network_->forget();
}

void WebUi::handleOta() {
    const bool ok = !Update.hasError();
    server_.send(ok ? 200 : 500, "application/json; charset=utf-8",
                 ok ? "{\"status\":\"ok\"}" : "{\"status\":\"error\"}");
    if (ok) {
        delay(500);
        ESP.restart();
    }
}

void WebUi::handleReboot() {
    server_.send(200, "application/json; charset=utf-8", "{\"status\":\"restarting\"}");
    delay(300);
    ESP.restart();
}

void WebUi::sendStatic(const String &path) {
    String target = path;
    if (target == "/" || target.length() == 0) {
        target = "/index.html";
    }
    if (!LittleFS.exists(target)) {
        server_.send(404, "text/plain; charset=utf-8", "not found");
        return;
    }
    // The interface is rebuilt with every firmware update, so a fixed change
    // key is enough: browsers may keep it, the device sends little.
    server_.sendHeader("Cache-Control", "max-age=3600");
    File file = LittleFS.open(target, "r");
    server_.streamFile(file, contentTypeOf(target));
    file.close();
}

void WebUi::handleNotFound() {
    // Anything else in the access point mode ends on the interface, which is
    // what makes a phone open the setup page by itself.
    if (network_ != nullptr && network_->status().accessPoint) {
        sendStatic("/index.html");
        return;
    }
    server_.send(404, "text/plain; charset=utf-8", "not found");
}
