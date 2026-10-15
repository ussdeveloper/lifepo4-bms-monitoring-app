# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

* Automatic monitoring: the application connects to the best available battery
  on its own as soon as one shows up in the list, retries failed connections
  after 2 s (then every 12 s) and reports the attempt number in the interface.
* Settings panel (gear icon in the title bar) with a refresh interval slider
  (2–30 s) plus a free value, and settings stored in `settings.json`.
* Public read API (`/api/batteries`, toggle in the settings) that returns the
  last reading of every battery **with the time of that reading, also while the
  battery is asleep** — the last sample is persisted in `devices.json`.
* MQTT publishing with optional Home Assistant discovery: every battery becomes
  one Home Assistant device with sensors (state of charge, voltage, current,
  capacities, cycles, cells, temperatures, problem codes) and binary sensors
  (charging, discharging, balancing), plus availability reporting.
* Charging / discharging / idle indicator in the battery list.
* Charging power in the detail panel: a small graph of the last ten minutes with
  the current value below it (positive while charging, negative while
  discharging). The window is filled from the history file after a restart.
* Estimated time until the battery is full (or empty) next to the power value:
  coulomb counting at the current is combined with the trend of the state of
  charge, so a tapering charge is reflected too. Also published to MQTT as
  `eta_min` and as a Home Assistant sensor.
* The list is a scrolling frame: the rounded panel, its elevation and the
  section head stay in place while only the rows move, and rows fade out at the
  scroll edges instead of being cut in half.
* Taking the battery over: when another application (a phone application, a
  second computer) connects, the BMS drops this connection. The application then
  reports *another application connected — taking it back*, keeps showing the
  last reading and keeps retrying until it gets the battery back, instead of
  holding it hostage or giving up.
* The right hand panel explains an empty device list (radar, "Looking for
  batteries…", scan statistics: advertisements, devices, names, scan restarts)
  while the whole left panel is left to the live device list, so more rows fit.
* The device list contains only devices the current scan actually sees — the
  batteries restored from disk are listed above it as batteries, not as devices.
* The scanner restarts itself after 60 s without a single advertisement and logs
  every newly seen device, so a computer that finds nothing can be diagnosed
  from its log file.
* Releasing the battery on purpose: the *unplug* button in the title bar (and
  `POST /api/release`) drops the connection so the BMS starts advertising again
  and a phone or a second computer can find it; the status bar counts the time
  left and monitoring resumes on its own afterwards. The release time is a
  setting (five minutes by default) and can also be passed to the API.
* Cell balance in the detail panel: a window drawn behind the cell bars (tight,
  ±30 mV, above 3.40 V per cell where the curve is steep and the BMS balances;
  wide, ±60 mV, in the flat 3.10–3.40 V part where a spread is a *phantom*
  imbalance). Cells outside the window are highlighted, the weakest cell is
  named, and the same values go to MQTT as `balance_mv` / `balance_state` with a
  Home Assistant sensor.
* The balance window follows the battery: large cells need longer to move with a
  fixed balancer current (+8 mV from 280 Ah, +4 mV from 150 Ah) and a pack that
  is still forming settles over the first cycles (+15 mV up to 5 cycles, +8 mV up
  to 20), so a brand new 314 Ah pack is judged against ±53 mV at the top of the
  charge instead of ±30. The interface prints that arithmetic.
* Cell history database: every reading, with all cell voltages, is stored in
  `history.db` (SQLite) in the application data directory, written in batches and
  keyed on (battery, timestamp) so a reading cannot be stored twice.
* A *Cells over time* chart in the detail panel draws the cell voltages from that
  database with 1 h / 6 h / 24 h / 7 d ranges, filled from the whole history.
* Backups are the database file: *Download backup* sends a consistent copy
  (`GET /api/db/export`) and *Import backup* merges one back in
  (`POST /api/db/import`), adding only the readings that are not there yet, cell
  voltages included. `GET /api/cells/history` serves the chart data.
* The "Cell history database" setting switches the storing on and off (on by
  default; a settings file from an older version is migrated).
* Console window setting: the windowed application starts without a console
  (the build uses the Windows GUI subsystem) and the setting opens one with the
  log when it is wanted, live, without restarting. The log file is written
  either way.
* Two tabs on the left: **Batteries** lists everything that answers (including
  addresses added by hand), **Bluetooth devices** lists everything the scan sees,
  each with a count. Before the user picks a tab an empty battery list falls back
  to the device list.
* `-listen lan` (or a single address) serves the interface and the read API to
  the local network, so a second computer can watch the battery over HTTP while
  this one keeps the Bluetooth link. Other computers are read-only: they get
  `/api/state` (without the broker password) and `/api/batteries`, and every
  call that changes something answers `403`.
* **Add by MAC**: a device can be listed by hand (button next to *Check every
  device*), which is how a battery reachable only through another Bluetooth radio
  is monitored. Hand added addresses are remembered, probed with the highest
  priority and removable in the same dialog.
* Every Bluetooth radio of the computer is reported (settings panel) and asked
  which batteries it knows, so a computer with two radios can still reach a
  battery the default radio does not hear.
* A device name read over GATT is stored even when the device turns out not to
  be a battery, so the list shows names instead of bare addresses.
* Screenshots in the README and documentation.
* Unit tests for the settings store and for the MQTT/Home Assistant payloads.

### Changed

* The battery grid is now a plain list: one row per battery, sorted
  alphabetically by name (then address) instead of by signal strength, which
  made the rows jump while scanning.
* Devices without an advertised name are labelled the way Windows labels them
  (`Bluetooth AA:BB:CC:DD:EE:FF`) instead of showing the bare address.
* Only the list scrolls; it reaches down to the status bar and the inner device
  list no longer adds a second scrollbar.
* The read countdown is only a progress bar: the text beside it changed on every
  poll and made the panel jump.
* The window client area covers the whole window (`WM_NCCALCSIZE`), so the
  interface sits flush against the window edges instead of being inset by the
  invisible resize border Windows keeps around a frameless window. Resizing is
  done by the application itself (the web view covers the frame), and the scan
  chip dot lines up with its label.
* The state of charge ring shows the percentage and its caption inside the
  circle instead of overflowing it.
* Title bar controls share one height and use Lucide icons with a 0.75 stroke in
  a softer colour, so icons and labels line up.
* Battery rows use a light elevation (small ambient shadow plus a bright inner
  edge) instead of a heavy shadow below the row.
* Battery serial numbers, Bluetooth addresses and the Windows user name are
  masked in the screenshots, and the JSON examples use placeholder values.

### Added

* The battery panel now lists every Bluetooth device in range with its state
  (`queued`, `reading`, `no BMS`, `no answer`) and signal strength whenever no
  battery was found, so it is clear that the scanner is working.
* Log file next to the executable now records probes, timeouts and panics
  (previously nothing was written because writing to stderr of a GUI process
  failed first and swallowed the messages).

### Fixed

* Devices that did not answer were queued again on every advertisement, which
  kept the interface busy on "checking" forever.
* Identification of a single device could block the whole queue indefinitely;
  probes now have a hard deadline and the queue keeps moving.
* Devices rejected in an earlier run are skipped instead of being probed again,
  which makes the second start much faster.
* Chips and panels marked as hidden were still laid out (`[hidden]` was
  overridden by `display: inline-flex`).

## [0.1.0] - 2026-10-14

Initial public release: a read-only Bluetooth LE monitor for LiFePO4 battery
management systems, with a Windows desktop application and a Python CLI.

### Added

* Desktop application (Go + WebView2) with a frameless window: custom title bar
  with drag, minimize/maximize/close, language switch and a status bar.
* Battery grid that lists every detected BMS; hovering a card reads its data,
  clicking pins the selection. Readings refresh every 5 seconds.
* Detail panel with state of charge ring, voltage, current, state of health,
  remaining and design capacity, cycle count, per-cell voltages with spread,
  temperatures and MOSFET/balancer/protection flags.
* Battery identification for three protocol families, validated with checksums:
  * HS03 / HiLink (`0x0001`, `0x0002`, `0x0003`)
  * HS04 / WATT (`0xFFF0`, `0xFFF1`, `0xFFF2`, `0xFFFA` with HiLink authentication)
  * JBD / Xiaoxiang (`0xFF00`, `0xFF01`, `0xFF02`)
* Vendor neutral scanning: devices are probed in priority order (remembered
  batteries and known brands first, generic devices later) and a device is only
  reported as a battery when it answers with a valid frame.
* Persistent per-user data in `%APPDATA%\LiFePO4BMSMonitor`:
  * `devices.json` — identified batteries and the cache of devices that are not
    a BMS, which makes later starts much faster;
  * `history\<address>.csv` — one row per reading while a battery is monitored.
* Bilingual interface (English default, Polish) with a title bar switch; the
  choice is remembered.
* Application icon drawn to match the interface logo and embedded in the
  executable, plus the window icon used by the task bar.
* Embedded web interface with glassmorphism styling, contained scrolling inside
  the grid and detail panel, and no page level scrolling.
* Command line tool (`cli/humsienk_cli.py`) with a keyboard driven TUI, live
  scan progress, reports, CSV monitoring and the same protocol support.
* Unit tests: protocol framing/checksums (including real captured frames),
  store and history handling, drag behaviour and device classification.
* Build scripts for Windows (`scripts/build-windows.ps1`) and for Linux/WSL
  cross-compilation (`scripts/build-linux.sh`).
* Documentation: README, architecture, protocol reference and build guide.

### Known limitations

* Only Windows is supported for running the application (WebView2 and WinRT BLE).
* JBD units with the newer password protected firmware (`FF AA` unlock) are
  detected but cannot be read yet.
* Bluetooth address privacy: devices with random addresses are re-probed after
  a restart, because the address changes.

[Unreleased]: https://github.com/ussdeveloper/lifepo4-bms-monitoring-app/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ussdeveloper/lifepo4-bms-monitoring-app/releases/tag/v0.1.0
