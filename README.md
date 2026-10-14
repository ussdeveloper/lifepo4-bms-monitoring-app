# LiFePO4 BMS Monitor

Read-only Bluetooth LE monitor for LiFePO4 battery management systems (BMS).
It scans for nearby batteries, lists them in a desktop application and shows the
live state of the battery you point at — state of charge, voltage, current,
cells, temperatures and MOSFET/balancer state — refreshing every few seconds.

The application never writes settings to a battery: every frame it sends is a
read request.

![Battery dashboard](docs/screenshots/dashboard.png)

* Desktop application: Go + WebView2, frameless window with its own title bar, bilingual UI (English / Polish)
* Settings panel: refresh interval, read API, MQTT with Home Assistant discovery
* Command line tool: Python TUI for quick checks and reports (`cli/`)
* Battery data and history are stored per user in `%APPDATA%\LiFePO4BMSMonitor`

![Settings](docs/screenshots/settings.png)

## Supported batteries

Detection is vendor neutral: a device is identified by the protocol it speaks,
not by its advertised name. The name is only a hint that decides the order in
which devices are checked.

| Family | Service / characteristics | Devices seen in the wild |
|---|---|---|
| HS03 / HiLink | `0x0001`, write `0x0002`, notify `0x0003` | Humsienk, ECO-WORTHY, DCH and similar 4S/8S packs |
| HS04 / WATT | `0xFFF0`, notify `0xFFF1`, write `0xFFF2`, auth `0xFFFA` | Humsienk "WATT" generation packs |
| JBD / Xiaoxiang | `0xFF00`, notify `0xFF01`, write `0xFF02` | Overkill Solar, JBD "SP04S" boards, many rebrands |

See [docs/protocols.md](docs/protocols.md) for the frame formats, checksums and
how to add another BMS family.

## Repository layout

```
main.go, hub.go, session.go   desktop application (Go, package main)
protocol.go                   HS03/HiLink and HS04/WATT framing and decoding
protocol_jbd.go               JBD/Xiaoxiang framing and decoding
identify.go                   vendor neutral protocol detection
store.go                      known devices and CSV history in the user profile
window.go                     Win32 frameless window, custom drag, icon
server.go, ui/                embedded web interface (HTML/CSS/JS)
assets/                       application icon and the script that draws it
cli/                          Python command line tool with its tests
docs/                         architecture, protocol and build documentation
scripts/                      build scripts for Windows and Linux/WSL
```

## Build

Requirements: Go 1.24 or newer. The WebView2 runtime ships with Windows 11 and
current Windows 10 installations.

Windows:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
# also build the Python CLI executable
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1 -Cli
```

Linux / WSL (cross-compiles the same Windows executable):

```bash
./scripts/build-linux.sh
```

Both scripts run the unit tests and write `dist/lifepo4-bms-monitor.exe`.
Details and troubleshooting: [docs/build.md](docs/build.md).

## Usage

Start the desktop application by double clicking `lifepo4-bms-monitor.exe` or
from a terminal:

```powershell
.\dist\lifepo4-bms-monitor.exe
```

| Option | Meaning |
|---|---|
| `-port 8811` | fixed port for the local interface (default: free port) |
| `-interval 5` | default refresh interval, used until settings.json exists |
| `-lang pl` | start the interface in Polish (English is the default) |
| `-data D:\BMS` | different data directory (default: `%APPDATA%\LiFePO4BMSMonitor`) |
| `-no-window` | run only the interface server, useful for debugging |
| `-version` | print the version and exit |

How the interface behaves:

* The application **monitors the best available battery automatically**: as soon
  as a battery is on the list (or a remembered one wakes up) the session connects
  to it — no interaction needed. Failed connections are retried after 2 s (first
  six attempts) and then every 12 s, so a sleeping BMS is picked up within a few
  seconds of waking up.
* Batteries are listed as rows **sorted alphabetically** (by name, then by
  address) — the order never depends on signal strength, so rows do not jump
  while scanning. Only the list itself scrolls, from below the heading down to
  the status bar.
* Hovering a battery row switches the monitoring to it; clicking pins it. The
  automatic choice resumes when the pinned battery is out of range.
* A small charging indicator next to the battery name shows whether the battery
  is charging, discharging or idle.
* When no battery is in range yet, the panel lists **every** Bluetooth device
  the scanner sees together with its state (`queued`, `reading`, `no BMS`,
  `no answer`) and signal strength — so it is obvious that the scan works and
  what is around. Devices without an advertised name are shown the way Windows
  shows them, for example `Bluetooth 80:2b:f9:d0:d8:e2`.
* The window has its own title bar (drag anywhere on it), a language switch and
  a status bar showing scan state, device counts, history size and version.
* **Check every device** re-probes all visible devices, including the ones that
  were rejected earlier.
* Batteries that were identified once are listed again on the next start, even
  while they are asleep — they are shown as *not in range* until they answer.

If the battery shows up as *not in range*: press the wake-up button on the
battery (or connect a charger) — a sleeping BMS stops advertising and cannot be
discovered by any tool. To check what the adapter sees right now:

```powershell
.\.venv\Scripts\python.exe .\cli\humsienk_cli.py --list --all --scan-seconds 25
```

## Settings

The gear icon in the title bar opens the settings panel. Everything is stored in
`%APPDATA%\LiFePO4BMSMonitor\settings.json` and applies immediately.

| Setting | Meaning |
|---|---|
| Refresh interval | slider from 2 s to 30 s, plus a free value (1 s … 1 h) in the number field |
| Read API | switch for the public read endpoint (see below) |
| MQTT and Home Assistant | broker host, port, user, password, topic prefix |
| Home Assistant discovery | announce every battery as a Home Assistant device |

### Read API

`GET http://127.0.0.1:<port>/api/batteries` returns every known battery with its
**last reading and the time of that reading — also while the battery is asleep**,
so automations keep working with the last known state:

```json
{
  "generatedAt": "2026-10-14T12:59:46-05:00",
  "count": 1,
  "batteries": [
    {
      "addr": "4E:DF:C5:14:DD:67",
      "name": "HS030902AB26170219",
      "model": "BMC-04S001",
      "brand": "HiLink",
      "protocol": "HS03",
      "online": false,
      "lastSeen": "2026-10-14T12:18:50-05:00",
      "lastSampleAt": "2026-10-14T12:18:51-05:00",
      "sample": { "voltageV": 13.45, "currentA": 10.04, "soc": 77, "cellsMv": [3362, 3362, 3363, 3359] }
    }
  ]
}
```

Other endpoints: `/api/state` (full application state) and `/api/settings`
(GET or POST a partial settings patch). Add `?addr=<address>` to `/api/batteries`
to query a single battery.

### MQTT and Home Assistant

With MQTT enabled the application connects to the broker, publishes its
availability on `<prefix>/status` (retained, with a last-will message) and sends
every reading to `<prefix>/<mac>/state` as JSON:

```json
{
  "address": "4E:DF:C5:14:DD:67", "name": "HS030902AB26170219",
  "voltage": 13.45, "current": 10.04, "soc": 77, "remaining_ah": 242.57,
  "cell_min_v": 3.362, "cell_max_v": 3.363, "cell_delta_mv": 4,
  "temp_min_c": 20, "temp_max_c": 26, "charging": true, "discharging": true
}
```

With discovery enabled, every battery is announced once under
`<discovery-prefix>/<component>/<prefix>_<mac>/<entity>/config`, grouped into one
Home Assistant device per battery (manufacturer, model, MAC address) with these
entities:

| Type | Entities |
|---|---|
| `sensor` | state of charge, voltage, current, remaining and design capacity, cycles, cell voltage minimum/maximum/spread, temperature minimum/maximum, problem codes |
| `binary_sensor` | charging, discharging, balancing |

The last known state is published right after connecting, so Home Assistant has
values even when a battery is asleep at that moment.

## Stored data

Everything is kept in the user profile, next to each other:

```
%APPDATA%\LiFePO4BMSMonitor\
├── settings.json                refresh interval, read API and MQTT configuration
├── devices.json                 batteries (with their last reading) + rejected devices
└── history\
    └── 4E-DF-C5-14-DD-67.csv    one row per reading (every 5 s while monitoring)
```

`devices.json` makes the second start instant: known batteries are listed
immediately and devices that were already rejected are not probed again.

The history CSV has one row per sample:

```
timestamp, device, model, brand, protocol, voltage_v, current_a, soc_pct,
soh_pct, remaining_ah, design_ah, cycles, cell_min_mv, cell_max_mv,
cell_delta_mv, cells, temp_min_c, temp_max_c, charging_mosfet,
discharging_mosfet, balancer, problem_code
```

## Command line tool

The Python CLI in [cli/](cli) speaks the same protocols and is handy for
scripting, reports and quick checks without a GUI:

```bash
python -m pip install -r cli/requirements.txt
python cli/humsienk_cli.py --help          # English by default
python cli/humsienk_cli.py --lang pl       # Polish output
python cli/humsienk_cli.py --list          # scan and list batteries
python cli/humsienk_cli.py --report        # write a report next to the program
python cli/humsienk_cli.py --watch         # monitor and log to CSV
```

Tests: `python -m pytest cli/tests -q`.

## Documentation

* [docs/architecture.md](docs/architecture.md) — components, threading model, data flow
* [docs/protocols.md](docs/protocols.md) — BLE protocols, frames and validation
* [docs/build.md](docs/build.md) — build prerequisites and release builds
* [CHANGELOG.md](CHANGELOG.md) — what changed in each version

## Safety and limitations

* Read-only: the application never changes BMS settings, thresholds or MOSFETs.
* Some BMS systems need an unlock handshake on newer firmware (JBD `FF AA`).
  Such units are detected by their service but data reading can fail; the UI
  reports the error instead of hiding it.
* Bluetooth range matters: RSSI below about −90 dBm usually means unreliable
  reads. Keep the computer close to the battery while monitoring.

## License

MIT — see [LICENSE](LICENSE).
