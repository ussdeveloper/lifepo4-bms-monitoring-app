// Wi-Fi: the device joins the configured network, and until it has one it
// starts its own access point with the interface on 192.168.4.1, so it can be
// set up from a phone without a cable.
#pragma once

#include <Arduino.h>

#include "appconfig.h"

struct NetworkStatus {
    bool accessPoint = false;
    bool connected = false;
    String ssid;
    String ip;
    int32_t rssi = 0;
    String accessPointSsid;
    String accessPointPassword;
    uint8_t accessPointClients = 0;
    String error;
    String hostname;
};

class Network {
public:
    void begin(AppConfig *config);
    void loop();
    const NetworkStatus &status() const { return status_; }

    // save stores the network and restarts: a clean join is worth the few
    // seconds, and it keeps one code path for "apply settings".
    void save(const String &ssid, const String &password);
    // forget drops the credentials and starts the access point again.
    void forget();

private:
    void startAccessPoint(const String &reason);
    void startStation();
    String deviceSuffix() const;

    AppConfig *config_ = nullptr;
    NetworkStatus status_;
    uint32_t nextAttemptAt_ = 0;
    bool accessPointStarted = false;
    bool accessPointUsers_ = false;
    uint32_t accessPointRetryAt_ = 0;
};
