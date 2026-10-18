// The interface and the API, served on port 80.
//
// The same ui/ files as the desktop application are served from the flash
// filesystem, and the API answers with the same JSON, so the two look and
// behave the same. The endpoints the device cannot offer (the long term
// database) say so instead of pretending.
#pragma once

#include <Arduino.h>
#include <ArduinoJson.h>
#include <WebServer.h>

#include "appconfig.h"
#include "battery.h"
#include "bms.h"
#include "display.h"
#include "net.h"
#include "stats.h"

class WebUi {
public:
    void begin(AppConfig *config, Network *network, Bms *bms, Display *display, History *history,
               Stats *stats, Monitor *monitor);
    void loop();
    void noteMqttConnection(bool connected) { mqttConnected_ = connected; }
    // updating is true while an upload is being written: the radio belongs to
    // the update then, otherwise a 100 % Bluetooth scan keeps stealing it and
    // the transfer crawls.
    bool updating() const { return filesystemUpload_ || firmwareUpload_; }

private:
    void handleState();
    void handleBatteries();
    void handleSettings();
    void handleSelect();
    void handleDeepScan();
    void handleConnect();
    void handleRelease();
    void handleAdd();
    void handleWindow();
    void handleCellHistory();
    void handleStatsClear();
    void handleLog();
    void handleWifi();
    void handleWifiSave();
    void handleWifiForget();
    void handleOta();
    void handleReboot();
    void handleNotFound();
    void sendStatic(const String &path);
    void addSampleJson(JsonObject target, const Sample &sample) const;
    void addBalanceJson(JsonObject target, const Balance &balance) const;
    void addSettingsJson(JsonObject target) const;
    String isoTime(uint32_t seconds) const;

    AppConfig *config_ = nullptr;
    Network *network_ = nullptr;
    Bms *bms_ = nullptr;
    Display *display_ = nullptr;
    History *history_ = nullptr;
    Stats *stats_ = nullptr;
    Monitor *monitor_ = nullptr;
    WebServer server_{80};
    bool mqttConnected_ = false;
    bool filesystemUpload_ = false;
    bool firmwareUpload_ = false;
};
