// Diagnostic firmware, built by the "rftest" environment only.
//
// It does one thing: scan Bluetooth without Wi-Fi running and print every
// device it hears with its signal level. The point is to tell a weak antenna
// from a radio that is being shared with Wi-Fi: if the battery shows up here
// but not in the application, the coexistence between the two radios is the
// problem; if it does not show up here either, the link itself is too weak.
//
//   pio run -e rftest -t upload --upload-port COM5
//   pio device monitor
//
// Flashing this replaces the application until the normal firmware is written
// back, so it is a workshop tool and nothing else.
#include <Arduino.h>
#include <BLEDevice.h>
#include <BLEScan.h>

namespace {
// How long one round listens, and how long it rests before the next one.
const uint32_t ROUND_SECONDS = 8;
const uint32_t REST_MS = 1000;

BLEScan *scanner = nullptr;

void scanRound(int round) {
    BLEScanResults results = scanner->start(ROUND_SECONDS, false);
    const int found = results.getCount();
    Serial.printf("--- round %d: %d devices ---\n", round, found);
    for (int index = 0; index < found; index++) {
        BLEAdvertisedDevice device = results.getDevice(index);
        Serial.printf("  %s  %4d dBm  %s\n", device.getAddress().toString().c_str(),
                      device.getRSSI(), device.getName().c_str());
    }
    scanner->clearResults();
}

void scanTask(void *) {
    for (;;) {
        static int round = 0;
        scanRound(++round);
        delay(REST_MS);
    }
}
}  // namespace

void setup() {
    Serial.begin(115200);
    delay(300);
    Serial.println();
    Serial.println("BLE receive test: no Wi-Fi, no Bluetooth stack sharing");
    BLEDevice::init("rf-test");
    // Full transmit power, so scan requests reach as far as the receiver can.
    esp_ble_tx_power_set(ESP_BLE_PWR_TYPE_DEFAULT, ESP_PWR_LVL_P9);
    esp_ble_tx_power_set(ESP_BLE_PWR_TYPE_SCAN, ESP_PWR_LVL_P9);
    scanner = BLEDevice::getScan();
    scanner->setActiveScan(true);
    // No gaps: the receiver listens for the whole interval.
    scanner->setInterval(160);
    scanner->setWindow(160);
    // Scanning blocks, so it runs in its own task; the loop stays free.
    xTaskCreate(scanTask, "scan", 8192, nullptr, 1, nullptr);
}

void loop() { delay(1000); }
