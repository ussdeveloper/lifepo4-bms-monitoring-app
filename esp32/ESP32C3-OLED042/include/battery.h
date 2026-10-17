// The battery: one reading, the balance analysis, a rolling window of recent
// readings and the estimate of the time to full or empty. This is a direct port
// of protocol.go, balance.go and history.go of the desktop application, so the
// device and the computer report the same numbers.
#pragma once

#include <Arduino.h>

#include "config.h"

#define MAX_CELLS 24
#define MAX_TEMPERATURES 6

struct Balance {
    bool available = false;
    int spreadMv = 0;
    String zone = "flat";    // "top", "flat" or "bottom" of the curve
    String state = "balanced";  // "balanced", "phantom" or "imbalanced"
    int toleranceMv = 0;
    int baseMv = 0;
    int adjustMv = 0;
    bool newPack = false;
    bool largeCells = false;
    int centreMv = 0;
    int lowMv = 0;
    int highMv = 0;
    int weak = 0;
    int strong = 0;
};

struct Sample {
    String protocol;
    float voltageV = 0;
    float currentA = 0;
    int soc = 0;
    bool hasSoh = false;
    int soh = 0;
    float remainingAh = 0;
    float designAh = 0;
    int cycles = 0;
    int cellsMv[MAX_CELLS] = {0};
    int cellCount = 0;
    float temperaturesC[MAX_TEMPERATURES] = {0};
    int temperatureCount = 0;
    bool chargingMosfet = false;
    bool dischargingMosfet = false;
    bool balancer = false;
    uint32_t problemCode = 0;
    uint32_t timestamp = 0;  // seconds since the epoch
    Balance balance;
    float etaS = 0;
};

// Live state of the monitored battery, as the interface and MQTT expect it.
struct Monitor {
    bool connected = false;
    String protocol;
    String model;
    String brand;
    String firmware;
    String address;
    String name;
    uint32_t samples = 0;
    uint32_t updatedAt = 0;  // millis() of the last reading
    Sample sample;
};

class Battery {
public:
    // decodeHS03 turns the three answers of the BMS into one reading.
    static bool decodeHS03(const uint8_t *analog, size_t analogLength, const uint8_t *status,
                           size_t statusLength, const uint8_t *cells, size_t cellsLength,
                           Sample &out);
    static String decodeModel(const uint8_t *payload, size_t length);

    // analyseBalance compares the cell voltages of a reading: the window every
    // cell should stay inside follows the state of charge, the size of the
    // cells and how new the pack is.
    static Balance analyseBalance(const int *cellsMv, int count, float designAh, int cycles);

    // problemSummary names the protections in a problem code, joined with
    // ", " ("none" when nothing is set). Every consumer — the OLED, the
    // interface and MQTT — reads the same names from here.
    static String problemSummary(uint32_t code);
    // problemCount is how many protections a code reports.
    static int problemCount(uint32_t code);
};

// The rolling window of the last readings, drawn as the power graph and the
// cell chart. Cell voltages are stored as 16 bit values: a cell never goes far
// above 4 V, and the window has to fit in the little memory the device has.
struct HistoryPoint {
    uint32_t time;  // seconds since the epoch
    float watts;
    int16_t soc;
    int16_t cellCount;
    int16_t cellsMv[MAX_CELLS];
};

class History {
public:
    void add(const Sample &sample);
    size_t count() const { return count_; }
    // capacity is the number of readings the window currently keeps: it shrinks
    // when the device runs low on memory, so Wi-Fi, Bluetooth and the web
    // server always have room to work.
    size_t capacity() const { return capacity_; }
    // dropped counts the readings that left the window because it was full (or
    // because it had to shrink). The oldest ones go first.
    uint32_t dropped() const { return dropped_; }
    bool trimmed() const { return capacity_ < HISTORY_POINTS; }
    const HistoryPoint &at(size_t index) const { return points_[(start_ + index) % HISTORY_POINTS]; }
    // The estimate of the seconds until the battery is full (charging) or empty
    // (discharging); zero when it cannot be told.
    float estimate(const Sample &sample) const;
    // guardMemory shrinks the window when free memory is low; true when it did.
    bool guardMemory();

private:
    void fill(HistoryPoint &slot, const Sample &sample) const;
    HistoryPoint points_[HISTORY_POINTS];
    size_t start_ = 0;
    size_t count_ = 0;
    size_t capacity_ = HISTORY_POINTS;
    uint32_t dropped_ = 0;
};
