// A very small log: everything goes to the serial port (which on this board is
// the USB port itself) and the last lines are kept in memory, so the interface
// can show them - the device counterpart of the desktop log file.
#pragma once

#include <Arduino.h>

void logLine(const char *tag, const String &message);
// The last lines, oldest first, for the interface.
String logTail(size_t maximumLines = 40);
void logSetVerbose(bool verbose);
bool logVerbose();
