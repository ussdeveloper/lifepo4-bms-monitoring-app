#include "stats.h"

#include <LittleFS.h>

#include "config.h"
#include "log.h"

namespace {
const char *const STATS_PATH = "/stats.bin";
const uint32_t STATS_MAGIC = 0x53544154;  // "STAT"
const uint16_t STATS_VERSION = 1;

// The header is padded so the first record starts on a round offset.
struct StatsHeader {
    uint32_t magic;
    uint16_t version;
    uint16_t recordSize;
    uint16_t capacity;
    uint16_t head;
    uint16_t count;
    uint16_t reserved;
    uint32_t written;
    uint32_t padding[3];
};
static_assert(sizeof(StatsHeader) == 32, "the header must stay 32 bytes");

// One reading as it is written: 16 bit fields everywhere, because the file has
// to hold a useful number of readings in the little space the device has.
struct StatsRecord {
    uint32_t time;
    int16_t watts;
    int16_t soc;
    uint8_t cellCount;
    uint8_t reserved;
    int16_t cellsMv[MAX_CELLS];
} __attribute__((packed));

const size_t RECORD_SIZE = sizeof(StatsRecord);

// Flash given to the readings: whatever is free, minus what the interface files
// and a firmware update need. The window is a ring, so this is a ceiling, not a
// guess that can be exceeded.
const size_t FLASH_RESERVE_BYTES = 96 * 1024;
const uint16_t MIN_POINTS = 128;
const uint16_t MAX_POINTS = 2048;

// One flash write a minute: every write costs a block of flash, and the file is
// only there to survive a restart, not to protect against a lost second.
const uint32_t FLUSH_INTERVAL_MS = 60000;

StatsHeader readHeader(File &file) {
    StatsHeader header = {};
    file.seek(0, SeekSet);
    if (file.read(reinterpret_cast<uint8_t *>(&header), sizeof(header)) != sizeof(header)) {
        header.magic = 0;
    }
    return header;
}

void fillRecord(StatsRecord &record, const HistoryPoint &point) {
    record.time = point.time;
    record.watts = static_cast<int16_t>(constrain(point.watts, -32000.0f, 32000.0f));
    record.soc = point.soc;
    record.cellCount = static_cast<uint8_t>(constrain(point.cellCount, 0, MAX_CELLS));
    record.reserved = 0;
    for (int cell = 0; cell < MAX_CELLS; cell++) {
        record.cellsMv[cell] = point.cellsMv[cell];
    }
}

void fillPoint(HistoryPoint &point, const StatsRecord &record) {
    point.time = record.time;
    point.watts = record.watts;
    point.soc = record.soc;
    point.cellCount = record.cellCount > MAX_CELLS ? MAX_CELLS : record.cellCount;
    for (int cell = 0; cell < MAX_CELLS; cell++) {
        point.cellsMv[cell] = record.cellsMv[cell];
    }
}
}  // namespace

void Stats::begin() {
    if (file_) {
        file_.close();
    }

    // How many readings fit: what is free, less the reserve for the interface.
    size_t freeBytes = 0;
    if (LittleFS.totalBytes() > LittleFS.usedBytes()) {
        freeBytes = LittleFS.totalBytes() - LittleFS.usedBytes();
    }
    File existing = LittleFS.open(STATS_PATH, "r");
    size_t existingBytes = existing ? existing.size() : 0;
    if (existing) {
        existing.close();
    }
    const size_t budget = freeBytes + existingBytes;
    size_t fit = budget > FLASH_RESERVE_BYTES ? (budget - FLASH_RESERVE_BYTES) / RECORD_SIZE : 0;
    if (fit > MAX_POINTS) {
        fit = MAX_POINTS;
    }
    if (fit < MIN_POINTS) {
        fit = MIN_POINTS;
    }
    uint16_t capacity = static_cast<uint16_t>(fit);

    StatsHeader header = {};
    bool usable = false;
    file_ = LittleFS.open(STATS_PATH, "r+");
    if (file_) {
        header = readHeader(file_);
        const size_t wantedSize =
            sizeof(StatsHeader) + static_cast<size_t>(capacity) * RECORD_SIZE;
        usable = header.magic == STATS_MAGIC && header.version == STATS_VERSION &&
                 header.recordSize == RECORD_SIZE && header.capacity == capacity &&
                 header.count <= capacity && header.head < capacity &&
                 file_.size() >= sizeof(StatsHeader) && file_.size() <= wantedSize;
        if (!usable) {
            logLine("stats", "rebuilding the file: magic " + String(header.magic, 16) +
                                 " version " + String(header.version) + " record " +
                                 String(header.recordSize) + " capacity " +
                                 String(header.capacity) + "/" + String(capacity) + " count " +
                                 String(header.count) + " size " + String(file_.size()) + "/" +
                                 String(wantedSize));
        }
    }
    if (!usable) {
        // A new file, or one written with a different size: it is rebuilt with
        // the readings it still holds, newest ones first. The new file is
        // written next to the old one: opening it under the same name would
        // truncate the readings that are being copied.
        if (file_) {
            file_.close();
        }
        const char *TEMP_PATH = "/stats.tmp";
        File old = LittleFS.open(STATS_PATH, "r");
        File fresh = LittleFS.open(TEMP_PATH, "w");
        if (!fresh) {
            logLine("stats", "the statistics file cannot be created");
            ready_ = false;
            return;
        }
        size_t kept = 0;
        if (old) {
            const StatsHeader oldHeader = readHeader(old);
            const size_t oldCount = oldHeader.magic == STATS_MAGIC ? oldHeader.count : 0;
            const size_t want = oldCount < capacity ? oldCount : capacity;
            // The newest readings are the last ones in the ring.
            for (size_t index = oldCount - want; oldCount > 0 && index < oldCount; index++) {
                StatsRecord record = {};
                const size_t slot = (oldHeader.head + index) % oldHeader.capacity;
                old.seek(sizeof(StatsHeader) + slot * RECORD_SIZE, SeekSet);
                if (old.read(reinterpret_cast<uint8_t *>(&record), RECORD_SIZE) != RECORD_SIZE) {
                    break;
                }
                fresh.seek(sizeof(StatsHeader) + kept * RECORD_SIZE, SeekSet);
                fresh.write(reinterpret_cast<const uint8_t *>(&record), RECORD_SIZE);
                kept++;
            }
            old.close();
        }
        header = {};
        header.magic = STATS_MAGIC;
        header.version = STATS_VERSION;
        header.recordSize = RECORD_SIZE;
        header.capacity = capacity;
        header.head = static_cast<uint16_t>(kept % capacity);
        header.count = static_cast<uint16_t>(kept);
        header.written = static_cast<uint32_t>(kept);
        fresh.seek(0, SeekSet);
        fresh.write(reinterpret_cast<const uint8_t *>(&header), sizeof(header));
        // The file takes its full size straight away: the readings are written
        // at fixed offsets, and a file that grows on every write would have to
        // be extended in the middle, which costs more flash than it saves.
        const size_t wantedSize = sizeof(StatsHeader) + static_cast<size_t>(capacity) * RECORD_SIZE;
        fresh.seek(wantedSize - 1, SeekSet);
        const uint8_t zero = 0;
        fresh.write(&zero, 1);
        fresh.close();
        // The rebuilt file has to take the place of the old one, or the next
        // open finds nothing (on a fresh filesystem there is no old file at all).
        LittleFS.remove(STATS_PATH);
        if (!LittleFS.rename(TEMP_PATH, STATS_PATH)) {
            logLine("stats", "the statistics file cannot be renamed into place");
        }
        file_ = LittleFS.open(STATS_PATH, "r+");
        if (!file_) {
            logLine("stats", "the statistics file cannot be reopened");
            ready_ = false;
            return;
        }
    }

    capacity_ = header.capacity;
    head_ = header.head;
    count_ = header.count;
    written_ = header.written;
    bytes_ = sizeof(StatsHeader) + static_cast<size_t>(capacity_) * RECORD_SIZE;
    file_.seek(0, SeekSet);
    ready_ = true;
    pendingCount_ = 0;
    lastFlushAt_ = millis();
    logLine("stats", String(count_) + " readings in flash of " + String(capacity_) +
                        " (" + String(bytes_ / 1024) + " KB)");
}

void Stats::record(const HistoryPoint &point) {
    if (!ready_ || point.time == 0) {
        return;
    }
    if (pendingCount_ >= (sizeof(pending_) / sizeof(pending_[0]))) {
        flush();
    }
    if (pendingCount_ < (sizeof(pending_) / sizeof(pending_[0]))) {
        pending_[pendingCount_++] = point;
    }
}

void Stats::loop() {
    if (pendingCount_ == 0) {
        return;
    }
    if (millis() - lastFlushAt_ >= FLUSH_INTERVAL_MS) {
        flush();
    }
}

void Stats::flush() {
    if (!ready_ || pendingCount_ == 0 || !file_) {
        return;
    }
    for (size_t index = 0; index < pendingCount_; index++) {
        StatsRecord record = {};
        fillRecord(record, pending_[index]);
        file_.seek(sizeof(StatsHeader) + static_cast<size_t>(head_) * RECORD_SIZE, SeekSet);
        if (file_.write(reinterpret_cast<const uint8_t *>(&record), RECORD_SIZE) != RECORD_SIZE) {
            logLine("stats", "a reading could not be written to flash");
            pendingCount_ = 0;
            return;
        }
        head_ = static_cast<uint16_t>((head_ + 1) % capacity_);
        if (count_ < capacity_) {
            count_++;
        }
        written_++;
    }
    pendingCount_ = 0;
    writeHeader();
    // LittleFS keeps small writes in a block cache and only commits them when
    // the file is closed or synced. Without this the readings sit in the cache
    // and a restart throws them away, leaving an empty file behind.
    file_.flush();
    lastFlushAt_ = millis();
}

void Stats::writeHeader() {
    if (!file_) {
        return;
    }
    StatsHeader header = {};
    header.magic = STATS_MAGIC;
    header.version = STATS_VERSION;
    header.recordSize = RECORD_SIZE;
    header.capacity = capacity_;
    header.head = head_;
    header.count = count_;
    header.written = written_;
    file_.seek(0, SeekSet);
    file_.write(reinterpret_cast<const uint8_t *>(&header), sizeof(header));
}

bool Stats::at(size_t index, HistoryPoint &out) {
    if (!ready_ || index >= count_ || !file_) {
        return false;
    }
    const size_t slot = (head_ + capacity_ - count_ + index) % capacity_;
    file_.seek(sizeof(StatsHeader) + slot * RECORD_SIZE, SeekSet);
    StatsRecord record = {};
    if (file_.read(reinterpret_cast<uint8_t *>(&record), RECORD_SIZE) != RECORD_SIZE) {
        return false;
    }
    fillPoint(out, record);
    return true;
}

void Stats::clear() {
    pendingCount_ = 0;
    head_ = 0;
    count_ = 0;
    written_ = 0;
    writeHeader();
    if (file_) {
        file_.flush();
    }
    logLine("stats", "the statistics were cleared");
}
