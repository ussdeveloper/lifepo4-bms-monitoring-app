#include "log.h"

#include <stdio.h>

namespace {
// The tail of the log is a fixed array of characters, not a ring of String
// objects. Strings would allocate and free a buffer for every line, and after a
// few hours that fragments the heap badly enough that some allocation fails and
// the device stops. Fixed buffers cannot fragment: they are the same bytes from
// boot to shutdown.
const size_t LOG_LINES = 40;
const size_t LOG_LINE_CHARS = 128;
char lines[LOG_LINES][LOG_LINE_CHARS];
size_t nextLine = 0;
size_t lineCount = 0;
bool verbose = false;
}  // namespace

void logSetVerbose(bool value) { verbose = value; }

bool logVerbose() { return verbose; }

void logLine(const char *tag, const String &message) {
    const uint32_t seconds = millis() / 1000;
    char *slot = lines[nextLine];
    snprintf(slot, LOG_LINE_CHARS, "[%lu:%02lu] %s: %s",
             static_cast<unsigned long>(seconds / 60), static_cast<unsigned long>(seconds % 60),
             tag != nullptr ? tag : "", message.c_str());
    Serial.println(slot);

    nextLine = (nextLine + 1) % LOG_LINES;
    if (lineCount < LOG_LINES) {
        lineCount++;
    }
}

String logTail(size_t maximumLines) {
    const size_t wanted = (maximumLines < lineCount) ? maximumLines : lineCount;
    const size_t first = (nextLine + LOG_LINES - wanted) % LOG_LINES;
    // Reserve up front so the whole tail is built with one allocation instead
    // of one per line.
    String out;
    out.reserve(wanted * LOG_LINE_CHARS);
    for (size_t index = 0; index < wanted; index++) {
        out += lines[(first + index) % LOG_LINES];
        out += '\n';
    }
    return out;
}
