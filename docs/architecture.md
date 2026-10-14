# Architecture

The application is a single Go binary that embeds the whole web interface and
talks to Bluetooth LE directly, without any external service.

```
┌───────────────────────── lifepo4-bms-monitor.exe ─────────────────────────┐
│                                                                           │
│  WebView2 window (ui/index.html, ui/style.css, ui/app.js)                 │
│      │  HTTP + JSON over 127.0.0.1 (embedded server)                      │
│      ▼                                                                    │
│  server.go      /api/state  /api/select  /api/deepscan  /api/window          │
│                 /api/batteries (public read API)  /api/settings              │
│      │                                                                    │
│      ├── hub.go      scanning, device registry, identification queue      │
│      ├── session.go  polling session for the selected battery             │
│      ├── identify.go vendor neutral protocol detection                    │
│      ├── protocol*.go framing, checksums and decoding                     │
│      ├── settings.go settings.json (interval, read API, MQTT)             │
│      ├── mqtt.go     MQTT publishing + Home Assistant discovery           │
│      └── store.go    devices.json + history/*.csv in the user profile     │
│                                                                           │
│  window.go  Win32: frameless window, drag, icon, DWM rounded corners      │
└───────────────────────────────────────────────────────────────────────────┘
```

## Threading model

Bluetooth on Windows uses WinRT, which needs the calling thread to be in the
multi threaded apartment (MTA). WebView2 however initialises COM as a single
threaded apartment (STA) on the main thread — the two cannot share a thread.
The application therefore keeps all BLE work on dedicated OS threads pinned with
`runtime.LockOSThread()`:

| Goroutine | Thread | Work |
|---|---|---|
| `scanWorker` | pinned, MTA | enables the adapter and receives scan results |
| `probeWorker` (×2) | pinned per probe | connects, discovers services, identifies the protocol |
| `session.run` | pinned, MTA | one connection per selected battery, polls every 5 s |

Because WinRT calls can block indefinitely, every identification runs inside
`probeSafely` with a hard deadline (`probeDeadline`). A probe that does not
finish is abandoned, its device is marked *unavailable* and the queue keeps
moving, so the interface never gets stuck in "checking".

## Discovery pipeline

1. The scanner receives advertisements. Every device is added to the registry
   with address, name, RSSI and a hit counter.
2. `classifyDevice` assigns a probe priority:
   * **high** — remembered battery, a known brand name (`HS…`, `ECO…`, `DCH…`,
     `JBD…`, …), or the advertisement carries a battery service (FF00/FFF0);
   * **medium** — the generic `0x0001` service used by HS03 devices;
   * **low** — any other named device, or a silent device that keeps appearing;
   * **skip** — unnamed devices and devices already rejected in an earlier run.
3. Devices are probed in priority order. While no battery has answered yet,
   generic devices wait a few seconds at the head of the queue so a battery that
   shows up in the meantime is served first.
4. A probe connects once, discovers the services of all known families and — for
   JBD — performs a real request/response round trip with checksum validation.
   Only then is a device reported as a battery.
5. Results are persisted: batteries in `devices.json`, rejections in the same
   file as `notBMS`. Later starts therefore identify in seconds instead of
   re-probing everything.

## Monitoring session

Selecting a card starts a session goroutine:

1. connect (10 s timeout),
2. protocol specific handshake (HS04: `HiLink` authentication; HS03: init frame;
   JBD: nothing),
3. read the model, then loop: read the analog/basic data and cell voltages,
   decode into a `Sample`, publish it to the interface and append it to the
   history CSV,
4. wait `-interval` seconds (default 5) and repeat.

Every error is reported with a code (`noDevice`, `connect`, `notify`, `auth`,
`protocol`, `unsupportedDevice`, `ble`) that the interface translates, plus the
raw technical message from the BLE stack.

## User interface

The interface is plain HTML/CSS/JS embedded into the binary with `go:embed` and
served from a local HTTP server on a free port. It polls `/api/state` once per
second and updates the DOM in place (stable hover state, animated bars).

`/api/state` returns:

| Field | Content |
|---|---|
| `hub.batteries` | identified batteries (and remembered ones that are offline) |
| `hub.checking` | devices currently queued or being probed |
| `hub.devices` | every device of the current scan, sorted alphabetically by name (then address) — a stable order, unlike signal strength |
| `hub.pending`, `hub.rejected`, `hub.identified` | progress counters |
| `session` | state and last reading of the monitored battery, plus `session.power`: the readings of the last ten minutes (unix milliseconds and watts) used by the charging power graph |
| `config` | version, default language, refresh interval, data directory, platform |

* The window is frameless: `window.go` removes the caption, keeps the resize
  frame, asks DWM for rounded corners and sets the embedded icon.
* Dragging is implemented by polling the cursor and moving the window with
  `SetWindowPos`, because the WebView2 child window owns the mouse and the
  classic `WM_NCLBUTTONDOWN` trick does not work on a caption-less window.
* All labels come from a small dictionary in `ui/app.js` (English and Polish);
  the choice is stored in `localStorage` and can be overridden with `-lang`.

## Persistence

`store.go` owns everything below `%APPDATA%\LiFePO4BMSMonitor`:

| File | Content |
|---|---|
| `settings.json` | refresh interval, read API switch and MQTT configuration |
| `devices.json` | version, timestamp and one entry per device: address, name, model, brand, protocol, status (`bms`/`notBMS`), RSSI, last seen time and the newest reading |
| `history\<address>.csv` | one row per sample with the columns documented in the README |

The history file is also read back: `Store.RecentPower` parses the tail of the
file and `Session` seeds the ten minute power graph with it, so the graph shows a
full window right after a start. In memory the readings are kept in a rolling
per-address window (`history.go`), so the interface never scans the file while
it polls.

Writes are atomic (temporary file + rename) and the store degrades gracefully:
if the directory is not writable the application keeps running with persistence
disabled. The newest reading is kept in memory on every sample and flushed to
disk at most every 30 seconds, so the read API and MQTT can always report the
last known state — also while a battery is asleep.

## MQTT and Home Assistant

`mqtt.go` owns a single background goroutine that

1. watches the settings every two seconds and connects or disconnects as needed,
2. publishes `online`/`offline` on `<prefix>/status` (retained, with a last-will
   message so a crashed instance does not look alive),
3. publishes one retained Home Assistant discovery document per entity whenever
   the set of known batteries changes, and re-publishes the last known state, and
4. forwards every new sample from `session.go` to `<prefix>/<mac>/state`.

The payload builders (`buildStatePayload`, `buildDiscoveryPayloads`) are pure
functions covered by unit tests, so the wire format can be verified without a
broker.
