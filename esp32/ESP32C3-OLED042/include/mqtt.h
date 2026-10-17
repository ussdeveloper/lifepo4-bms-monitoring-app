// MQTT: the same topics, the same JSON and the same Home Assistant discovery
// documents as the desktop application, so a battery shows up in Home
// Assistant identically whether a computer or this device reads it.
#pragma once

#include <Arduino.h>

#include "appconfig.h"
#include "battery.h"
#include "bms.h"

class Mqtt {
public:
    void begin(AppConfig *config, Bms *bms);
    void loop();
    bool connected() const;
    // publish sends one reading.
    void publish(const Monitor &monitor);

private:
    void connect();
    void disconnect();
    // publishDiscovery announces every remembered battery to Home Assistant.
    void publishDiscovery();
    void publishBatteryDiscovery(const String &address, const String &name, const String &brand,
                                 const String &model, int cells, int &refused);
    void publishBatteryAvailability(const String &address, bool online);
    void publishAvailability(const char *state);
    String slug(const String &address) const;

    AppConfig *config_ = nullptr;
    Bms *bms_ = nullptr;
    bool enabled_ = false;
    String activeSignature_;
    // The last problem code published as news, so an unchanged fault is not
    // repeated on every reading.
    uint32_t lastProblemCode_ = 0;
    bool problemPublished_ = false;
    // The list of batteries the last discovery batch was built from.
    String discoverySignature_;
    // The battery whose availability was published last, so a change (the BMS
    // answering or falling silent) is published once and not every second.
    String availabilityAddress_;
    bool availabilityOnline_ = false;
    uint32_t nextAttemptAt_ = 0;
};
