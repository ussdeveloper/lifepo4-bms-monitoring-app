// Over the air updates.
//
// Two ways in, both writing to the spare application slot (the partition table
// keeps the running firmware until the new one is complete):
//
//   * the Arduino/PlatformIO protocol on UDP 3232, which is what
//     "pio run -t upload --upload-port <ip>" and the Arduino IDE use;
//   * a plain HTTP POST of a .bin to /api/ota, which any browser or curl can
//     do (the interface has a button for it).
#pragma once

#include <Arduino.h>

#include "appconfig.h"

class Ota {
public:
    void begin(AppConfig *config);
    void loop();
    bool ready() const { return ready_; }
    const String &hostname() const { return hostname_; }

private:
    AppConfig *config_ = nullptr;
    bool ready_ = false;
    String hostname_;
};
