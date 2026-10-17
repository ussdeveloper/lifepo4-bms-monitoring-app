#include "log.h"

namespace {
const size_t LOG_LINES = 60;
String lines[LOG_LINES];
size_t nextLine = 0;
size_t lineCount = 0;
bool verbose = false;
}  // namespace

void logSetVerbose(bool value) { verbose = value; }

bool logVerbose() { return verbose; }

void logLine(const char *tag, const String &message) {
    const uint32_t seconds = millis() / 1000;
    String line;
    line.reserve(message.length() + 16);
    line += '[';
    line += String(seconds / 60);
    line += ':';
    if (seconds % 60 < 10) {
        line += '0';
    }
    line += String(seconds % 60);
    line += "] ";
    line += tag;
    line += ": ";
    line += message;
    Serial.println(line);

    lines[nextLine] = line;
    nextLine = (nextLine + 1) % LOG_LINES;
    if (lineCount < LOG_LINES) {
        lineCount++;
    }
}

String logTail(size_t maximumLines) {
    String out;
    const size_t wanted = (maximumLines < lineCount) ? maximumLines : lineCount;
    const size_t first = (nextLine + LOG_LINES - wanted) % LOG_LINES;
    for (size_t index = 0; index < wanted; index++) {
        out += lines[(first + index) % LOG_LINES];
        out += '\n';
    }
    return out;
}
