#include "bms.h"

#include <BLEDevice.h>
#include <BLEScan.h>
#include <string.h>

#include "config.h"
#include "log.h"

namespace {
// How long the device waits before asking a remembered battery directly when it
// cannot be heard in the air. A connect attempt blocks the Bluetooth task for up
// to half a minute, so this is deliberately long: listening is what finds a
// battery, and a direct attempt is the exception for one that is held by another
// device and therefore silent.
const uint32_t KNOWN_RETRY_MS = 45000;
// Failed connection attempts before the Bluetooth stack is restarted: each one
// uses up a registration slot that the library never gives back on its own.
const uint8_t CONNECT_FAILURES_BEFORE_RESTART = 3;
}  // namespace

namespace {
// The HS03 (HiLink) service of the battery.
BLEUUID SERVICE_HS03((uint16_t)0x0001);
BLEUUID CHAR_WRITE_HS03((uint16_t)0x0002);
BLEUUID CHAR_NOTIFY_HS03((uint16_t)0x0003);

const uint8_t COMMAND_INIT = 0x00;
const uint8_t COMMAND_MODEL = 0x11;
const uint8_t COMMAND_STATUS = 0x20;
const uint8_t COMMAND_ANALOG = 0x21;
const uint8_t COMMAND_CELLS = 0x22;
const uint32_t COMMAND_TIMEOUT_MS = 4000;

BLEClient *client = nullptr;
BLERemoteCharacteristic *writeCharacteristic = nullptr;
BLEScan *scanner = nullptr;
Bms *activeInstance = nullptr;  // the BLE library takes plain function pointers

// buildCommand frames one request: AA <command> 00 <command> 00. The BMS
// checksums its answers itself.
void buildCommand(uint8_t command, uint8_t *out) {
    out[0] = 0xAA;
    out[1] = command;
    out[2] = 0x00;
    out[3] = command;
    out[4] = 0x00;
}

void notifyCallback(BLERemoteCharacteristic *characteristic, uint8_t *data, size_t length,
                    bool isNotify) {
    (void)characteristic;
    (void)isNotify;
    if (activeInstance != nullptr) {
        activeInstance->handleNotification(data, length);
    }
}

// A name is only a hint; the real check is the service the device exposes.
bool looksLikeBattery(const String &name) {
    if (name.length() == 0) {
        return false;
    }
    return name.startsWith("HS03") || name.startsWith("HS04") || name.indexOf("HiLink") >= 0 ||
           name.indexOf("BMS") >= 0 || name.indexOf("Battery") >= 0 ||
           name.indexOf("battery") >= 0 || name.indexOf("Humsienk") >= 0;
}

void bmsTask(void *parameter) { static_cast<Bms *>(parameter)->run(); }
}  // namespace

void Bms::begin(AppConfig *config) {
    config_ = config;
    activeInstance = this;
    address_ = config->batteryAddress;
    name_ = config->batteryName;

    scanner = BLEDevice::getScan();
    scanner->setActiveScan(true);
    // A wide listening window: with Wi-Fi on, only the time the scan actually
    // listens is left for hearing a battery that does not advertise often.
    scanner->setInterval(BLE_SCAN_INTERVAL);
    scanner->setWindow(BLE_SCAN_WINDOW);

    // The stack itself is started by the task below (and stopped again while
    // the setup access point is up), so nothing here touches the radio.
    xTaskCreate(bmsTask, "bms", 8192, this, 1, nullptr);
    logLine("bms", "Bluetooth task started");
}

uint32_t Bms::releasedSeconds() const {
    if (releaseUntil_ == 0) {
        return 0;
    }
    const uint32_t now = millis();
    if (static_cast<int32_t>(releaseUntil_ - now) <= 0) {
        return 0;
    }
    return (releaseUntil_ - now) / 1000;
}

void Bms::watch(const String &address) {
    // A direct address works even when the battery does not advertise (another
    // device is holding it), which is exactly what a phone does to take over.
    disconnect();
    address_ = address;
    nextActionAt_ = millis();
    state_ = State::Idle;
}

void Bms::deepScan() {
    disconnect();
    scanRestarts_++;
    nextActionAt_ = millis();
    state_ = State::Idle;
    logLine("bms", "scan restarted on request");
}

void Bms::release(uint32_t seconds) {
    disconnect();
    releaseUntil_ = millis() + seconds * 1000UL;
    state_ = State::Releasing;
    logLine("bms", "battery released for " + String(seconds / 60) + " min");
    if (onState) {
        onState();
    }
}

void Bms::pause(bool value) {
    if (paused_ == value) {
        return;
    }
    // Only the flag is set here; the BLE task itself does the radio work (the
    // Bluetooth stack may only be touched from the task that owns it).
    paused_ = value;
    logLine("bms", value ? "Bluetooth will pause for the access point" : "Bluetooth resumes");
}

void Bms::forceConnect() {
    // The button in the interface: whatever pause the state machine is in, the
    // next round goes straight at the remembered battery. A connect attempt
    // already in flight cannot be cut short (it is a blocking call inside the
    // Bluetooth task), so this takes effect right after it ends.
    knownRetryAt_ = 0;
    nextActionAt_ = millis();
    if (address_.length() == 0 && config_ != nullptr && config_->knownCount() > 0) {
        address_ = config_->known(0).address;
        name_ = config_->known(0).name;
    }
    logLine("bms", "connect requested by hand");
}

void Bms::run() {
    for (;;) {
        if (paused_) {
            // The setup access point gets the whole radio: the Bluetooth stack
            // is shut down until the device is on a network again.
            if (bleStarted_) {
                disconnect();
                // release_memory stays false: with it the library forgets to
                // mark the stack as stopped, and the restart below would be
                // skipped.
                BLEDevice::deinit(false);
                bleStarted_ = false;
                logLine("bms", "Bluetooth off, the access point has the radio");
            }
            delay(200);
            continue;
        }
        if (!bleStarted_) {
            BLEDevice::init("LiFePO4-BMS");
            // Full transmit power: a battery at the edge of the range hears the
            // connection request the first time instead of timing out, and the
            // scan requests behind the names come back more often.
            esp_ble_tx_power_set(ESP_BLE_PWR_TYPE_DEFAULT, ESP_PWR_LVL_P9);
            esp_ble_tx_power_set(ESP_BLE_PWR_TYPE_SCAN, ESP_PWR_LVL_P9);
            esp_ble_tx_power_set(ESP_BLE_PWR_TYPE_ADV, ESP_PWR_LVL_P9);
            scanner = BLEDevice::getScan();
            scanner->setActiveScan(true);
            // Active scanning asks every device for its name, which doubles the
            // packets a round receives.
            scanner->setInterval(BLE_SCAN_INTERVAL);
            scanner->setWindow(BLE_SCAN_WINDOW);
            bleStarted_ = true;
            nextActionAt_ = millis();
            // Listening comes first: the first direct attempt is deferred, so a
            // battery that is awake is found by a scan instead of being missed
            // while the task is stuck in a connect that cannot succeed.
            knownRetryAt_ = millis() + KNOWN_RETRY_MS;
            logLine("bms", "Bluetooth ready: listening " + String(BLE_SCAN_SECONDS) +
                               " s in a row every " +
                               String(BLE_SCAN_SECONDS + BLE_SCAN_PAUSE_MS / 1000) + " s, full power");
        }
        const uint32_t now = millis();

        if (state_ == State::Releasing) {
            if (releasedSeconds() == 0) {
                releaseUntil_ = 0;
                nextActionAt_ = now;
                state_ = State::Idle;
                logLine("bms", "release finished, taking the battery back");
                if (onState) {
                    onState();
                }
            }
            delay(200);
            continue;
        }

        switch (state_) {
            case State::Idle:
                if (static_cast<int32_t>(now - nextActionAt_) < 0) {
                    break;
                }
                startScan();
                break;

            case State::Scanning:
                finishScan();
                break;

            case State::Connecting:
                if (static_cast<int32_t>(now - nextActionAt_) >= 0) {
                    fail("the battery did not answer the connection");
                }
                break;

            case State::Ready:
                if (!client->isConnected()) {
                    fail("the connection to the battery was lost");
                    break;
                }
                if (static_cast<int32_t>(now - nextActionAt_) >= 0) {
                    startPolling();
                }
                break;

            case State::Polling:
                if (!client->isConnected()) {
                    fail("the connection to the battery was lost");
                    break;
                }
                pollBattery();
                break;

            case State::Releasing:
                break;
        }
        delay(20);
    }
}

void Bms::startScan() {
    if (scanner == nullptr) {
        return;
    }
    logLine("bms", "scanning for the battery");
    // start() blocks for the scan duration; this runs in the BLE task, so the
    // interface keeps answering meanwhile.
    BLEScanResults results = scanner->start(BLE_SCAN_SECONDS, false);
    (void)results;
    finishScan();
}

void Bms::finishScan() {
    if (scanner == nullptr) {
        state_ = State::Idle;
        nextActionAt_ = millis() + 2000;
        return;
    }
    BLEScanResults results = scanner->getResults();
    const int found = results.getCount();
    String candidate;
    uint32_t candidateType = BLE_ADDR_TYPE_PUBLIC;
    const String previousAddress = address_;
    String candidateName;
    int candidateRssi = -32768;
    uint32_t fresh = 0;
    seenCount_ = 0;

    for (int index = 0; index < found; index++) {
        BLEAdvertisedDevice device = results.getDevice(index);
        adverts_++;
        const String name = String(device.getName().c_str());
        const String deviceAddress = String(device.getAddress().toString().c_str());
        if (deviceAddress.length() > 0) {
            fresh++;
        }
        // A battery from the remembered list is a candidate even when it does
        // not advertise a name (they often do not): the address is the proof.
        const bool known = config_ != nullptr && config_->isKnownAddress(deviceAddress);
        const bool isBattery = known || looksLikeBattery(name);
        // The list the interface shows: this is what makes it obvious whether
        // the scan hears anything at all.
        if (seenCount_ < MAX_SEEN && deviceAddress.length() > 0) {
            SeenDevice &entry = seen_[seenCount_++];
            entry.address = deviceAddress;
            entry.name = name;
            entry.rssi = device.getRSSI();
            entry.battery = isBattery;
        }
        if (!isBattery) {
            continue;
        }
        // Everything that looks like a battery is remembered for good, so it is
        // looked for again after a restart.
        if (config_ != nullptr && !known) {
            config_->rememberBattery(deviceAddress, name);
        }
        // A known battery wins over an unknown one, a stronger signal over a
        // weaker one.
        const bool better = candidate.length() == 0 ||
                            (known && !config_->isKnownAddress(candidate)) ||
                            (device.getRSSI() > candidateRssi &&
                             known == config_->isKnownAddress(candidate));
        if (better) {
            candidate = deviceAddress;
            candidateType = device.getAddressType();
            candidateName = name;
            candidateRssi = device.getRSSI();
        }
    }
    scanner->clearResults();
    devicesSeen_ = fresh;

    if (candidate.length() == 0) {
        // Nothing recognised in the air: the remembered batteries are asked
        // directly, because a BMS that another device holds does not advertise.
        tryKnownBatteries();
        if (state_ != State::Connecting) {
            nextActionAt_ = millis() + BLE_SCAN_PAUSE_MS;
            state_ = State::Idle;
        }
        if (address_.length() == 0 && previousAddress.length() > 0) {
            address_ = previousAddress;
        }
        return;
    }
    name_ = candidateName;
    rssi_ = candidateRssi;
    if (config_ != nullptr) {
        config_->rememberBattery(candidate, candidateName);
    }
    tryConnect(candidate, candidateType);
}

void Bms::tryKnownBatteries() {
    if (config_ == nullptr || config_->knownCount() == 0) {
        return;
    }
    // One address per round, in turn, with a pause in between: this is the
    // "keep looking for the batteries I know" behaviour, and it stays polite to
    // the radio that Wi-Fi shares.
    if (static_cast<int32_t>(millis() - knownRetryAt_) < 0) {
        return;
    }
    knownRetryAt_ = millis() + KNOWN_RETRY_MS;
    if (knownIndex_ >= config_->knownCount()) {
        knownIndex_ = 0;
    }
    const KnownBattery &battery = config_->known(knownIndex_++);
    logLine("bms", "asking " + battery.address + " directly");
    name_ = battery.name;
    tryConnect(battery.address, BLE_ADDR_TYPE_RANDOM);
}

void Bms::restartBluetooth() {
    // The Arduino Bluetooth client registers a GATT application on every
    // connection attempt, and a battery that is not there uses those slots up.
    // The stack has a handful of them, so after a few failures every later
    // attempt is refused ("Deregister Failed unknown client cif") — a battery
    // that is plainly visible but can never be reached. Restarting the stack
    // hands all the slots back.
    //
    // Restarting it costs memory and time, so it is done less and less often
    // while the battery stays away: a device that cycles its Bluetooth stack
    // every couple of minutes for hours is a device that runs out of memory.
    if (stackRestarts_ < 6) {
        stackRestarts_++;
    }
    uint32_t backoff = 60000UL << (stackRestarts_ > 4 ? 4 : (stackRestarts_ - 1));
    if (backoff > 15UL * 60UL * 1000UL) {
        backoff = 15UL * 60UL * 1000UL;
    }
    knownRetryAt_ = millis() + backoff;
    logLine("bms", "restarting Bluetooth after failed connections, next attempt in " +
                       String(backoff / 60000) + " min");
    if (client != nullptr) {
        client->disconnect();
        delete client;
        client = nullptr;
    }
    writeCharacteristic = nullptr;
    connected_ = false;
    bleStarted_ = false;
    BLEDevice::deinit(false);
    connectFailures_ = 0;
    if (onState) {
        onState();
    }
}

void Bms::tryConnect(const String &target, uint32_t addressType) {
    if (!bleStarted_) {
        return;
    }
    if (client == nullptr) {
        client = BLEDevice::createClient();
    }
    state_ = State::Connecting;
    nextActionAt_ = millis() + BLE_CONNECT_TIMEOUT_MS;
    attempts_++;

    BLEAddress bleAddress(target.c_str());
    esp_ble_addr_type_t type = static_cast<esp_ble_addr_type_t>(addressType);
    if (!client->connect(bleAddress, type)) {
        // Every failed call uses up one of the stack's registration slots, so
        // the second guess at the address type only happens while there is
        // room left for it.
        if (++connectFailures_ >= CONNECT_FAILURES_BEFORE_RESTART) {
            restartBluetooth();
            fail("could not connect to " + target);
            return;
        }
        // A BMS usually uses a random (static) address; the first attempt has
        // to guess, so try the other kind before giving up.
        type = (type == BLE_ADDR_TYPE_PUBLIC) ? BLE_ADDR_TYPE_RANDOM : BLE_ADDR_TYPE_PUBLIC;
        if (!client->connect(bleAddress, type)) {
            if (++connectFailures_ >= CONNECT_FAILURES_BEFORE_RESTART) {
                restartBluetooth();
            }
            fail("could not connect to " + target);
            return;
        }
        addressType_ = type;
    } else {
        addressType_ = type;
    }
    connectFailures_ = 0;

    BLERemoteService *service = client->getService(SERVICE_HS03);
    if (service == nullptr) {
        fail("the device does not speak the HiLink protocol (0001 service)");
        return;
    }
    BLERemoteCharacteristic *notifyCharacteristic = service->getCharacteristic(CHAR_NOTIFY_HS03);
    writeCharacteristic = service->getCharacteristic(CHAR_WRITE_HS03);
    if (notifyCharacteristic == nullptr || writeCharacteristic == nullptr) {
        fail("the battery is missing its data characteristics");
        return;
    }
    if (!notifyCharacteristic->canNotify()) {
        fail("the battery does not send notifications");
        return;
    }
    notifyCharacteristic->registerForNotify(notifyCallback);

    address_ = target;
    connected_ = true;
    takeover_ = false;
    error_ = "";
    // A battery that answers proves the stack is healthy again: the next
    // failure starts the backoff from the beginning.
    stackRestarts_ = 0;
    protocol_ = "HS03/HiLink";
    modelRead_ = false;
    analogLength_ = 0;
    statusLength_ = 0;
    cellsLength_ = 0;
    awaiting_ = 0;
    queueCount_ = 0;
    queueIndex_ = 0;

    if (config_ != nullptr) {
        // The battery that answers is the preferred one from now on, and what
        // the device learned about it is kept in flash.
        config_->rememberBattery(target, name_);
        config_->promote(target);
        config_->batteryAddress = target;
        if (name_.length() > 0) {
            config_->batteryName = name_;
        }
        config_->save();
    }

    // The BMS wants a "hello" before it answers anything.
    uint8_t frame[5];
    buildCommand(COMMAND_INIT, frame);
    writeCharacteristic->writeValue(frame, sizeof(frame), true);
    delay(60);

    state_ = State::Ready;
    nextActionAt_ = millis() + 300;
    logLine("bms", "connected to " + target);
    if (onState) {
        onState();
    }
}

void Bms::startPolling() {
    queue_[0] = COMMAND_ANALOG;
    queue_[1] = COMMAND_STATUS;
    queue_[2] = COMMAND_CELLS;
    queueCount_ = 3;
    queueIndex_ = 0;
    if (!modelRead_) {
        // The model name only has to be read once per connection.
        queue_[3] = COMMAND_MODEL;
        queueCount_ = 4;
        modelRead_ = true;
    }
    state_ = State::Polling;
}

void Bms::pollBattery() {
    const uint32_t now = millis();

    if (awaiting_ != 0) {
        if (static_cast<int32_t>(now - awaitingUntil_) >= 0) {
            logLine("bms", "the BMS did not answer command 0x" + String(awaiting_, HEX));
            awaiting_ = 0;
        } else {
            return;
        }
    }
    if (rxReady_) {
        takeNotification();
        return;
    }
    if (queueIndex_ < queueCount_) {
        sendCommand(queue_[queueIndex_++]);
        return;
    }

    decode();
    queueCount_ = 0;
    queueIndex_ = 0;
    state_ = State::Ready;
    nextActionAt_ = millis() + (config_ != nullptr ? config_->intervalMs : DEFAULT_INTERVAL_MS);
}

void Bms::sendCommand(uint8_t command) {
    if (writeCharacteristic == nullptr || client == nullptr || !client->isConnected()) {
        return;
    }
    uint8_t frame[5];
    buildCommand(command, frame);
    rxReady_ = false;
    rxLength_ = 0;
    awaiting_ = command;
    awaitingUntil_ = millis() + COMMAND_TIMEOUT_MS;
    writeCharacteristic->writeValue(frame, sizeof(frame), true);
}

void Bms::handleNotification(const uint8_t *data, size_t length) {
    if (rxReady_ || data == nullptr || length == 0 || length > RX_SIZE) {
        return;
    }
    memcpy(rx_, data, length);
    rxLength_ = length;
    rxReady_ = true;
}

void Bms::takeNotification() {
    rxReady_ = false;
    const size_t length = rxLength_;
    if (length < 5 || rx_[0] != 0xAA) {
        return;
    }
    const size_t expected = static_cast<size_t>(rx_[2]) + 5;
    if (expected > length) {
        return;
    }
    uint16_t sum = 0;
    for (size_t index = 1; index + 2 < expected; index++) {
        sum += rx_[index];
    }
    const uint16_t want = static_cast<uint16_t>(rx_[expected - 2]) |
                          (static_cast<uint16_t>(rx_[expected - 1]) << 8);
    if (sum != want) {
        logLine("bms", "the BMS answer failed its checksum");
        awaiting_ = 0;
        return;
    }
    const uint8_t command = rx_[1];
    const uint8_t *payload = rx_ + 3;
    const size_t payloadLength = expected - 5;
    switch (command) {
        case COMMAND_MODEL:
            model_ = Battery::decodeModel(payload, payloadLength);
            if (model_.length() > 0 && config_ != nullptr) {
                // The real name of the battery, straight from the BMS: this is
                // what the list shows instead of a bare address.
                if (config_->rememberBattery(address_, model_)) {
                    config_->save();
                }
                config_->batteryName = model_;
                name_ = model_;
            }
            break;
        case COMMAND_ANALOG:
            analogLength_ = payloadLength < sizeof(analog_) ? payloadLength : sizeof(analog_);
            memcpy(analog_, payload, analogLength_);
            break;
        case COMMAND_STATUS:
            statusLength_ = payloadLength < sizeof(status_) ? payloadLength : sizeof(status_);
            memcpy(status_, payload, statusLength_);
            break;
        case COMMAND_CELLS:
            cellsLength_ = payloadLength < sizeof(cells_) ? payloadLength : sizeof(cells_);
            memcpy(cells_, payload, cellsLength_);
            break;
        default:
            break;
    }
    if (awaiting_ == command) {
        awaiting_ = 0;
    }
}

void Bms::decode() {
    Sample sample;
    if (!Battery::decodeHS03(analog_, analogLength_, status_, statusLength_, cells_, cellsLength_,
                             sample)) {
        logLine("bms", "the reading was incomplete");
        return;
    }
    sample.timestamp = static_cast<uint32_t>(time(nullptr));
    samples_++;
    if (config_ != nullptr) {
        config_->touchBattery(address_);
    }
    if (onSample) {
        onSample(sample);
    }
}

void Bms::fail(const String &message) {
    error_ = message;
    if (connected_) {
        // The battery stopped answering while it was being read: on this BMS
        // that means another device took it over (a phone, the desktop app).
        takeover_ = true;
    }
    disconnect();
    nextActionAt_ = millis() + BLE_RETRY_MS;
    state_ = State::Idle;
    logLine("bms", message);
    if (onState) {
        onState();
    }
}

void Bms::disconnect() {
    connected_ = false;
    awaiting_ = 0;
    queueCount_ = 0;
    queueIndex_ = 0;
    analogLength_ = 0;
    statusLength_ = 0;
    cellsLength_ = 0;
    rxReady_ = false;
    rxLength_ = 0;
    writeCharacteristic = nullptr;
    if (client != nullptr && client->isConnected()) {
        client->disconnect();
    }
}

