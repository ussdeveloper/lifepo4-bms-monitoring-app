// Bluetooth LE central: finds the battery, connects to it, reads it in a loop
// and hands the readings over. The command sequence and the framing are the
// same as in the desktop application (protocol.go of the main project).
//
// Everything happens in its own FreeRTOS task, because scanning and connecting
// block: the interface must keep answering while the device looks for the
// battery.
#pragma once

#include <Arduino.h>
#include <functional>

#include "appconfig.h"
#include "battery.h"

class Bms {
public:
    void begin(AppConfig *config);

    bool connected() const { return connected_; }
    const String &address() const { return address_; }
    const String &name() const { return name_; }
    const String &model() const { return model_; }
    const String &brand() const { return brand_; }
    const String &protocol() const { return protocol_; }
    const String &error() const { return error_; }
    int rssi() const { return rssi_; }
    // True while the battery does not answer, which usually means another
    // application (a phone, the desktop monitor) took it over.
    bool takeover() const { return takeover_; }
    uint32_t adverts() const { return adverts_; }
    uint32_t devicesSeen() const { return devicesSeen_; }
    uint32_t scanRestarts() const { return scanRestarts_; }
    uint32_t attempts() const { return attempts_; }
    uint32_t samples() const { return samples_; }
    uint32_t releasedSeconds() const;

    // One device the scanner saw: the interface lists these, which is how the
    // "why does it not find my battery" question gets an answer.
    struct SeenDevice {
        String address;
        String name;
        int rssi = 0;
        bool battery = false;
    };
    static const size_t MAX_SEEN = 24;
    size_t seenCount() const { return seenCount_; }
    const SeenDevice &seen(size_t index) const { return seen_[index]; }

    // Called for every complete reading (from the BLE task).
    std::function<void(const Sample &)> onSample;
    // Called whenever the connection state changes.
    std::function<void()> onState;

    // watch tells the scanner to go for one address (empty = find one itself).
    // watch makes this battery the one to read, forceConnect just asks for an
    // attempt right now.
    void watch(const String &address);
    void forceConnect();    // deepScan forgets the current wait and scans again right away.
    void deepScan();
    // pause stops the radio work: while the device offers its setup access
    // point, Bluetooth stays quiet so the access point is easy to find (one
    // radio serves both, and it cannot be busy with a battery hunt).
    void pause(bool paused);
    bool paused() const { return paused_; }
    // release stops monitoring for the given time so another device can take
    // the battery: while this device is connected the BMS stops advertising.
    void release(uint32_t seconds);

    // Called by the BLE stack when the BMS sends an answer.
    void handleNotification(const uint8_t *data, size_t length);

    // The state machine; runs in its own task.
    void run();

private:
    enum class State { Idle, Scanning, Connecting, Ready, Polling, Releasing };

    void startScan();
    void finishScan();
    void tryConnect(const String &address, uint32_t addressType);
    // restartBluetooth hands back the registration slots the library leaks on
    // every failed connection attempt.
    void restartBluetooth();
    // tryKnownBatteries walks the remembered list: a battery stops advertising
    // while another device holds it, but it still answers a direct connection,
    // so the device keeps asking the addresses it knows.
    void tryKnownBatteries();
    void disconnect();
    void startPolling();
    void pollBattery();
    void sendCommand(uint8_t command);
    void takeNotification();
    void decode();
    void fail(const String &message);

    AppConfig *config_ = nullptr;
    State state_ = State::Idle;
    volatile bool connected_ = false;
    volatile bool takeover_ = false;
    volatile bool paused_ = false;
    bool bleStarted_ = false;
    uint8_t connectFailures_ = 0;
    // How many times the Bluetooth stack was restarted in a row; the wait
    // between attempts grows with it.
    uint8_t stackRestarts_ = 0;
    String address_;
    String name_;
    String model_;
    String brand_ = "HiLink";
    String protocol_;
    String error_;
    int rssi_ = 0;
    uint32_t adverts_ = 0;
    uint32_t devicesSeen_ = 0;
    uint32_t scanRestarts_ = 0;
    uint32_t attempts_ = 0;
    uint32_t samples_ = 0;
    uint32_t nextActionAt_ = 0;
    uint32_t releaseUntil_ = 0;
    uint32_t addressType_ = 0;
    bool modelRead_ = false;
    // Which remembered battery is asked next when a scan finds nothing.
    size_t knownIndex_ = 0;
    uint32_t knownRetryAt_ = 0;

    // One command at a time: the BMS answers each write with a notification.
    static const size_t RX_SIZE = 512;
    uint8_t rx_[RX_SIZE] = {0};
    volatile size_t rxLength_ = 0;
    volatile bool rxReady_ = false;
    uint8_t awaiting_ = 0;
    uint32_t awaitingUntil_ = 0;

    uint8_t queue_[4] = {0, 0, 0, 0};
    size_t queueCount_ = 0;
    size_t queueIndex_ = 0;

    uint8_t analog_[64] = {0};
    size_t analogLength_ = 0;
    uint8_t status_[16] = {0};
    size_t statusLength_ = 0;
    uint8_t cells_[64] = {0};
    size_t cellsLength_ = 0;

    // The devices of the last scan round, strongest first.
    SeenDevice seen_[MAX_SEEN];
    size_t seenCount_ = 0;
};
