#include "net.h"

#include <DNSServer.h>
#include <ESPmDNS.h>
#include <WiFi.h>

#include "config.h"
#include "log.h"

namespace {
// One attempt at the saved network: if it is there the device joins in a few
// seconds, if it is not the setup access point comes up right after.
const uint32_t STA_TIMEOUT_MS = 25000;
// While the setup access point is up the saved network is still tried once in
// a while, so a router that comes back later does not need a power cycle.
const uint32_t AP_RETRY_MS = 5UL * 60UL * 1000UL;
DNSServer dns;
const byte DNS_PORT = 53;
}  // namespace

String Network::deviceSuffix() const {
    // BATTERY-<number>: a random four digit number, drawn once and remembered,
    // so the access point keeps its name across restarts and is still easy to
    // pick out among the neighbours' networks.
    if (config_ != nullptr && config_->accessPointNumber == 0) {
        config_->accessPointNumber = 1000 + (esp_random() % 9000);
        config_->save();
    }
    const uint32_t number = config_ != nullptr && config_->accessPointNumber != 0
                                ? config_->accessPointNumber
                                : (1000 + (esp_random() % 9000));
    return String(number);
}

void Network::begin(AppConfig *config) {
    config_ = config;
    WiFi.persistent(false);
    WiFi.setHostname(MDNS_NAME);

    if (!config->isConfigured()) {
        startAccessPoint("no network configured yet");
        return;
    }
    startStation();
}

void Network::startStation() {
    if (accessPointStarted) {
        WiFi.softAPdisconnect(true);
        dns.stop();
        accessPointStarted = false;
    }
    status_.accessPoint = false;
    status_.error = "";
    WiFi.mode(WIFI_STA);
    // Modem sleep stays on: the ESP32-C3 shares its radio, and turning sleep
    // off while Bluetooth is running aborts the device (the Wi-Fi driver says
    // so itself).
    WiFi.setSleep(true);
    WiFi.begin(config_->wifiSsid.c_str(), config_->wifiPassword.c_str());
    status_.ssid = config_->wifiSsid;
    nextAttemptAt_ = millis() + STA_TIMEOUT_MS;
    logLine("wifi", "joining " + config_->wifiSsid);
}

void Network::startAccessPoint(const String &reason) {
    status_.accessPoint = true;
    status_.error = reason;
    status_.accessPointSsid = String(ACCESS_POINT_PREFIX) + deviceSuffix();
    status_.accessPointPassword = "";  // open: it only exists until it is set up
    accessPointUsers_ = false;
    if (!accessPointStarted) {
        // A clean access point, nothing else on the radio: joining an access
        // point that is also trying to connect somewhere makes it invisible
        // (the station attempts eat the beacons), and being found is the whole
        // point of this mode. The saved network is tried again at every start.
        WiFi.mode(WIFI_AP);
        const bool started = WiFi.softAP(status_.accessPointSsid.c_str(),
                                         status_.accessPointPassword.c_str());
        if (!started) {
            logLine("wifi", "the access point could not be started");
        } else {
            logLine("wifi", "access point up: channel " + String(WiFi.channel()) + ", mac " +
                                WiFi.softAPmacAddress() + ", " +
                                String(ESP.getFreeHeap() / 1024) + " KB free");
        }
        // Everything a phone asks for ends on the setup page.
        dns.start(DNS_PORT, "*", WiFi.softAPIP());
        accessPointStarted = true;
    }
    accessPointRetryAt_ = millis() + AP_RETRY_MS;
    logLine("wifi", "access point " + status_.accessPointSsid + " on " +
                        WiFi.softAPIP().toString() + " (" + reason + ")");
}

void Network::save(const String &ssid, const String &password) {
    if (config_ == nullptr) {
        return;
    }
    config_->wifiSsid = ssid;
    config_->wifiPassword = password;
    config_->save();
    logLine("wifi", "network saved, restarting");
    delay(200);
    ESP.restart();
}

void Network::forget() {
    if (config_ != nullptr) {
        config_->wifiSsid = "";
        config_->wifiPassword = "";
        config_->save();
    }
    delay(200);
    ESP.restart();
}

void Network::loop() {
    if (status_.accessPoint) {
        dns.processNextRequest();
        status_.accessPointClients = WiFi.softAPgetStationNum();
        if (status_.accessPointClients > 0) {
            // Someone is on the setup page: the access point stays put until
            // the device is restarted, so nothing is interrupted mid-setup.
            accessPointUsers_ = true;
            accessPointRetryAt_ = millis() + AP_RETRY_MS;
        }
        if (status_.accessPointClients == 0 && !accessPointUsers_ && config_ != nullptr &&
            config_->isConfigured() && static_cast<int32_t>(millis() - accessPointRetryAt_) >= 0) {
            logLine("wifi", "trying " + config_->wifiSsid + " again");
            // If the attempt fails the fallback below brings the access point
            // straight back.
            startStation();
        }
        return;
    }

    if (WiFi.status() == WL_CONNECTED) {
        if (!status_.connected) {
            status_.connected = true;
            status_.ip = WiFi.localIP().toString();
            status_.rssi = WiFi.RSSI();
            status_.hostname = String(MDNS_NAME) + ".local";
            logLine("wifi", "connected, interface on http://" + status_.ip + "/");
            if (MDNS.begin(MDNS_NAME)) {
                MDNS.addService("http", "tcp", 80);
                logLine("wifi", "also on http://" + status_.hostname + "/");
            }
        }
        status_.rssi = WiFi.RSSI();
        return;
    }

    status_.connected = false;
    if (static_cast<int32_t>(millis() - nextAttemptAt_) < 0) {
        return;
    }
    // The network is not there: offer the access point so the credentials can
    // be corrected without plugging the device in.
    startAccessPoint("cannot reach " + config_->wifiSsid);
}
