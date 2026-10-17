# Firmware versions — ESP32-C3

Every change to the device firmware bumps the version and the build number.
Both are printed in the interface (bottom right: `v… · b…`) and in the settings
panel (*Device*), and both are in `/api/state` (`config.version`,
`config.build`) and in the Home Assistant discovery document (`sw_version`).

| Build | Version | Changes |
|---|---|---|
| 1 | esp32-0.1.0 | First working build: UI and API on port 80 from LittleFS, BLE reading, OLED, setup access point `BATTERY-<n>`, Wi-Fi settings, OTA firmware, MQTT with Home Assistant discovery, battery list kept in flash, memory guard for the history ring. |
| 2 | esp32-0.2.0 | Precise MQTT: every voltage in three decimals, `voltage_mv`, `current_ma`, one sensor per cell (`cell_1`…`cell_N`) with `suggested_display_precision: 3`, problem names (`problem_text`, `problem_count`, `problem_active`) next to the raw code, per-battery availability so a sleeping battery shows as unavailable, discovery republished when the cell count is learned. Statistics in a fixed-size flash file (`/stats.bin`, 2048 readings, 116 KB, oldest overwritten, one write a minute) that survives a restart. Bluetooth stack restarted after three failed connections (the library leaks a registration slot per attempt). Interface files can be updated over the air too (`POST /api/ota?fs=1`). Scan listens 75 % of the time so a battery two metres away is not missed while Wi-Fi runs. |
| 3 | esp32-0.2.0 | Every voltage and current value is `x.xxx` in Home Assistant: `suggested_display_precision: 3` on all of them (Home Assistant's own default for volts is zero decimals, which is why a cell read as "3 V"), plus `voltage_mv` and `current_ma` for the raw millivolt reading. Text sensors (balance state, problem text, problem hex) no longer claim `state_class`. State is published retained, so Home Assistant has the last reading straight after a restart. Statistics file rebuilt through a temporary name, so a fresh filesystem creates it correctly. Build number shown next to the version in the interface (`v0.2.0 · b3`). |
| 4 | esp32-0.2.0 | A problem is news: `lifepo4bms/<address>/problems` carries one message the moment the code changes, with `code`, `hex`, `text` (the names, e.g. "AFE error, software lock"), `count`, `active` and `time` — ready for a Home Assistant notification automation. The check runs on every reading, so a fault appears and clears without polling. |
