#include "battery.h"

#include <math.h>
#include <string.h>

#include "config.h"

namespace {
const uint8_t HS03_HEAD = 0xAA;
const int HS03_MIN_FRAME = 5;

// Balance limits: tight where the LiFePO4 curve is steep (where a spread means
// a difference in capacity), wide in the flat part, where it means almost
// nothing.
const int BALANCE_TOP_MV = 3400;
const int BALANCE_BOTTOM_MV = 3100;
const int BALANCE_TOP_TOLERANCE_MV = 30;
const int BALANCE_BOTTOM_TOLERANCE_MV = 40;
const int BALANCE_FLAT_TOLERANCE_MV = 60;

// The power the battery has to take or give for the estimate to mean anything.
const float IDLE_CURRENT_A = 0.3f;
const float MIN_ESTIMATE_S = 30.0f;
const float MAX_ESTIMATE_S = 48.0f * 3600.0f;

uint16_t readUInt16(const uint8_t *data) {
    return static_cast<uint16_t>(data[0]) | (static_cast<uint16_t>(data[1]) << 8);
}

// One entry per protection bit the BMS reports in the status frame. The names
// are what the interface, the OLED and Home Assistant show, so a code never
// reaches the user as a bare number.
struct ProblemBit {
    uint32_t mask;
    const char *name;
};

const ProblemBit PROBLEMS[] = {
    {0x00000001u, "cell over voltage"},
    {0x00000002u, "cell under voltage"},
    {0x00000004u, "pack over voltage"},
    {0x00000008u, "pack under voltage"},
    {0x00000010u, "charge over temperature"},
    {0x00000020u, "discharge over temperature"},
    {0x00000040u, "charge low temperature"},
    {0x00000080u, "discharge low temperature"},
    {0x00000100u, "charge over current"},
    {0x00000200u, "discharge over current"},
    {0x00000400u, "short circuit"},
    {0x00000800u, "AFE error"},
    {0x00001000u, "software lock"},
};
const size_t PROBLEM_COUNT = sizeof(PROBLEMS) / sizeof(PROBLEMS[0]);

uint32_t readUInt32(const uint8_t *data) {
    return static_cast<uint32_t>(data[0]) | (static_cast<uint32_t>(data[1]) << 8) |
           (static_cast<uint32_t>(data[2]) << 16) | (static_cast<uint32_t>(data[3]) << 24);
}

int32_t readInt32(const uint8_t *data) { return static_cast<int32_t>(readUInt32(data)); }

void balanceWindowAdjustment(float designAh, int cycles, int &adjustment, bool &newPack,
                            bool &largeCells) {
    adjustment = 0;
    newPack = false;
    largeCells = false;
    if (cycles <= 5) {
        adjustment += 15;
        newPack = true;
    } else if (cycles <= 20) {
        adjustment += 8;
        newPack = true;
    }
    if (designAh >= 280.0f) {
        adjustment += 8;
        largeCells = true;
    } else if (designAh >= 150.0f) {
        adjustment += 4;
        largeCells = true;
    }
}
}  // namespace

bool Battery::decodeHS03(const uint8_t *analog, size_t analogLength, const uint8_t *status,
                         size_t statusLength, const uint8_t *cells, size_t cellsLength,
                         Sample &out) {
    if (analog == nullptr || analogLength < 26 || status == nullptr || statusLength < 8) {
        return false;
    }
    const int soc = analog[8];
    if (soc > 100) {
        return false;
    }
    int slots = static_cast<int>(cellsLength) / 2;
    if (slots > MAX_CELLS) {
        slots = MAX_CELLS;
    }
    out.cellCount = 0;
    for (int index = 0; index < slots && cells != nullptr; index++) {
        const int millivolts = readUInt16(cells + index * 2);
        if (millivolts > 0) {
            out.cellsMv[out.cellCount++] = millivolts;
        }
    }
    if (out.cellCount == 0) {
        return false;
    }
    out.temperatureCount = MAX_TEMPERATURES;
    for (int index = 0; index < MAX_TEMPERATURES; index++) {
        out.temperaturesC[index] = static_cast<float>(static_cast<int8_t>(analog[20 + index]));
    }
    out.protocol = "HS03/HiLink";
    out.voltageV = static_cast<float>(readUInt32(analog)) / 1000.0f;
    out.currentA = static_cast<float>(readInt32(analog + 4)) / 1000.0f;
    out.soc = soc;
    out.hasSoh = true;
    out.soh = analog[9];
    out.remainingAh = static_cast<float>(readUInt32(analog + 10)) / 1000.0f;
    out.designAh = static_cast<float>(readUInt32(analog + 14)) / 1000.0f;
    out.cycles = readUInt16(analog + 18);
    out.chargingMosfet = (status[4] & 0x80) != 0;
    out.dischargingMosfet = (status[6] & 0x80) != 0;
    out.balancer = (status[5] & 0x80) != 0;
    out.problemCode = readUInt32(status + 4) & 0xFF7F7F7Fu;
    out.balance = analyseBalance(out.cellsMv, out.cellCount, out.designAh, out.cycles);
    return true;
}

String Battery::decodeModel(const uint8_t *payload, size_t length) {
    if (payload == nullptr || length == 0) {
        return String();
    }
    size_t end = 0;
    while (end < length && payload[end] != 0) {
        end++;
    }
    String text;
    text.reserve(end);
    for (size_t index = 0; index < end; index++) {
        const char character = static_cast<char>(payload[index]);
        text += (character >= 32 && character < 127) ? character : ' ';
    }
    text.trim();
    return text;
}

String Battery::problemSummary(uint32_t code) {
    String out;
    for (size_t index = 0; index < PROBLEM_COUNT; index++) {
        if ((code & PROBLEMS[index].mask) == 0) {
            continue;
        }
        if (out.length() > 0) {
            out += ", ";
        }
        out += PROBLEMS[index].name;
    }
    // A bit the table does not know is still reported, so nothing is hidden.
    if (out.length() == 0 && code != 0) {
        out = "unknown problem";
    }
    if (out.length() == 0) {
        out = "none";
    }
    return out;
}

int Battery::problemCount(uint32_t code) {
    int count = 0;
    for (size_t index = 0; index < PROBLEM_COUNT; index++) {
        if ((code & PROBLEMS[index].mask) != 0) {
            count++;
        }
    }
    const uint32_t known = 0x00001FFFu;
    if ((code & ~known) != 0) {
        count++;
    }
    return count;
}

Balance Battery::analyseBalance(const int *cellsMv, int count, float designAh, int cycles) {
    Balance balance;
    if (cellsMv == nullptr || count < 2) {
        return balance;
    }
    int minimum = cellsMv[0];
    int maximum = cellsMv[0];
    int weak = 1;
    int strong = 1;
    long total = 0;
    for (int index = 0; index < count; index++) {
        total += cellsMv[index];
        if (cellsMv[index] < minimum) {
            minimum = cellsMv[index];
            weak = index + 1;
        }
        if (cellsMv[index] > maximum) {
            maximum = cellsMv[index];
            strong = index + 1;
        }
    }
    const int centre = static_cast<int>(total / count);
    const int spread = maximum - minimum;

    int base = BALANCE_FLAT_TOLERANCE_MV;
    String zone = "flat";
    if (centre >= BALANCE_TOP_MV) {
        zone = "top";
        base = BALANCE_TOP_TOLERANCE_MV;
    } else if (centre <= BALANCE_BOTTOM_MV) {
        zone = "bottom";
        base = BALANCE_BOTTOM_TOLERANCE_MV;
    }
    int adjustment = 0;
    bool newPack = false;
    bool largeCells = false;
    balanceWindowAdjustment(designAh, cycles, adjustment, newPack, largeCells);
    const int tolerance = base + adjustment;

    String state = "balanced";
    if (spread > tolerance) {
        state = (zone == "flat") ? "phantom" : "imbalanced";
    }

    balance.available = true;
    balance.spreadMv = spread;
    balance.zone = zone;
    balance.state = state;
    balance.toleranceMv = tolerance;
    balance.baseMv = base;
    balance.adjustMv = adjustment;
    balance.newPack = newPack;
    balance.largeCells = largeCells;
    balance.centreMv = centre;
    balance.lowMv = centre - tolerance;
    balance.highMv = centre + tolerance;
    balance.weak = weak;
    balance.strong = strong;
    return balance;
}

void History::fill(HistoryPoint &slot, const Sample &sample) const {
    slot.time = sample.timestamp;
    slot.watts = sample.voltageV * sample.currentA;
    slot.soc = static_cast<int16_t>(sample.soc);
    slot.cellCount = static_cast<int16_t>(sample.cellCount);
    for (int index = 0; index < sample.cellCount && index < MAX_CELLS; index++) {
        slot.cellsMv[index] = static_cast<int16_t>(sample.cellsMv[index]);
    }
}

void History::add(const Sample &sample) {
    if (sample.timestamp == 0) {
        return;
    }
    if (count_ < capacity_) {
        HistoryPoint &slot = points_[(start_ + count_) % HISTORY_POINTS];
        fill(slot, sample);
        count_++;
        return;
    }
    // The window is full: the oldest reading makes room for the new one, so the
    // buffer can never grow past its capacity.
    start_ = (start_ + 1) % HISTORY_POINTS;
    HistoryPoint &slot = points_[(start_ + count_ - 1) % HISTORY_POINTS];
    fill(slot, sample);
    dropped_++;
}

bool History::guardMemory() {
    if (ESP.getFreeHeap() >= LOW_HEAP_BYTES || capacity_ <= MIN_HISTORY_POINTS) {
        return false;
    }
    // Halve the window, dropping the oldest readings first, and keep going
    // until the floor: the charts get shorter, the device stays alive.
    size_t next = capacity_ / 2;
    if (next < MIN_HISTORY_POINTS) {
        next = MIN_HISTORY_POINTS;
    }
    while (count_ > next) {
        start_ = (start_ + 1) % HISTORY_POINTS;
        count_--;
        dropped_++;
    }
    capacity_ = next;
    return true;
}

float History::estimate(const Sample &sample) const {
    if (sample.designAh <= 0) {
        return 0;
    }
    const float current = fabsf(sample.currentA);
    const bool charging = sample.currentA > 0;
    const float missingAh = charging ? (sample.designAh - sample.remainingAh) : sample.remainingAh;
    if (missingAh <= 0.05f || current < IDLE_CURRENT_A) {
        return 0;
    }
    const float coulomb = missingAh / current * 3600.0f;

    // The trend of the state of charge over the window: it catches a charge
    // that is tapering off, which the current alone cannot see.
    float trend = 0;
    if (count_ >= 3) {
        const HistoryPoint &first = at(0);
        const HistoryPoint &last = at(count_ - 1);
        const uint32_t span = last.time - first.time;
        const int socDelta = last.soc - first.soc;
        float peakCurrent = 0;
        for (size_t index = 0; index < count_; index++) {
            const float value = fabsf(at(index).watts);
            if (value > peakCurrent) {
                peakCurrent = value;
            }
        }
        const bool tapering = fabsf(sample.currentA) < 0.8f * (peakCurrent / fmaxf(sample.voltageV, 1.0f));
        if (span >= 180 && socDelta != 0 && tapering) {
            const float socPerSecond = static_cast<float>(socDelta) / static_cast<float>(span);
            const float socLeft = charging ? (100.0f - sample.soc) : static_cast<float>(sample.soc);
            if ((socPerSecond > 0) == charging && socPerSecond != 0) {
                trend = socLeft / fabsf(socPerSecond);
            }
        }
    }

    float seconds = coulomb;
    if (trend > 0 && (charging ? trend > coulomb : trend < coulomb)) {
        seconds = trend;
    }
    if (seconds < MIN_ESTIMATE_S) {
        seconds = MIN_ESTIMATE_S;
    }
    if (seconds > MAX_ESTIMATE_S) {
        seconds = MAX_ESTIMATE_S;
    }
    return seconds;
}
