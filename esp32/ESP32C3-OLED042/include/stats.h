// Readings kept in flash, so the numbers survive a restart.
//
// The file is a ring of a fixed number of records: writing a new one overwrites
// the oldest, so it can never grow past its size and never fills the partition.
// Records are written in batches (one flash write every minute), because every
// small write costs a whole block of flash and the device is meant to run for
// years.
#pragma once

#include <Arduino.h>
#include <FS.h>

#include "battery.h"

class Stats {
public:
    // begin opens (or creates) the file and picks how many readings fit: the
    // size follows the free space in the filesystem, with a reserve left alone
    // for the interface files.
    void begin();
    // record queues one reading; loop writes the queue out when it is time.
    void record(const HistoryPoint &point);
    void loop();
    void flush();

    size_t count() const { return count_; }
    size_t capacity() const { return capacity_; }
    // bytes is the size of the file on flash (header included).
    size_t bytes() const { return bytes_; }
    // written counts every reading ever stored; written - count is how many
    // fell out of the ring.
    uint32_t written() const { return written_; }
    size_t pending() const { return pendingCount_; }
    // at reads one stored reading, 0 being the oldest.
    bool at(size_t index, HistoryPoint &out);
    void clear();

private:
    void writeHeader();
    bool prepare(uint16_t &capacityOut);

    File file_;
    bool ready_ = false;
    uint16_t capacity_ = 0;
    uint16_t head_ = 0;      // next slot to write
    uint16_t count_ = 0;     // readings currently stored
    uint32_t written_ = 0;   // readings ever stored
    size_t bytes_ = 0;
    uint32_t lastFlushAt_ = 0;

    HistoryPoint pending_[8];
    size_t pendingCount_ = 0;
};
