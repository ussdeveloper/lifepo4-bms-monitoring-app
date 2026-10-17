#include "display.h"

#include <U8g2lib.h>
#include <Wire.h>

#include "config.h"
#include "log.h"

namespace {
// 72x40 is the display of these boards: four lines of small text or one big
// number with a symbol.
U8G2_SSD1306_72X40_ER_F_HW_I2C oled(U8G2_R0, U8X8_PIN_NONE);

// How long the warning screen and the battery screen take turns.
const uint32_t WARNING_SWITCH_MS = 4000;
}  // namespace

bool Display::begin(AppConfig *config) {
    config_ = config;

    // The board wiring is not standardised, so try the usual pin pairs and keep
    // the first one that has a display on it. Once found, the pins are stored.
    if (config->displaySda != 0xFF && config->displayScl != 0xFF) {
        sda_ = config->displaySda;
        scl_ = config->displayScl;
        address_ = config->displayAddress;
        Wire.begin(sda_, scl_);
        Wire.beginTransmission(address_);
        if (Wire.endTransmission() == 0) {
            oled.setI2CAddress(address_ << 1);
            oled.begin();
            ready_ = true;
            logLine("oled", "display on SDA " + String(sda_) + ", SCL " + String(scl_));
            return true;
        }
        logLine("oled", "the stored pins do not answer, probing again");
        sda_ = 0xFF;
        scl_ = 0xFF;
    }

    for (size_t index = 0; index < I2C_CANDIDATE_COUNT; index++) {
        const I2cCandidate &candidate = I2C_CANDIDATES[index];
        Wire.begin(candidate.sda, candidate.scl);
        for (uint8_t address : DISPLAY_ADDRESSES) {
            Wire.beginTransmission(address);
            if (Wire.endTransmission() != 0) {
                continue;
            }
            oled.setI2CAddress(address << 1);
            oled.begin();
            sda_ = candidate.sda;
            scl_ = candidate.scl;
            address_ = address;
            ready_ = true;
            if (config_ != nullptr) {
                config_->displaySda = sda_;
                config_->displayScl = scl_;
                config_->displayAddress = address_;
                config_->save();
            }
            logLine("oled", "display found on SDA " + String(sda_) + ", SCL " + String(scl_));
            return true;
        }
        Wire.end();
    }
    logLine("oled", "no display answered on the known pin pairs");
    return false;
}

void Display::giveUp(const char *reason) {
    if (!ready_) {
        return;
    }
    ready_ = false;
    logLine("oled", String("switching the display off: ") + reason);
}

void Display::centerText(const char *text, int y) {
    const int width = oled.getStrWidth(text);
    oled.drawStr((72 - width) / 2, y, text);
}

void Display::drawBatteryIcon(int x, int y, int width, int height, int percent) {
    // Body plus the little nub on the right, filled from the left.
    oled.drawFrame(x, y, width, height);
    oled.drawBox(x + width, y + 3, 2, height - 6);
    const int inner = width - 4;
    int filled = inner * percent / 100;
    if (filled < 0) {
        filled = 0;
    }
    if (filled > inner) {
        filled = inner;
    }
    if (filled > 0) {
        oled.drawBox(x + 2, y + 2, filled, height - 4);
    }
}

void Display::drawWarningIcon(int x, int y, int size) {
    // A triangle with an exclamation mark, drawn with lines so it scales.
    oled.drawLine(x + size / 2, y, x, y + size);
    oled.drawLine(x + size / 2, y, x + size, y + size);
    oled.drawLine(x, y + size, x + size, y + size);
    const int middle = x + size / 2;
    oled.drawBox(middle, y + size / 3, 2, size / 3);
    oled.drawBox(middle, y + size - size / 5, 2, 2);
}

const char *Display::problemText(uint32_t code) const {
    // The bits the BMS sets for the protections it knows; the desktop interface
    // shows the same codes.
    if (code & 0x00000001) return "CELL OVER VOLT";
    if (code & 0x00000002) return "CELL UNDER VOLT";
    if (code & 0x00000004) return "PACK OVER VOLT";
    if (code & 0x00000008) return "PACK UNDER VOLT";
    if (code & 0x00000010) return "CHARGE OVERTEMP";
    if (code & 0x00000020) return "DISCHARGE OVERTEMP";
    if (code & 0x00000040) return "CHARGE LOW TEMP";
    if (code & 0x00000080) return "DISCHARGE LOW TEMP";
    if (code & 0x00000100) return "CHARGE OVERCURRENT";
    if (code & 0x00000200) return "DISCHARGE OVERCURRENT";
    if (code & 0x00000400) return "SHORT CIRCUIT";
    if (code & 0x00000800) return "AFE ERROR";
    if (code & 0x00001000) return "SOFTWARE LOCK";
    return "BMS PROBLEM";
}

void Display::draw(const Status &status, const Sample &sample, bool haveSample) {
    if (!ready_) {
        return;
    }
    if (status.accessPoint) {
        drawAccessPoint(status);
        return;
    }
    if (!status.wifiConnected) {
        drawWifi(status);
        return;
    }
    if (!status.batteryConnected || !haveSample) {
        drawNoBattery(status);
        return;
    }

    // A problem or a drifted cell deserves attention, but the state of charge
    // stays visible: the two screens take turns.
    const bool warning = sample.problemCode != 0 ||
                        (sample.balance.available && sample.balance.state == "imbalanced") ||
                        status.takeover;
    const uint32_t now = millis();
    if (warning && now - lastSwitch_ > WARNING_SWITCH_MS) {
        showWarning_ = !showWarning_;
        lastSwitch_ = now;
    }
    if (!warning) {
        showWarning_ = false;
    }
    if (showWarning_) {
        if (status.takeover && !status.batteryConnected) {
            drawWarning(status, sample, "TAKEN OVER", "another device reads");
            return;
        }
        if (sample.problemCode != 0) {
            char code[16];
            snprintf(code, sizeof(code), "0x%lX", static_cast<unsigned long>(sample.problemCode));
            drawWarning(status, sample, problemText(sample.problemCode), String(code));
            return;
        }
        const String detail = "cell " + String(sample.balance.weak) + " " +
                              String(sample.balance.spreadMv) + "mV";
        drawWarning(status, sample, "CELL DRIFT", detail);
        return;
    }
    drawBattery(status, sample);
}

void Display::drawAccessPoint(const Status &status) {    oled.clearBuffer();
    oled.setFont(u8g2_font_6x10_tf);
    oled.drawStr(0, 9, "SETUP: JOIN WIFI");
    oled.setFont(u8g2_font_5x7_tf);
    oled.drawStr(0, 20, status.accessPointSsid.c_str());
    String password = status.accessPointPassword;
    if (password.length() == 0) {
        password = "(open)";
    } else {
        password = "pass " + password;
    }
    oled.drawStr(0, 30, password.c_str());
    oled.setFont(u8g2_font_6x10_tf);
    oled.drawStr(0, 40, "192.168.4.1");
    oled.sendBuffer();
}

void Display::drawWifi(const Status &status) {
    oled.clearBuffer();
    oled.setFont(u8g2_font_6x10_tf);
    oled.drawStr(0, 9, "WIFI");
    oled.setFont(u8g2_font_5x7_tf);
    const String ssid = status.ssid.length() > 0 ? status.ssid : String("(not set)");
    oled.drawStr(0, 20, ssid.substring(0, 13).c_str());
    String state = status.wifiError.length() > 0 ? status.wifiError : String("connecting...");
    oled.drawStr(0, 30, state.substring(0, 13).c_str());
    if (status.ip.length() > 0) {
        oled.drawStr(0, 40, status.ip.c_str());
    }
    oled.sendBuffer();
}

void Display::drawNoBattery(const Status &status) {
    oled.clearBuffer();
    oled.setFont(u8g2_font_6x10_tf);
    oled.drawStr(0, 9, "NO BATTERY");
    oled.setFont(u8g2_font_5x7_tf);
    if (status.batteryError.length() > 0) {
        oled.drawStr(0, 20, status.batteryError.substring(0, 13).c_str());
    }
    const String devices = String(status.devicesSeen) + " devices seen";
    oled.drawStr(0, 30, devices.c_str());
    oled.drawStr(0, 40, status.ip.c_str());
    oled.sendBuffer();
}

void Display::drawBattery(const Status &status, const Sample &sample) {
    oled.clearBuffer();
    oled.setFont(u8g2_font_6x10_tf);
    String title = status.batteryName.length() > 0 ? status.batteryName : String("BATTERY");
    if (title.length() > 12) {
        title = title.substring(0, 12);
    }
    oled.drawStr(0, 9, title.c_str());

    // The state of charge, big, with a battery symbol filled to the same level.
    oled.setFont(u8g2_font_logisoso16_tn);
    const String percent = String(sample.soc);
    oled.drawStr(0, 30, percent.c_str());
    const int width = oled.getStrWidth(percent.c_str());
    oled.drawStr(width + 2, 30, "%");
    oled.setFont(u8g2_font_6x10_tf);
    drawBatteryIcon(width + 18, 15, 26, 14, sample.soc);

    oled.setFont(u8g2_font_5x7_tf);
    char line[24];
    snprintf(line, sizeof(line), "%.2fV %+.1fA", sample.voltageV, sample.currentA);
    oled.drawStr(0, 40, line);
    const char *state = sample.problemCode != 0 ? "PROBLEM"
                       : (sample.balancer ? "BALANCING"
                                          : (sample.chargingMosfet && sample.currentA > 0.3f ? "CHARGING"
                                                                                            : (sample.currentA < -0.3f ? "LOAD" : "IDLE")));
    const int stateWidth = oled.getStrWidth(state);
    oled.drawStr(70 - stateWidth, 40, state);
    oled.sendBuffer();
}

void Display::drawWarning(const Status &status, const Sample &sample, const String &headline,
                          const String &detail) {
    oled.clearBuffer();
    drawWarningIcon(0, 2, 14);
    oled.setFont(u8g2_font_6x10_tf);
    oled.drawStr(18, 12, headline.substring(0, 9).c_str());
    oled.setFont(u8g2_font_5x7_tf);
    oled.drawStr(0, 26, detail.substring(0, 13).c_str());
    char line[24];
    snprintf(line, sizeof(line), "%.2fV %d%%", sample.voltageV, sample.soc);
    oled.drawStr(0, 36, line);
    (void)status;
    oled.sendBuffer();
}
