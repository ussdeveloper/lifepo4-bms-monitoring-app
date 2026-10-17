// The 0.42" OLED on the board: what the device is doing right now.
//
// Screens, in order of importance:
//   * the access point it started, so it can be joined without a computer;
//   * Wi-Fi state while the device joins the network;
//   * "no battery" with the reason (nothing in range, connection lost);
//   * the battery: state of charge as a number with a battery symbol, the
//     voltage and current, and a warning symbol when the BMS reports a problem
//     or the cells drift apart.
#pragma once

#include <Arduino.h>

#include "appconfig.h"
#include "battery.h"

class Display {
public:
    // begin probes the usual I2C pin pairs and starts the display that answers.
    // Returns false when no display was found (the device keeps working).
    bool begin(AppConfig *config);
    bool available() const { return ready_; }

    // The state the screens are drawn from.
    struct Status {
        bool accessPoint = false;
        bool wifiConnected = false;
        String ssid;
        String ip;
        String accessPointSsid;
        String accessPointPassword;
        uint8_t accessPointClients = 0;
        String wifiError;
        bool batteryConnected = false;
        bool takeover = false;
        String batteryName;
        String batteryError;
        uint32_t devicesSeen = 0;
        uint32_t releaseLeft = 0;
    };

    void draw(const Status &status, const Sample &sample, bool haveSample);
    // Called by the device when a draw took too long: a display that hangs the
    // I2C bus must not take the whole device down, so it is switched off.
    void giveUp(const char *reason);

private:
    void drawAccessPoint(const Status &status);
    void drawWifi(const Status &status);
    void drawNoBattery(const Status &status);
    void drawBattery(const Status &status, const Sample &sample);
    void drawWarning(const Status &status, const Sample &sample, const String &headline,
                     const String &detail);
    void drawBatteryIcon(int x, int y, int width, int height, int percent);
    void drawWarningIcon(int x, int y, int size);
    void centerText(const char *text, int y);
    const char *problemText(uint32_t code) const;

    AppConfig *config_ = nullptr;
    bool ready_ = false;
    uint8_t sda_ = 0xFF;
    uint8_t scl_ = 0xFF;
    uint8_t address_ = 0x3C;
    // The warning screens alternate with the battery screen so neither hides
    // the other for good.
    uint32_t lastSwitch_ = 0;
    bool showWarning_ = false;
};
