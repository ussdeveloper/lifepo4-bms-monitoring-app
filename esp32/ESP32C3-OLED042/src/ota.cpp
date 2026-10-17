#include "ota.h"

#include <ArduinoOTA.h>
#include <WiFi.h>

#include "config.h"
#include "log.h"

void Ota::begin(AppConfig *config) {
    config_ = config;
    ArduinoOTA.setHostname(MDNS_NAME);
    if (config_->otaPassword.length() > 0) {
        ArduinoOTA.setPassword(config_->otaPassword.c_str());
    }
    ArduinoOTA.onStart([]() {
        // A reboot into the new image follows, so there is nothing to clean up.
        logLine("ota", "update starting, do not switch the device off");
    });
    ArduinoOTA.onProgress([](unsigned int progress, unsigned int total) {
        static uint32_t lastReport = 0;
        const uint32_t percent = total ? (progress * 100 / total) : 0;
        if (millis() - lastReport > 2000) {
            lastReport = millis();
            logLine("ota", "received " + String(percent) + "%");
        }
    });
    ArduinoOTA.onEnd([]() { logLine("ota", "update finished, restarting"); });
    ArduinoOTA.onError([](ota_error_t error) {
        logLine("ota", "update failed (code " + String(static_cast<int>(error)) + ")");
    });
    ArduinoOTA.begin();
    ready_ = true;
    hostname_ = String(MDNS_NAME) + ".local";
    logLine("ota", "over the air updates ready on " + hostname_ + ":3232");
}

void Ota::loop() {
    if (ready_) {
        ArduinoOTA.handle();
    }
}
