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
