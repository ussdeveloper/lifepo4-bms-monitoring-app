# LiFePO4 BMS Monitor on an ESP32-C3 with a 0.42" OLED

A second home for this project: the same application, running on a board the
size of a coin, next to the battery.

* reads the battery over **Bluetooth LE** (the HiLink / HS03 protocol, the same
  framing and the same command sequence as `protocol.go` of the desktop
  application);
* serves **the very same interface** (`../../ui`, copied into the filesystem
  image at build time, so the two can never drift apart) on **port 80**;
* speaks the **same HTTP API** (`/api/state`, `/api/batteries`, `/api/select`,
  `/api/add`, `/api/release`, `/api/cells/history`, `/api/settings`) and the
  same **MQTT topics with Home Assistant discovery**, so a battery looks the
  same whether a computer or this device reads it;
* shows what it is doing on the **0.42" OLED**: the access point to join, the
  Wi-Fi state, "no battery", and then the state of charge with a battery symbol,
  voltage, current and a warning triangle when the BMS reports a problem or the
  cells drift apart;
* **updates itself over the air** (PlatformIO/Arduino on port 3232 and a plain
  HTTP upload on `/api/ota`), and only then switches to the new firmware;
* remembers every battery it has ever found in flash, and keeps looking for
  them even while they sleep or another device holds them;
* starts its own access point (`BATTERY-<number>`) when it has no network yet,
  so it can be set up from a phone without a cable.

## Hardware

| | |
|---|---|
| Board | ESP32-C3 (4 MB flash), the common "0.42 inch OLED" board |
| Display | SSD1306, 72x40, I2C |
| Port | the board's own USB (USB Serial/JTAG) |

There is no standard for the display pins, so the firmware **probes the usual
I2C pin pairs at boot** (SDA/SCL: 5/6, 8/9, 4/5, 6/7, 3/4, …) and keeps the
first one that answers; the pins are then stored and reused. On the board used
here it found SDA 5 / SCL 6.

## Build and flash

```powershell
cd esp32\ESP32C3-OLED042

# firmware and interface over USB (COM5 is this board)
pio run -e esp32c3 -t upload --upload-port COM5      # firmware
pio run -e esp32c3 -t uploadfs --upload-port COM5    # interface (ui/)

# over the air, once the device is on the network
pio run -e ota -t upload --upload-port 192.168.1.50
# or the same thing with nothing installed but curl:
curl -F "firmware=@.pio/build/esp32c3/firmware.bin" http://192.168.1.50/api/ota

pio device monitor                                    # serial log
```

The partition table keeps two application slots, so an update writes to the
spare one and the device switches over after a restart: a bad upload cannot
brick it.

> **A USB flash writes only the first slot.** When the device was last updated
> over the air it boots from the *second* slot, so `pio run -t upload` appears
> to change nothing: the new firmware is on flash but never runs. Either update
> over the air (the normal path), or write the image to the running slot as well:
>
> ```powershell
> python $env:USERPROFILE\.platformio\packages\tool-esptoolpy\esptool.py `
>   --port COM5 --chip esp32c3 write_flash --flash_size keep 0x1d0000 `
>   .pio/build/esp32c3/firmware.bin     # the second slot, 0x10000 is the first
> ```

## Setting it up

1. The device starts the access point **`BATTERY-<number>`** (open, it exists
   only until it is configured) and shows it on the OLED.
2. Join it from a phone or a computer and open <http://192.168.4.1/>.
3. Settings → **Wi-Fi network** → *Scan* → pick the network → *Join*. The device
   stores it and restarts.
4. From then on it is a normal network device: open its address in a browser,
   or `http://lifepo4-bms.local/`. The address is in the log and in the settings
   panel (*Device*).
5. Nothing there? The device falls back to its access point after three failed
   attempts and keeps retrying the configured network in the background, so a
   router that comes back is picked up on its own.

The same settings panel carries the interface language, the refresh interval,
the read API switch, MQTT (broker, topic prefix, Home Assistant discovery) and
**Update firmware** / **Restart** for the device itself.

## What is different from the desktop application

| | Desktop | ESP32-C3 |
|---|---|---|
| Interface | served from the binary, port 80/8822 | served from the filesystem image, port 80 |
| History | SQLite on disk (`history.db`), chart of hours…months | the last ~360 readings in RAM for the live chart, **plus a fixed-size file in flash** (see below), oldest always dropped |
| Backup | download/import the database | `GET /api/cells/history` (the flash file), `POST /api/stats/clear` to empty it |
| Battery list | `devices.json` | up to 8 batteries in flash (NVS) |
| Console window | optional console with the log | the serial log, and `/api/log` |
| Radios | every Bluetooth radio of the computer | the one radio the chip has |

Wi-Fi and Bluetooth share that one radio: the scan listens for 75 ms of every
100 ms, in rounds of four seconds with a pause in between, so the web interface
stays responsive while the device looks for the battery.

## What is kept in flash

Everything the device knows survives a restart, and none of it can grow without
a limit:

| What | Where | Size |
|---|---|---|
| Wi-Fi network, interval, release time, language, read API, MQTT, OTA password, display pins, access point number | NVS (one key each) | a few hundred bytes |
| Batteries found so far (address and name) | NVS, JSON under `batteries` | up to 8 entries |
| Readings for the chart | `/stats.bin` in the filesystem | a ring of 2048 records (58 B each), **116 KB fixed** |

The statistics file never grows: a new reading overwrites the oldest one, and its
size is chosen when it is created from what is free in the filesystem, leaving
96 KB spare for the interface files and an update. Readings are written in
batches once a minute, not on every sample, because each write costs a block of
flash and the device is meant to run for years. `/api/state` reports
`historyStored`, `historyStoredCapacity`, `historyBytes` and `flashFree`, and the
settings panel shows the same numbers.

A battery that is seen but not answered is asked directly every twenty seconds;
the Bluetooth stack is restarted after three failed connection attempts, because
the Arduino Bluetooth client uses up a registration slot on every attempt and
would otherwise stop connecting altogether.

## MQTT in Home Assistant

Every voltage and current is published and displayed as `x.xxx`, the resolution
the BMS actually measures (it reports millivolts):

| Entity | Value |
|---|---|
| `voltage` / `current` | `3.451` V / `-2.531` A, three decimals |
| `voltage_mv` / `current_ma` | the raw integer millivolts / milliamps |
| `cell_1` … `cell_N` | one sensor per cell, three decimals, `suggested_display_precision: 3` |
| `cell_min` / `cell_max` / `cell_delta` | three decimals / millivolts |
| `problems` / `problem_hex` | the code as a number and as `0x00001800` |
| `problem_text` | the names, e.g. `AFE error, software lock` |
| `problem_count` / `problem_active` | how many, and whether any is set |

Home Assistant's own default for volts is **zero** decimals, so each numeric
entity carries `suggested_display_precision` — without it a cell shows as "3 V".

A problem is also **news**: `lifepo4bms/<address>/problems` gets one message each
time the code changes, so an automation can notify instead of polling:

```json
{"address":"4E:DF:C5:14:DD:67","name":"BMC-04S001","code":6144,
 "hex":"0x00001800","text":"AFE error, software lock","count":2,
 "active":true,"time":1760736000}
```

Availability is published twice: `lifepo4bms/status` (the device or the desktop
application) and `lifepo4bms/<address>/status` (the battery itself), so a
sleeping battery shows as unavailable instead of holding on to an old reading.

## Layout

| File | What it does |
|---|---|
| `src/main.cpp` | boot order, the loop, memory guard, heartbeat |
| `src/net.cpp` | station and access point, captive portal, mDNS |
| `src/bms.cpp` | BLE central: scan, connect, the HS03 command sequence |
| `src/battery.cpp` | decoding, balance window, history ring, time estimate |
| `src/stats.cpp` | the readings kept in flash (a fixed-size ring file) |
| `src/webui.cpp` | the HTTP API, the same JSON as the desktop |
| `src/mqtt.cpp` | MQTT publishing and Home Assistant discovery |
| `src/display.cpp` | the OLED screens |
| `src/ota.cpp` | over the air updates |
| `src/appconfig.cpp` | settings and the battery list in NVS |
| `sync_ui.py` | copies `../../ui` into the filesystem image before every build |

`src/battery.cpp` is a direct port of `protocol.go` (decoding), `balance.go`
(the window every cell should stay inside) and `history.go` (the time estimate)
of the desktop application, so both report the same numbers for the same
battery.
