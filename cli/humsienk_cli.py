from __future__ import annotations

import argparse
import asyncio
import csv
import math
import re
import shutil
import sys
import threading
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any


SERVICE_UUID = "0000fff0-0000-1000-8000-00805f9b34fb"
NOTIFY_UUID = "0000fff1-0000-1000-8000-00805f9b34fb"
WRITE_UUID = "0000fff2-0000-1000-8000-00805f9b34fb"
AUTH_UUID = "0000fffa-0000-1000-8000-00805f9b34fb"
GENERIC_ACCESS_UUID = "00001800-0000-1000-8000-00805f9b34fb"
DEVICE_NAME_UUID = "00002a00-0000-1000-8000-00805f9b34fb"

# HS04/WATT v4 — newer variant (FFF0 service, 0x7E frames, Modbus CRC)
PRODUCT_POINT = 0x92
COLLECTION_BOARDS_POINT = 0x1E
ANALOG_POINT = 0x8C
READ_POINTS = {PRODUCT_POINT, COLLECTION_BOARDS_POINT, ANALOG_POINT}
MAX_FRAME_SIZE = 512

# HS03/HiLink — older variant (0001 service, 0xAA frames, checksum)
HS03_SERVICE_UUID = "00000001-0000-1000-8000-00805f9b34fb"
HS03_WRITE_UUID = "00000002-0000-1000-8000-00805f9b34fb"
HS03_NOTIFY_UUID = "00000003-0000-1000-8000-00805f9b34fb"
HS03_HEAD = 0xAA
HS03_MIN_FRAME = 5
HS03_INIT_COMMAND = 0x00
HS03_MODEL_COMMAND = 0x11
HS03_STATUS_COMMAND = 0x20
HS03_ANALOG_COMMAND = 0x21
HS03_CELLS_COMMAND = 0x22
HS03_MAX_CELLS = 24
HS03_TEMP_COUNT = 6

DEFAULT_MONITOR_SECONDS = 5.0

DEFAULT_LANG = "en"

# Message catalogue: every user-visible string is looked up through tr().
MESSAGES: dict[str, dict[str, str]] = {
    "en": {
        "cli.description": "Read-only Humsienk BMS data over Bluetooth LE.",
        "cli.help": "show this help message and exit",
        "cli.scan_seconds": "Bluetooth scan time in seconds (default: 15)",
        "cli.address": "device address that skips the interactive selection",
        "cli.all": "show all BLE devices, not only Humsienk candidates",
        "cli.no_resolve": "skip GATT name and protocol resolution (faster TUI start)",
        "cli.watch": "refresh the data at the given interval (monitor mode)",
        "cli.watch_metavar": "SECONDS",
        "cli.report": (
            "save a .txt report (read) or a .csv log (monitor) in the "
            "raporty/ directory"
        ),
        "cli.report_dir": (
            "alternative report directory (default: raporty/ next to the program)"
        ),
        "cli.report_dir_metavar": "DIRECTORY",
        "cli.list": "only list the discovered devices and exit",
        "cli.lang": "output language (default: en)",
        "cli.invalid_times": "Scan and refresh times must be greater than zero.",
        "cli.missing_bleak": (
            "The Bleak library is missing. Install the dependencies: "
            "python -m pip install -r requirements.txt"
        ),
        "scan.start": "Scanning Bluetooth LE ({seconds:g} s)…",
        "scan.status": "  … scanning: {remaining:4d} s left | found {found}",
        "scan.name_from_advertisement": (
            "  ~ {name:36} {address:17}  (name from advertisement)"
        ),
        "scan.windows_cache_check": "  … checking the Windows name cache ({count})",
        "scan.name_from_windows_cache": (
            "  ~ {name:36} {address:17}  (from the Windows cache)"
        ),
        "scan.finished": "  = scan finished: {count} devices",
        "main.none_found": (
            "No Humsienk devices found (HS03/HS04). Check Bluetooth, move closer to "
            "the battery, press the BMS wake button and scan again. If the BMS does "
            "not advertise a name, try --all."
        ),
        "main.all_warning": (
            "Note: --all shows every BLE device. Protocol compatibility is verified "
            "after connecting."
        ),
        "main.address_not_found": "No device found with address {address}.",
        "main.selected": "Selected: {name} ({address})",
        "main.error": "Error: {message}",
        "main.back_to_list_prompt": "\nEnter — back to the list, q — quit: ",
        "main.empty_device_list": "The device list is empty now.",
        "main.finished": "\nRead finished.",
        "tui.title": "Humsienk BLE — battery selection",
        "tui.range": "   ({start}-{end} of {total})",
        "tui.ready": "Ready.",
        "tui.footer": (
            "↑/↓ — select   Enter — read   m — monitor   s — save report   "
            "p — check all   r — refresh   q — quit"
        ),
        "tui.all_inspected": "All devices inspected.",
        "tui.inspecting": "Inspecting names and protocols ({done}/{total})...",
        "tui.inspection_done": "Done — inspected {count} devices.",
        "chooser.header": "\nDiscovered Bluetooth LE devices:",
        "chooser.prompt": "Choose a number (m<N> — monitor, e.g. m1; q — quit): ",
        "chooser.invalid": "Invalid selection.",
        "connect.connected": "Connected to: {name} ({address}) [{protocol}]",
        "connect.monitor_mode": (
            "MONITOR — refreshing every {interval:g} s, Ctrl+C stops."
        ),
        "connect.unsupported": (
            "The device exposes neither the HS03 (0001) nor the HS04 (FFF0) service; "
            "it may speak a different Humsienk protocol."
        ),
        "report.title": "Humsienk battery status report",
        "report.date": "Date: {timestamp}",
        "report.device": "Device: {name}",
        "report.address": "Address: {address}",
        "report.saved": "Report saved: {path}",
        "report.monitor_log": "Monitor log (CSV): {path}",
        "report.save_failed": "Could not save the report: {error}",
        "status.heading": "              HUMSIENK  |  BATTERY STATUS",
        "status.identity": "Identification:            {identity}",
        "status.protocol": "Protocol:                  {protocol}",
        "status.soh": "State of health (SOH):     {soh:>6}%",
        "status.soc": "State of charge:           {soc:>6}%",
        "status.voltage": "Pack voltage:              {voltage:>7.2f} V",
        "status.current": "Current:                   {current:>+7.1f} A  ({state})",
        "status.remaining_capacity": "Remaining capacity:        {capacity:>7.1f} Ah",
        "status.total_capacity": "Total capacity:            {capacity:>7.1f} Ah",
        "status.design_capacity": "Design capacity:           {capacity:>7.1f} Ah",
        "status.cycles": "Cycle count:               {cycles:>7}",
        "status.mosfet": (
            "MOSFET:                    charging {charging}, discharging {discharging}"
        ),
        "status.balancing": "Balancing:                 {balancer}",
        "status.problems": "Problem codes:             {problems}",
        "status.cells": (
            "Cells:                     {count}  "
            "(min {minimum:.3f} V / max {maximum:.3f} V)"
        ),
        "status.cell_voltages": "Cell voltages:",
        "status.temperatures": "Temperatures (BMS sensor order, roles not confirmed):",
        "status.sensor": "  Sensor {index}: {temperature:.1f} °C",
        "state.charging": "CHARGING",
        "state.discharging": "DISCHARGING",
        "state.idle": "IDLE",
        "value.active": "active",
        "value.inactive": "inactive",
        "value.none": "none",
        "device.no_name": "Unnamed device",
        "badge.other": "  [other]",
        "error.allowlisted_reads_only": "Only allowlisted BMS reads are supported.",
        "error.bad_frame_length": "The response frame has an invalid length.",
        "error.bad_frame_markers": "Invalid frame start or end marker.",
        "error.length_mismatch": "The frame length does not match the header.",
        "error.bad_crc": "Invalid CRC checksum.",
        "error.wrong_request": "The BMS replied to a different request.",
        "error.unsupported_version": (
            "Unsupported BMS protocol version: {version} (v4 required)."
        ),
        "error.read_rejected": "The BMS rejected the read request.",
        "error.unknown_response_type": "Unknown BMS response type.",
        "error.empty_payload": "The BMS returned an empty data set.",
        "error.bad_cell_data": "Invalid cell count or cell data.",
        "error.bad_temperature_data": "Invalid temperature sensor count or data.",
        "error.bad_soc": "Invalid state of charge: {soc}%.",
        "error.hs03_bad_frame_length": "The HS03 frame has an invalid length.",
        "error.hs03_bad_frame_start": "Invalid HS03 frame start marker.",
        "error.hs03_length_mismatch": (
            "The HS03 frame length does not match the header."
        ),
        "error.hs03_wrong_command": "The HS03 BMS replied to a different command.",
        "error.hs03_bad_checksum": "Invalid HS03 checksum.",
        "error.hs03_short_analog": "The HS03 analog data response is too short.",
        "error.hs03_short_status": "The HS03 status response is too short.",
        "error.hs03_no_cell_voltages": "The HS03 BMS did not send cell voltages.",
        "error.hs03_no_active_cell": "The HS03 BMS reported no active cell.",
        "error.response_timeout": "Timed out waiting for the BMS response.",
        "error.hs03_response_timeout": "Timed out waiting for the HS03 response.",
        "error.bluetooth_overflow": "Too much Bluetooth data; the read was aborted.",
        "error.response_too_large": "The BMS response exceeds the allowed size.",
        "error.declared_frame_too_large": "The BMS declared a frame that is too large.",
        "error.trailing_bytes": "Extra bytes arrived after the BMS response.",
        "error.hs03_response_too_large": "The HS03 response exceeds the allowed size.",
        "error.hs03_declared_frame_too_large": (
            "The HS03 BMS declared a frame that is too large."
        ),
        "error.hs03_trailing_bytes": "Extra bytes arrived after the HS03 response.",
        "error.auth_rejected": "The BMS rejected the HiLink authentication.",
        "error.bad_product_response": "Invalid BMS identification response.",
        "error.bad_board_count": "The BMS returned an invalid module count.",
        "error.missing_board_one": (
            "The BMS did not report the required cell module number 1."
        ),
    },
    "pl": {
        "cli.description": "Odczyt tylko danych BMS Humsienk przez Bluetooth LE.",
        "cli.help": "pokaż tę pomoc i zakończ",
        "cli.scan_seconds": "czas skanowania Bluetooth w sekundach (domyślnie: 15)",
        "cli.address": "adres urządzenia do pominięcia interaktywnego wyboru",
        "cli.all": "pokaż wszystkie urządzenia BLE, nie tylko kandydatów Humsienk",
        "cli.no_resolve": (
            "nie dociągaj nazw i protokołów przez GATT (szybszy start TUI)"
        ),
        "cli.watch": "odświeżaj dane w podanym odstępie czasu (tryb monitora)",
        "cli.watch_metavar": "SEKUNDY",
        "cli.report": (
            "zapisuj raport .txt (odczyt) lub log .csv (monitor) w katalogu raporty/"
        ),
        "cli.report_dir": "inny katalog raportów (domyślnie raporty/ obok programu)",
        "cli.report_dir_metavar": "KATALOG",
        "cli.list": "tylko wypisz znalezione urządzenia i zakończ",
        "cli.lang": "język komunikatów (domyślnie: en)",
        "cli.invalid_times": (
            "Czasy skanowania i odświeżania muszą być większe od zera."
        ),
        "cli.missing_bleak": (
            "Brak biblioteki Bleak. Zainstaluj zależności: "
            "python -m pip install -r requirements.txt"
        ),
        "scan.start": "Skanowanie Bluetooth LE ({seconds:g} s)…",
        "scan.status": (
            "  … skanowanie: pozostało {remaining:4d} s | znaleziono {found}"
        ),
        "scan.name_from_advertisement": "  ~ {name:36} {address:17}  (nazwa z reklamy)",
        "scan.windows_cache_check": "  … sprawdzam pamięć nazw Windows ({count})",
        "scan.name_from_windows_cache": (
            "  ~ {name:36} {address:17}  (z pamięci Windows)"
        ),
        "scan.finished": "  = skanowanie zakończone: {count} urządzeń",
        "main.none_found": (
            "Nie znaleziono urządzeń Humsienk (HS03/HS04). Sprawdź Bluetooth, "
            "zbliż się do akumulatora, wciśnij przycisk wybudzania BMS i ponów "
            "skan. Jeśli BMS nie ogłasza nazwy, spróbuj --all."
        ),
        "main.all_warning": (
            "Uwaga: --all pokazuje wszystkie urządzenia BLE. Zgodność "
            "protokołu jest weryfikowana po połączeniu."
        ),
        "main.address_not_found": "Nie znaleziono urządzenia o adresie {address}.",
        "main.selected": "Wybrano: {name} ({address})",
        "main.error": "Błąd: {message}",
        "main.back_to_list_prompt": "\nEnter — wróć do listy, q — wyjście: ",
        "main.empty_device_list": "Lista urządzeń jest teraz pusta.",
        "main.finished": "\nZakończono odczyt.",
        "tui.title": "Humsienk BLE — wybór akumulatora",
        "tui.range": "   ({start}-{end} z {total})",
        "tui.ready": "Gotowe.",
        "tui.footer": (
            "↑/↓ — wybór   Enter — odczyt   m — monitor   s — zapis raportu   "
            "p — sprawdź wszystkie   r — ponów   q — wyjście"
        ),
        "tui.all_inspected": "Wszystkie urządzenia sprawdzone.",
        "tui.inspecting": "Sprawdzam nazwy i protokoły ({done}/{total})...",
        "tui.inspection_done": "Gotowe — sprawdzono {count} urządzeń.",
        "chooser.header": "\nZnalezione urządzenia Bluetooth LE:",
        "chooser.prompt": "Wybierz numer (m<N> — monitor, np. m1; q — wyjście): ",
        "chooser.invalid": "Nieprawidłowy wybór.",
        "connect.connected": "Połączono z: {name} ({address}) [{protocol}]",
        "connect.monitor_mode": (
            "MONITOR — odświeżanie co {interval:g} s, Ctrl+C kończy."
        ),
        "connect.unsupported": (
            "Urządzenie nie udostępnia usługi HS03 (0001) ani HS04 (FFF0); "
            "to może być inny protokół Humsienk."
        ),
        "report.title": "Raport stanu akumulatora Humsienk",
        "report.date": "Data: {timestamp}",
        "report.device": "Urządzenie: {name}",
        "report.address": "Adres: {address}",
        "report.saved": "Raport zapisany: {path}",
        "report.monitor_log": "Log monitora (CSV): {path}",
        "report.save_failed": "Nie udało się zapisać raportu: {error}",
        "status.heading": "              HUMSIENK  |  STAN AKUMULATORA",
        "status.identity": "Identyfikacja:             {identity}",
        "status.protocol": "Protokół:                  {protocol}",
        "status.soh": "Stan zdrowia (SOH):        {soh:>6}%",
        "status.soc": "Naładowanie:               {soc:>6}%",
        "status.voltage": "Napięcie pakietu:          {voltage:>7.2f} V",
        "status.current": "Prąd:                      {current:>+7.1f} A  ({state})",
        "status.remaining_capacity": "Pojemność pozostała:      {capacity:>7.1f} Ah",
        "status.total_capacity": "Pojemność całkowita:       {capacity:>7.1f} Ah",
        "status.design_capacity": "Pojemność projektowa:      {capacity:>7.1f} Ah",
        "status.cycles": "Liczba cykli:              {cycles:>7}",
        "status.mosfet": (
            "MOSFET:                    ładowanie {charging}, "
            "rozładowanie {discharging}"
        ),
        "status.balancing": "Balansowanie:              {balancer}",
        "status.problems": "Kody problemów:            {problems}",
        "status.cells": (
            "Ogniwa:                    {count}  "
            "(min {minimum:.3f} V / max {maximum:.3f} V)"
        ),
        "status.cell_voltages": "Napięcia ogniw:",
        "status.temperatures": (
            "Temperatury (kolejność czujników z BMS, role niepotwierdzone):"
        ),
        "status.sensor": "  Czujnik {index}: {temperature:.1f} °C",
        "state.charging": "ŁADOWANIE",
        "state.discharging": "ROZŁADOWANIE",
        "state.idle": "POSTÓJ",
        "value.active": "aktywne",
        "value.inactive": "nieaktywne",
        "value.none": "brak",
        "device.no_name": "Urządzenie bez nazwy",
        "badge.other": "  [inny]",
        "error.allowlisted_reads_only": "Tylko dozwolone odczyty BMS są obsługiwane.",
        "error.bad_frame_length": "Ramka odpowiedzi ma nieprawidłową długość.",
        "error.bad_frame_markers": "Nieprawidłowy początek lub koniec ramki.",
        "error.length_mismatch": "Długość ramki nie zgadza się z nagłówkiem.",
        "error.bad_crc": "Nieprawidłowa suma kontrolna CRC.",
        "error.wrong_request": "BMS zwrócił odpowiedź na inne żądanie.",
        "error.unsupported_version": (
            "Nieobsługiwana wersja protokołu BMS: {version} (wymagana v4)."
        ),
        "error.read_rejected": "BMS odrzucił żądanie odczytu.",
        "error.unknown_response_type": "Nieznany typ odpowiedzi BMS.",
        "error.empty_payload": "BMS zwrócił pusty zestaw danych.",
        "error.bad_cell_data": "Nieprawidłowa liczba lub dane ogniw.",
        "error.bad_temperature_data": (
            "Nieprawidłowa liczba lub dane czujników temperatury."
        ),
        "error.bad_soc": "Nieprawidłowy poziom naładowania: {soc}%.",
        "error.hs03_bad_frame_length": "Ramka HS03 ma nieprawidłową długość.",
        "error.hs03_bad_frame_start": "Nieprawidłowy początek ramki HS03.",
        "error.hs03_length_mismatch": "Długość ramki HS03 nie zgadza się z nagłówkiem.",
        "error.hs03_wrong_command": "BMS HS03 zwrócił odpowiedź na inną komendę.",
        "error.hs03_bad_checksum": "Nieprawidłowa suma kontrolna HS03.",
        "error.hs03_short_analog": "Zbyt krótka odpowiedź HS03 z danymi analogowymi.",
        "error.hs03_short_status": "Zbyt krótka odpowiedź HS03 ze statusem.",
        "error.hs03_no_cell_voltages": "BMS HS03 nie przesłał napięć ogniw.",
        "error.hs03_no_active_cell": "BMS HS03 nie zgłosił żadnego aktywnego ogniwa.",
        "error.response_timeout": "Przekroczono czas oczekiwania na odpowiedź BMS.",
        "error.hs03_response_timeout": (
            "Przekroczono czas oczekiwania na odpowiedź HS03."
        ),
        "error.bluetooth_overflow": "Za dużo danych Bluetooth; przerwano odczyt.",
        "error.response_too_large": "Odpowiedź BMS przekracza dozwolony rozmiar.",
        "error.declared_frame_too_large": "BMS zadeklarował zbyt dużą ramkę.",
        "error.trailing_bytes": "Odebrano nadmiarowe bajty odpowiedzi BMS.",
        "error.hs03_response_too_large": "Odpowiedź HS03 przekracza dozwolony rozmiar.",
        "error.hs03_declared_frame_too_large": "BMS HS03 zadeklarował zbyt dużą ramkę.",
        "error.hs03_trailing_bytes": "Odebrano nadmiarowe bajty odpowiedzi HS03.",
        "error.auth_rejected": "BMS odrzucił uwierzytelnienie HiLink.",
        "error.bad_product_response": "Nieprawidłowa odpowiedź identyfikacyjna BMS.",
        "error.bad_board_count": "BMS zwrócił nieprawidłową liczbę modułów.",
        "error.missing_board_one": "BMS nie zgłosił wymaganego modułu ogniw nr 1.",
    },
}

_LANG = DEFAULT_LANG


def set_language(lang: str) -> None:
    """Select the message catalogue used by tr()."""
    global _LANG
    _LANG = lang if lang in MESSAGES else DEFAULT_LANG


def tr(key: str, **kwargs: Any) -> str:
    """Return the message for *key* in the active language.

    Placeholders are filled through str.format(); a missing key falls back to
    the key itself.
    """
    template = MESSAGES.get(_LANG, {}).get(key)
    if template is None:
        return key
    return template.format(**kwargs) if kwargs else template


def no_name() -> str:
    """Label for a device whose name could not be resolved."""
    return tr("device.no_name")


class ProtocolError(Exception):
    pass


@dataclass(frozen=True)
class BatterySnapshot:
    protocol: str
    voltage_v: float
    current_a: float
    soc_percent: int
    remaining_capacity_ah: float
    total_capacity_ah: float | None
    cycles: int
    design_capacity_ah: float
    cells_mv: tuple[int, ...]
    temperatures_c: tuple[float, ...]
    soh_percent: int | None = None
    charging_mosfet: bool | None = None
    discharging_mosfet: bool | None = None
    balancer_active: bool | None = None
    problem_code: int | None = None


@dataclass
class DeviceRow:
    """Device on the selection list; name and protocol resolved on connect."""

    device: Any
    name: str
    rssi: int
    protocol: str = ""
    inspected: bool = False


class TuiUnavailable(Exception):
    """TUI mode is not available in this terminal."""


def crc16_modbus(data: bytes) -> int:
    crc = 0xFFFF
    for byte in data:
        crc ^= byte
        for _ in range(8):
            crc = (crc >> 1) ^ (0xA001 if crc & 1 else 0)
    return crc


def build_read_request(point: int, extended: bool = False) -> bytes:
    if point not in READ_POINTS or (extended and point != ANALOG_POINT):
        raise ValueError(tr("error.allowlisted_reads_only"))

    frame = bytes((0x7E, int(extended), 1, 3, 0, point, 0, 0))
    if extended:
        frame += bytes((0, 5, 1, 0, 32, 0, 32))
    checksum = crc16_modbus(frame)
    return frame + checksum.to_bytes(2, "big") + b"\x0d"


def validate_response(frame: bytes, expected_point: int) -> bytes:
    if len(frame) < 11 or len(frame) > MAX_FRAME_SIZE:
        raise ProtocolError(tr("error.bad_frame_length"))
    if frame[0] != 0x7E or frame[-1] != 0x0D:
        raise ProtocolError(tr("error.bad_frame_markers"))
    if int.from_bytes(frame[6:8], "big") + 11 != len(frame):
        raise ProtocolError(tr("error.length_mismatch"))
    expected_crc = crc16_modbus(frame[:-3])
    if int.from_bytes(frame[-3:-1], "big") != expected_crc:
        raise ProtocolError(tr("error.bad_crc"))
    if frame[2] != 1 or int.from_bytes(frame[4:6], "big") != expected_point:
        raise ProtocolError(tr("error.wrong_request"))
    if frame[1] != 4:
        raise ProtocolError(
            tr("error.unsupported_version", version=frame[1])
        )
    if frame[3] == 0x86:
        raise ProtocolError(tr("error.read_rejected"))
    if frame[3] != 3:
        raise ProtocolError(tr("error.unknown_response_type"))
    return frame[8:-3]


def decode_snapshot(payload: bytes) -> BatterySnapshot:
    if not payload:
        raise ProtocolError(tr("error.empty_payload"))

    cell_count = payload[0]
    if not 1 <= cell_count <= 32 or len(payload) < 2 + 2 * cell_count:
        raise ProtocolError(tr("error.bad_cell_data"))

    temperature_count = payload[1 + 2 * cell_count]
    offset = 2 + 2 * cell_count + 2 * temperature_count
    if not 2 <= temperature_count <= 32 or len(payload) < offset + 14:
        raise ProtocolError(tr("error.bad_temperature_data"))

    soc = int.from_bytes(payload[offset + 12 : offset + 14], "big")
    if soc > 100:
        raise ProtocolError(tr("error.bad_soc", soc=soc))

    raw_current = int.from_bytes(payload[offset : offset + 2], "big")
    current = (raw_current & 0x3FFF) / (10 if raw_current & 0x4000 else 1)
    if raw_current & 0x8000:
        current = -current

    cells = tuple(
        int.from_bytes(payload[1 + index * 2 : 3 + index * 2], "big")
        for index in range(cell_count)
    )
    temperatures_start = 2 + 2 * cell_count
    temperatures = tuple(
        (int.from_bytes(payload[pos : pos + 2], "big") - 2730) / 10
        for pos in range(
            temperatures_start,
            temperatures_start + 2 * temperature_count,
            2,
        )
    )

    return BatterySnapshot(
        protocol="HS04/WATT",
        voltage_v=int.from_bytes(payload[offset + 2 : offset + 4], "big") / 100,
        current_a=current,
        soc_percent=soc,
        remaining_capacity_ah=int.from_bytes(
            payload[offset + 4 : offset + 6], "big"
        )
        / 10,
        total_capacity_ah=int.from_bytes(
            payload[offset + 6 : offset + 8], "big"
        )
        / 10,
        cycles=int.from_bytes(payload[offset + 8 : offset + 10], "big"),
        design_capacity_ah=int.from_bytes(
            payload[offset + 10 : offset + 12], "big"
        )
        / 10,
        cells_mv=cells,
        temperatures_c=temperatures,
    )


def build_hs03_command(command: int) -> bytes:
    """HS03 command: AA, command, 0x00, checksum (LE)."""
    body = bytes((command, 0x00))
    return bytes((HS03_HEAD,)) + body + (sum(body) & 0xFF).to_bytes(2, "little")


def hs03_checksum(frame: bytes) -> int:
    return sum(frame) & 0xFFFF


def validate_hs03_response(frame: bytes, expected_command: int) -> bytes:
    if len(frame) < HS03_MIN_FRAME or len(frame) > MAX_FRAME_SIZE:
        raise ProtocolError(tr("error.hs03_bad_frame_length"))
    if frame[0] != HS03_HEAD:
        raise ProtocolError(tr("error.hs03_bad_frame_start"))
    if len(frame) != frame[2] + HS03_MIN_FRAME:
        raise ProtocolError(tr("error.hs03_length_mismatch"))
    if frame[1] != expected_command:
        raise ProtocolError(tr("error.hs03_wrong_command"))
    if int.from_bytes(frame[-2:], "little") != hs03_checksum(frame[1:-2]):
        raise ProtocolError(tr("error.hs03_bad_checksum"))
    return frame[3:-2]


def decode_hs03(analog: bytes, status: bytes, cells_frame: bytes) -> BatterySnapshot:
    if len(analog) < 26:
        raise ProtocolError(tr("error.hs03_short_analog"))
    if len(status) < 8:
        raise ProtocolError(tr("error.hs03_short_status"))

    soc = analog[8]
    if soc > 100:
        raise ProtocolError(tr("error.bad_soc", soc=soc))

    cell_slots = min(HS03_MAX_CELLS, len(cells_frame) // 2)
    if cell_slots < 1:
        raise ProtocolError(tr("error.hs03_no_cell_voltages"))

    # The BMS always reports a fixed number of slots (e.g. 24); unused ones read 0 mV.
    cells = tuple(
        value
        for index in range(cell_slots)
        if (value := int.from_bytes(cells_frame[2 * index : 2 + 2 * index], "little"))
    )
    if not cells:
        raise ProtocolError(tr("error.hs03_no_active_cell"))

    temperatures = tuple(
        float(int.from_bytes(analog[20 + index : 21 + index], "little", signed=True))
        for index in range(HS03_TEMP_COUNT)
    )

    return BatterySnapshot(
        protocol="HS03/HiLink",
        voltage_v=int.from_bytes(analog[0:4], "little") / 1000,
        current_a=int.from_bytes(analog[4:8], "little", signed=True) / 1000,
        soc_percent=soc,
        remaining_capacity_ah=int.from_bytes(analog[10:14], "little") / 1000,
        total_capacity_ah=None,
        cycles=int.from_bytes(analog[18:20], "little"),
        design_capacity_ah=round(int.from_bytes(analog[14:18], "little") / 1000),
        cells_mv=cells,
        temperatures_c=temperatures,
        soh_percent=analog[9],
        charging_mosfet=bool(status[4] & 0x80),
        discharging_mosfet=bool(status[6] & 0x80),
        balancer_active=bool(status[5] & 0x80),
        problem_code=int.from_bytes(status[4:8], "little") & 0xFF7F7F7F,
    )


def detect_protocol(client: Any) -> str:
    """Detect the protocol variant from the device GATT services."""
    service = client.services.get_service(SERVICE_UUID)
    if service is not None and all(
        service.get_characteristic(characteristic_uuid)
        for characteristic_uuid in (NOTIFY_UUID, WRITE_UUID, AUTH_UUID)
    ):
        return "HS04"
    hs03 = client.services.get_service(HS03_SERVICE_UUID)
    if hs03 is not None and all(
        hs03.get_characteristic(characteristic_uuid)
        for characteristic_uuid in (HS03_NOTIFY_UUID, HS03_WRITE_UUID)
    ):
        return "HS03"
    return ""


_SERVICE_UUID_FORMS = {SERVICE_UUID, "fff0", "0000fff0"}
_NAME_PREFIXES = ("hs", "eco", "dch")


def _is_humsienk_candidate(name: str, service_uuids: list[str]) -> bool:
    normalized = name.strip().casefold()
    if normalized.startswith(_NAME_PREFIXES) or "humsienk" in normalized:
        return True
    return any(
        uuid.strip().casefold() in _SERVICE_UUID_FORMS for uuid in service_uuids
    )


def format_registry_address(raw: str) -> str:
    digits = "".join(character for character in raw if character.isalnum()).upper()
    if len(digits) != 12 or any(character not in "0123456789ABCDEF" for character in digits):
        return ""
    return ":".join(digits[index : index + 2] for index in range(0, 12, 2))


def load_windows_known_names() -> dict[str, str]:
    """Names remembered by Windows — BTHPORT cache, updated after connections."""
    if sys.platform != "win32":
        return {}
    import winreg

    names: dict[str, str] = {}
    try:
        with winreg.OpenKey(
            winreg.HKEY_LOCAL_MACHINE,
            r"SYSTEM\CurrentControlSet\Services\BTHPORT\Parameters\Devices",
        ) as devices_key:
            index = 0
            while True:
                try:
                    subkey_name = winreg.EnumKey(devices_key, index)
                    index += 1
                except OSError:
                    break
                address = format_registry_address(subkey_name)
                if not address:
                    continue
                try:
                    with winreg.OpenKey(devices_key, subkey_name) as device_key:
                        value, _ = winreg.QueryValueEx(device_key, "Name")
                except OSError:
                    continue
                if isinstance(value, bytes):
                    value = value.decode("utf-8", errors="replace")
                cleaned = str(value).rstrip("\x00").strip()
                if cleaned:
                    names[address] = cleaned
    except OSError:
        return {}
    return names


def resolve_display_name(*names: str) -> str:
    for name in names:
        cleaned = (name or "").strip()
        if cleaned:
            return cleaned
    return no_name()


async def read_gatt_name(client: Any) -> str:
    """Read the device name from GATT (0x2A00) — the same source the mobile app uses."""
    try:
        service = client.services.get_service(GENERIC_ACCESS_UUID)
        characteristic = (
            service.get_characteristic(DEVICE_NAME_UUID) if service else None
        )
        if characteristic is None:
            return ""
        value = await client.read_gatt_char(characteristic)
    except Exception:
        return ""
    return (
        bytes(value)
        .decode("utf-8", errors="replace")
        .replace("\x00", "")
        .strip()
    )


async def load_windows_cached_names(addresses: list[str]) -> dict[str, str]:
    """Names from the Windows cache — the same source the phone/Settings see.

    Windows labels unknown devices as "Bluetooth <address>"; we show that label
    just like Settings does instead of pretending the name is missing.
    """
    if sys.platform != "win32" or not addresses:
        return {}
    try:
        from winrt.windows.devices.bluetooth import BluetoothLEDevice
    except Exception:
        return {}

    names: dict[str, str] = {}
    for mac in addresses:
        if not mac:
            continue
        try:
            address = int(re.sub(r"[^0-9a-fA-F]", "", mac), 16)
            device = await BluetoothLEDevice.from_bluetooth_address_async(address)
        except Exception:
            continue
        if device is None:
            continue
        try:
            name = (device.name or "").strip()
        finally:
            try:
                device.close()
            except Exception:
                pass
        if name:
            names[mac.upper()] = name
    return names


def format_scan_status(remaining: float, found: int) -> str:
    return tr("scan.status", remaining=math.ceil(remaining), found=found)


def format_found_line(name: str, address: str, rssi: int) -> str:
    return f"  + {name[:36]:36} {address:17} RSSI {rssi:>5} dBm"


async def scan_devices(
    scan_seconds: float, include_all: bool = False
) -> list[tuple[Any, str, int]]:
    """Scan BLE with a countdown; devices are shown as soon as they are detected."""
    from bleak import BleakScanner

    known_names = load_windows_known_names()
    placeholder = no_name()
    found: dict[str, tuple[Any, str, int]] = {}
    live = sys.stdout.isatty()

    def print_live(text: str) -> None:
        sys.stdout.write("\r\x1b[2K")
        print(text)

    def on_detection(device: Any, advertisement: Any) -> None:
        device_name = device.name or ""
        advertised_name = advertisement.local_name or ""
        cached_name = known_names.get(device.address.upper(), "")
        name = resolve_display_name(device_name, advertised_name, cached_name)
        service_uuids = advertisement.service_uuids or []
        is_candidate = any(
            _is_humsienk_candidate(candidate_name, service_uuids)
            for candidate_name in (device_name, advertised_name, cached_name)
        )
        if not (is_candidate or include_all):
            return

        previous = found.get(device.address)
        best_name = name if name != placeholder else (previous[1] if previous else name)
        found[device.address] = (device, best_name, advertisement.rssi)
        if not live:
            return
        if previous is None:
            print_live(format_found_line(best_name, device.address, advertisement.rssi))
        elif previous[1] == placeholder and best_name != placeholder:
            print_live(
                tr(
                    "scan.name_from_advertisement",
                    name=best_name[:36],
                    address=device.address,
                )
            )

    if live:
        print(tr("scan.start", seconds=scan_seconds))

    scanner = BleakScanner(detection_callback=on_detection)
    await scanner.start()
    loop = asyncio.get_running_loop()
    deadline = loop.time() + scan_seconds
    next_tick = 0.0
    try:
        while True:
            remaining = deadline - loop.time()
            if remaining <= 0:
                break
            if live and loop.time() >= next_tick:
                next_tick = loop.time() + 1
                sys.stdout.write("\r\x1b[2K" + format_scan_status(remaining, len(found)))
                sys.stdout.flush()
            await asyncio.sleep(0.2)
    finally:
        await scanner.stop()
        if live:
            sys.stdout.write("\r\x1b[2K")

    unnamed = [
        address for address, (_, name, _) in found.items() if name == placeholder
    ]
    if unnamed:
        if live:
            sys.stdout.write(
                "\r\x1b[2K" + tr("scan.windows_cache_check", count=len(unnamed))
            )
            sys.stdout.flush()
        cached_names = await load_windows_cached_names(unnamed)
        by_upper = {address.upper(): address for address in found}
        for mac_upper, cached_name in cached_names.items():
            key = by_upper.get(mac_upper)
            if key is None:
                continue
            device, _, rssi = found[key]
            found[key] = (device, cached_name, rssi)
            if live:
                print_live(
                    tr(
                        "scan.name_from_windows_cache",
                        name=cached_name[:36],
                        address=key,
                    )
                )

    if live:
        sys.stdout.write("\r\x1b[2K")
        print(tr("scan.finished", count=len(found)))

    return sorted(found.values(), key=lambda candidate: candidate[2], reverse=True)


async def inspect_device(
    device: Any, timeout: float = 6, attempts: int = 2
) -> tuple[str, str]:
    """Read the GATT name (0x2A00) and detect the protocol; retries once."""
    from bleak import BleakClient

    for attempt in range(attempts):
        try:
            async with BleakClient(device, timeout=timeout) as client:
                name = await read_gatt_name(client)
                protocol = detect_protocol(client)
            return name, protocol
        except Exception:
            if attempt + 1 >= attempts:
                return "", ""
            await asyncio.sleep(0.5)
    return "", ""


async def inspect_row(row: DeviceRow, timeout: float = 6) -> None:
    if row.name == no_name():
        cached_names = await load_windows_cached_names([row.device.address])
        cached_name = cached_names.get(row.device.address.upper())
        if cached_name:
            row.name = cached_name
    name, protocol = await inspect_device(row.device, timeout)
    if name:
        row.name = name
    row.protocol = protocol
    row.inspected = True


def visible_window(count: int, selected: int, height: int) -> tuple[int, int]:
    """Row range visible around the selection, with list scrolling."""
    if height <= 0 or count <= 0:
        return 0, 0
    if count <= height:
        return 0, count
    start = min(max(selected - height // 2, 0), count - height)
    return start, start + height


def protocol_badge(protocol: str, inspected: bool) -> str:
    if not inspected:
        return ""
    if protocol == "HS03":
        return "  [HS03]"
    if protocol == "HS04":
        return "  [HS04]"
    return tr("badge.other")


def _enable_ansi() -> None:
    if sys.platform != "win32":
        return
    import ctypes

    kernel32 = ctypes.windll.kernel32
    handle = kernel32.GetStdHandle(-11)
    mode = ctypes.c_uint32()
    if kernel32.GetConsoleMode(handle, ctypes.byref(mode)):
        kernel32.SetConsoleMode(handle, mode.value | 0x0004)


def _configure_console_encoding() -> None:
    """Enable UTF-8 so arrows and Polish characters work in the Windows console."""
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is None:
            continue
        try:
            reconfigure(encoding="utf-8", errors="replace")
        except (OSError, ValueError):
            pass
    if sys.platform != "win32":
        return
    import ctypes

    try:
        ctypes.windll.kernel32.SetConsoleOutputCP(65001)
        ctypes.windll.kernel32.SetConsoleCP(65001)
    except (AttributeError, OSError):
        pass


def _clear_screen() -> None:
    sys.stdout.write("\x1b[2J\x1b[H")
    sys.stdout.flush()


def _read_keys_thread(
    keys: asyncio.Queue[str],
    loop: asyncio.AbstractEventLoop,
    stop: threading.Event,
) -> None:
    """Read keys (arrows) outside the asyncio thread and forward them to the queue."""
    import time

    if sys.platform != "win32":
        loop.call_soon_threadsafe(keys.put_nowait, "eof")
        return
    import msvcrt

    specials = {"H": "up", "P": "down", "I": "page_up", "Q": "page_down"}
    while not stop.is_set():
        if not msvcrt.kbhit():
            time.sleep(0.05)
            continue
        char = msvcrt.getwch()
        if char in ("\x00", "\xe0"):
            key = specials.get(msvcrt.getwch())
            if key:
                loop.call_soon_threadsafe(keys.put_nowait, key)
            continue
        loop.call_soon_threadsafe(
            keys.put_nowait, "enter" if char in ("\r", "\n") else char.lower()
        )


def _tui_available() -> bool:
    return sys.platform == "win32" and sys.stdin.isatty()


async def select_battery_tui(
    rows: list[DeviceRow], resolve: bool = True
) -> tuple[DeviceRow, str]:
    """Device list with arrow-key support; returns (device, mode)."""
    keys: asyncio.Queue[str] = asyncio.Queue()
    loop = asyncio.get_running_loop()
    stop = threading.Event()
    thread = threading.Thread(
        target=_read_keys_thread, args=(keys, loop, stop), daemon=True
    )
    thread.start()
    _configure_console_encoding()
    _enable_ansi()

    index = 0
    status = ""
    resolver: asyncio.Task[None] | None = None

    def render() -> None:
        height = max(4, shutil.get_terminal_size((100, 30)).lines - 7)
        start, end = visible_window(len(rows), index, height)
        lines = [tr("tui.title"), ""]
        for position in range(start, end):
            row = rows[position]
            marker = ">" if position == index else " "
            lines.append(
                f"{marker} {row.name[:38]:38} {row.device.address:17} "
                f"RSSI {row.rssi:>5} dBm{protocol_badge(row.protocol, row.inspected)}"
            )
        if len(rows) > height:
            lines.append(tr("tui.range", start=start + 1, end=end, total=len(rows)))
        lines.extend(
            [
                "",
                status or tr("tui.ready"),
                tr("tui.footer"),
            ]
        )
        _clear_screen()
        print("\n".join(lines))

    def start_resolution(only_missing: bool = True) -> None:
        nonlocal resolver, status
        if resolver and not resolver.done():
            return
        if not only_missing:
            for row in rows:
                row.inspected = False
        pending = [row for row in rows if not row.inspected]
        if not pending:
            status = tr("tui.all_inspected")
            render()
            return
        total = len(pending)
        status = tr("tui.inspecting", done=0, total=total)

        async def run() -> None:
            nonlocal status
            for number, row in enumerate(pending, start=1):
                await inspect_row(row)
                status = tr("tui.inspecting", done=number, total=total)
                render()
            status = tr("tui.inspection_done", count=total)
            render()

        resolver = asyncio.create_task(run())

    try:
        if resolve:
            start_resolution()
        render()
        while True:
            try:
                key = await asyncio.wait_for(keys.get(), timeout=0.4)
            except asyncio.TimeoutError:
                continue
            if key == "up":
                index = (index - 1) % len(rows)
            elif key == "down":
                index = (index + 1) % len(rows)
            elif key == "page_up":
                index = max(0, index - 5)
            elif key == "page_down":
                index = min(len(rows) - 1, index + 5)
            elif key == "enter":
                return rows[index], "read"
            elif key == "m":
                return rows[index], "monitor"
            elif key == "s":
                return rows[index], "report"
            elif key in ("r", "p"):
                start_resolution(only_missing=key == "r")
            elif key == "q":
                raise SystemExit(0)
            elif key == "eof":
                raise TuiUnavailable
            render()
    finally:
        stop.set()
        if resolver and not resolver.done():
            resolver.cancel()
            try:
                await resolver
            except asyncio.CancelledError:
                pass
            except Exception:
                pass


async def choose_battery_textual(rows: list[DeviceRow]) -> tuple[DeviceRow, str]:
    """Fallback numeric selection when the TUI is unavailable."""
    while True:
        print(tr("chooser.header"))
        for index, row in enumerate(rows, start=1):
            print(
                f"  {index}. {row.name}  [{row.device.address}]  "
                f"RSSI {row.rssi} dBm"
            )
        choice = input(tr("chooser.prompt")).strip().lower()
        if choice == "q":
            raise SystemExit(0)
        mode = "monitor" if choice.startswith("m") else "read"
        number = choice[1:] if mode == "monitor" else choice
        if number.isdecimal() and 1 <= int(number) <= len(rows):
            return rows[int(number) - 1], mode
        print(tr("chooser.invalid"))


async def receive_frame(
    notifications: asyncio.Queue[bytes], overflowed: list[bool], timeout: float = 10
) -> bytes:
    data = bytearray()
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout

    while True:
        remaining = deadline - loop.time()
        if remaining <= 0:
            raise ProtocolError(tr("error.response_timeout"))
        try:
            chunk = await asyncio.wait_for(notifications.get(), timeout=remaining)
        except asyncio.TimeoutError as exc:
            raise ProtocolError(tr("error.response_timeout")) from exc

        if overflowed[0]:
            raise ProtocolError(tr("error.bluetooth_overflow"))
        data.extend(chunk)
        if len(data) > MAX_FRAME_SIZE:
            raise ProtocolError(tr("error.response_too_large"))
        if len(data) >= 8:
            expected_length = int.from_bytes(data[6:8], "big") + 11
            if expected_length > MAX_FRAME_SIZE:
                raise ProtocolError(tr("error.declared_frame_too_large"))
            if len(data) >= expected_length:
                if len(data) != expected_length:
                    raise ProtocolError(tr("error.trailing_bytes"))
                return bytes(data)


async def read_point(
    client: Any,
    notifications: asyncio.Queue[bytes],
    overflowed: list[bool],
    point: int,
    extended: bool = False,
) -> bytes:
    await asyncio.sleep(1)
    while not notifications.empty():
        notifications.get_nowait()
    overflowed[0] = False
    await client.write_gatt_char(
        WRITE_UUID, build_read_request(point, extended), response=True
    )
    return validate_response(await receive_frame(notifications, overflowed), point)


async def receive_hs03_frame(
    notifications: asyncio.Queue[bytes],
    overflowed: list[bool],
    expected_command: int,
    timeout: float = 10,
) -> bytes:
    data = bytearray()
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout

    while True:
        remaining = deadline - loop.time()
        if remaining <= 0:
            raise ProtocolError(tr("error.hs03_response_timeout"))
        try:
            chunk = await asyncio.wait_for(notifications.get(), timeout=remaining)
        except asyncio.TimeoutError as exc:
            raise ProtocolError(
                tr("error.hs03_response_timeout")
            ) from exc

        if overflowed[0]:
            raise ProtocolError(tr("error.bluetooth_overflow"))
        data.extend(chunk)
        if len(data) > MAX_FRAME_SIZE:
            raise ProtocolError(tr("error.hs03_response_too_large"))
        if len(data) >= 3:
            expected_length = data[2] + HS03_MIN_FRAME
            if expected_length > MAX_FRAME_SIZE:
                raise ProtocolError(tr("error.hs03_declared_frame_too_large"))
            if len(data) >= expected_length:
                if len(data) != expected_length:
                    raise ProtocolError(tr("error.hs03_trailing_bytes"))
                return validate_hs03_response(bytes(data), expected_command)


async def read_hs03_command(
    client: Any,
    notifications: asyncio.Queue[bytes],
    overflowed: list[bool],
    command: int,
) -> bytes:
    await asyncio.sleep(0.3)
    while not notifications.empty():
        notifications.get_nowait()
    overflowed[0] = False
    await client.write_gatt_char(
        HS03_WRITE_UUID, build_hs03_command(command), response=True
    )
    return await receive_hs03_frame(notifications, overflowed, command)


def format_snapshot(snapshot: BatterySnapshot, identity: str) -> list[str]:
    minimum_cell = min(snapshot.cells_mv) / 1000
    maximum_cell = max(snapshot.cells_mv) / 1000
    state = (
        tr("state.charging")
        if snapshot.current_a > 0
        else tr("state.discharging")
        if snapshot.current_a < 0
        else tr("state.idle")
    )

    lines = [
        "",
        "=" * 52,
        tr("status.heading"),
        "=" * 52,
    ]
    if identity:
        lines.append(tr("status.identity", identity=identity))
    lines.append(tr("status.protocol", protocol=snapshot.protocol))
    if snapshot.soh_percent is not None:
        lines.append(tr("status.soh", soh=snapshot.soh_percent))
    lines.extend(
        [
            tr("status.soc", soc=snapshot.soc_percent),
            tr("status.voltage", voltage=snapshot.voltage_v),
            tr("status.current", current=snapshot.current_a, state=state),
            tr("status.remaining_capacity", capacity=snapshot.remaining_capacity_ah),
        ]
    )
    if snapshot.total_capacity_ah is not None:
        lines.append(tr("status.total_capacity", capacity=snapshot.total_capacity_ah))
    lines.append(tr("status.design_capacity", capacity=snapshot.design_capacity_ah))
    lines.append(tr("status.cycles", cycles=snapshot.cycles))
    if snapshot.charging_mosfet is not None:
        lines.append(
            tr(
                "status.mosfet",
                charging="ON" if snapshot.charging_mosfet else "OFF",
                discharging="ON" if snapshot.discharging_mosfet else "OFF",
            )
        )
    if snapshot.balancer_active is not None:
        balancer = (
            tr("value.active") if snapshot.balancer_active else tr("value.inactive")
        )
        lines.append(tr("status.balancing", balancer=balancer))
    if snapshot.problem_code is not None:
        problems = (
            tr("value.none")
            if snapshot.problem_code == 0
            else f"0x{snapshot.problem_code:08X}"
        )
        lines.append(tr("status.problems", problems=problems))
    lines.append(
        tr(
            "status.cells",
            count=len(snapshot.cells_mv),
            minimum=minimum_cell,
            maximum=maximum_cell,
        )
    )
    lines.append(tr("status.cell_voltages"))
    for start in range(0, len(snapshot.cells_mv), 4):
        cells = snapshot.cells_mv[start : start + 4]
        lines.append(
            "  "
            + "  ".join(
                f"{start + index + 1:02d}: {millivolts / 1000:.3f} V"
                for index, millivolts in enumerate(cells)
            )
        )
    lines.append(tr("status.temperatures"))
    for index, temperature in enumerate(snapshot.temperatures_c, start=1):
        lines.append(tr("status.sensor", index=index, temperature=temperature))
    lines.append("=" * 52)
    return lines


def print_snapshot(snapshot: BatterySnapshot, identity: str) -> None:
    print("\n".join(format_snapshot(snapshot, identity)))


def application_directory() -> Path:
    if getattr(sys, "frozen", False):
        return Path(sys.executable).resolve().parent
    return Path(__file__).resolve().parent


def report_directory(override: str | None = None) -> Path:
    base = (
        Path(override).expanduser()
        if override
        else application_directory() / "raporty"
    )
    base.mkdir(parents=True, exist_ok=True)
    return base


def _safe_name(text: str) -> str:
    cleaned = "".join(
        character if character.isalnum() or character in "-_" else "_"
        for character in text.strip()
    )
    return cleaned.strip("_")[:40] or "urzadzenie"


def save_report(
    snapshot: BatterySnapshot,
    identity: str,
    address: str,
    override_dir: str | None = None,
) -> Path:
    directory = report_directory(override_dir)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    path = directory / f"raport_{_safe_name(identity or address)}_{timestamp}.txt"
    header = [
        tr("report.title"),
        tr("report.date", timestamp=datetime.now().isoformat(timespec="seconds")),
        tr("report.device", name=identity or no_name()),
        tr("report.address", address=address),
    ]
    path.write_text(
        "\n".join(header + format_snapshot(snapshot, identity)) + "\n",
        encoding="utf-8",
    )
    return path


MONITOR_COLUMNS = (
    "time",
    "device",
    "address",
    "protocol",
    "soc_percent",
    "soh_percent",
    "voltage_v",
    "current_a",
    "remaining_ah",
    "design_ah",
    "cycles",
    "min_cell_v",
    "max_cell_v",
    "min_temp_c",
    "max_temp_c",
    "problem_code",
)


def monitor_log_path(
    identity: str, address: str, override_dir: str | None = None
) -> Path:
    directory = report_directory(override_dir)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    return directory / f"monitor_{_safe_name(identity or address)}_{timestamp}.csv"


def monitor_sample_row(
    snapshot: BatterySnapshot, identity: str, address: str
) -> list[str]:
    temperatures = snapshot.temperatures_c or (0.0,)
    return [
        datetime.now().isoformat(timespec="seconds"),
        identity or no_name(),
        address,
        snapshot.protocol,
        str(snapshot.soc_percent),
        "" if snapshot.soh_percent is None else str(snapshot.soh_percent),
        f"{snapshot.voltage_v:.2f}",
        f"{snapshot.current_a:+.2f}",
        f"{snapshot.remaining_capacity_ah:.2f}",
        f"{snapshot.design_capacity_ah:.1f}",
        str(snapshot.cycles),
        f"{min(snapshot.cells_mv) / 1000:.3f}",
        f"{max(snapshot.cells_mv) / 1000:.3f}",
        f"{min(temperatures):.1f}",
        f"{max(temperatures):.1f}",
        "" if snapshot.problem_code is None else f"0x{snapshot.problem_code:08X}",
    ]


def append_monitor_sample(
    path: Path, snapshot: BatterySnapshot, identity: str, address: str
) -> None:
    new_file = not path.exists()
    with path.open("a", encoding="utf-8", newline="") as handle:
        writer = csv.writer(handle)
        if new_file:
            writer.writerow(MONITOR_COLUMNS)
        writer.writerow(monitor_sample_row(snapshot, identity, address))


async def start_notifications(
    client: Any, characteristic_uuid: str
) -> tuple[asyncio.Queue[bytes], list[bool]]:
    notifications: asyncio.Queue[bytes] = asyncio.Queue(maxsize=64)
    overflowed = [False]

    def on_notification(_sender: Any, data: bytearray) -> None:
        try:
            notifications.put_nowait(bytes(data))
        except asyncio.QueueFull:
            overflowed[0] = True

    await client.start_notify(characteristic_uuid, on_notification)
    return notifications, overflowed


async def read_hs04_battery(
    client: Any,
    display_name: str,
    address: str,
    interval: float | None,
    on_snapshot: Any,
) -> None:
    notifications, overflowed = await start_notifications(client, NOTIFY_UUID)
    await client.write_gatt_char(AUTH_UUID, b"HiLink", response=True)
    await asyncio.sleep(0.5)
    auth_status = await client.read_gatt_char(AUTH_UUID)
    if bytes(auth_status) != b"\x01":
        raise ProtocolError(tr("error.auth_rejected"))

    product_payload = await read_point(
        client, notifications, overflowed, PRODUCT_POINT
    )
    if len(product_payload) != 60:
        raise ProtocolError(tr("error.bad_product_response"))
    firmware = product_payload[:20].split(b"\0", 1)[0].decode(
        "ascii", errors="replace"
    ).strip()

    boards = await read_point(
        client, notifications, overflowed, COLLECTION_BOARDS_POINT
    )
    if not boards or not 1 <= boards[0] <= 32:
        raise ProtocolError(tr("error.bad_board_count"))
    board_count = boards[0]
    if len(boards) < 1 + board_count or 1 not in boards[1 : 1 + board_count]:
        raise ProtocolError(tr("error.missing_board_one"))

    print(
        tr(
            "connect.connected",
            name=display_name,
            address=address,
            protocol="HS04/WATT",
        )
    )
    while True:
        analog = await read_point(
            client, notifications, overflowed, ANALOG_POINT, extended=True
        )
        on_snapshot(decode_snapshot(analog), firmware)
        if interval is None:
            return
        await asyncio.sleep(interval)


async def read_hs03_battery(
    client: Any,
    display_name: str,
    address: str,
    interval: float | None,
    on_snapshot: Any,
) -> None:
    notifications, overflowed = await start_notifications(client, HS03_NOTIFY_UUID)
    await read_hs03_command(client, notifications, overflowed, HS03_INIT_COMMAND)

    model = ""
    try:
        model_payload = await read_hs03_command(
            client, notifications, overflowed, HS03_MODEL_COMMAND
        )
        model = (
            model_payload.split(b"\0", 1)[0].decode("ascii", errors="replace").strip()
        )
    except ProtocolError:
        pass  # identification is optional

    print(
        tr(
            "connect.connected",
            name=display_name,
            address=address,
            protocol="HS03/HiLink",
        )
    )
    while True:
        analog = await read_hs03_command(
            client, notifications, overflowed, HS03_ANALOG_COMMAND
        )
        cells = await read_hs03_command(
            client, notifications, overflowed, HS03_CELLS_COMMAND
        )
        status = await read_hs03_command(
            client, notifications, overflowed, HS03_STATUS_COMMAND
        )
        on_snapshot(decode_hs03(analog, status, cells), model)
        if interval is None:
            return
        await asyncio.sleep(interval)


async def read_battery(
    device: Any,
    name: str,
    interval: float | None,
    *,
    report: bool = False,
    report_dir: str | None = None,
) -> None:
    from bleak import BleakClient

    async with BleakClient(device, timeout=15) as client:
        gatt_name = await read_gatt_name(client)
        display_name = resolve_display_name(gatt_name, name)
        protocol = detect_protocol(client)
        address = device.address
        monitor_log: Path | None = None

        def on_snapshot(snapshot: BatterySnapshot, identity: str) -> None:
            nonlocal monitor_log
            print_snapshot(snapshot, identity)
            if not report:
                return
            try:
                if interval is None:
                    path = save_report(snapshot, identity, address, report_dir)
                    print(tr("report.saved", path=path))
                else:
                    if monitor_log is None:
                        monitor_log = monitor_log_path(identity, address, report_dir)
                        print(tr("report.monitor_log", path=monitor_log))
                    append_monitor_sample(monitor_log, snapshot, identity, address)
            except OSError as exc:
                print(tr("report.save_failed", error=exc), file=sys.stderr)

        if interval is not None:
            print(tr("connect.monitor_mode", interval=interval))

        if protocol == "HS04":
            await read_hs04_battery(
                client, display_name, address, interval, on_snapshot
            )
        elif protocol == "HS03":
            await read_hs03_battery(
                client, display_name, address, interval, on_snapshot
            )
        else:
            raise ProtocolError(tr("connect.unsupported"))


def _prescan_language(argv: list[str]) -> str:
    prescan = argparse.ArgumentParser(add_help=False)
    prescan.add_argument("--lang", default=DEFAULT_LANG)
    known, _ = prescan.parse_known_args(argv)
    return known.lang


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    raw_argv = list(sys.argv[1:] if argv is None else argv)
    set_language(_prescan_language(raw_argv))

    parser = argparse.ArgumentParser(description=tr("cli.description"), add_help=False)
    parser.add_argument(
        "-h",
        "--help",
        action="help",
        default=argparse.SUPPRESS,
        help=tr("cli.help"),
    )
    parser.add_argument(
        "--scan-seconds",
        type=float,
        default=15,
        help=tr("cli.scan_seconds"),
    )
    parser.add_argument(
        "--address",
        help=tr("cli.address"),
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help=tr("cli.all"),
    )
    parser.add_argument(
        "--no-resolve",
        action="store_true",
        help=tr("cli.no_resolve"),
    )
    parser.add_argument(
        "--watch",
        type=float,
        metavar=tr("cli.watch_metavar"),
        help=tr("cli.watch"),
    )
    parser.add_argument(
        "--report",
        action="store_true",
        help=tr("cli.report"),
    )
    parser.add_argument(
        "--report-dir",
        metavar=tr("cli.report_dir_metavar"),
        help=tr("cli.report_dir"),
    )
    parser.add_argument(
        "--list",
        action="store_true",
        help=tr("cli.list"),
    )
    parser.add_argument(
        "--lang",
        choices=sorted(MESSAGES),
        default=DEFAULT_LANG,
        help=tr("cli.lang"),
    )
    args = parser.parse_args(raw_argv)
    set_language(args.lang)
    if args.scan_seconds <= 0 or (args.watch is not None and args.watch <= 0):
        parser.error(tr("cli.invalid_times"))
    return args


async def async_main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        from bleak.exc import BleakError
    except ModuleNotFoundError:
        print(tr("cli.missing_bleak"), file=sys.stderr)
        return 1

    try:
        candidates = await scan_devices(args.scan_seconds, args.all)
        if not candidates:
            print(tr("main.none_found"))
            return 1
        if args.all:
            print(tr("main.all_warning"))
        rows = [
            DeviceRow(device, name, rssi) for device, name, rssi in candidates
        ]

        if args.list:
            for row in rows:
                badge = protocol_badge(row.protocol, row.inspected)
                print(
                    f"{row.name[:38]:38} {row.device.address:17} "
                    f"RSSI {row.rssi:>5} dBm{badge}"
                )
            return 0

        if args.address:
            selected_row = next(
                (
                    row
                    for row in rows
                    if row.device.address.lower() == args.address.lower()
                ),
                None,
            )
            if selected_row is None:
                raise ProtocolError(
                    tr("main.address_not_found", address=args.address)
                )
            await read_battery(
                selected_row.device,
                selected_row.name,
                args.watch if args.watch else None,
                report=args.report,
                report_dir=args.report_dir,
            )
            return 0

        if not _tui_available():
            selected_row, mode = await choose_battery_textual(rows)
            interval = (
                (args.watch or DEFAULT_MONITOR_SECONDS) if mode == "monitor"
                else (args.watch if args.watch else None)
            )
            await read_battery(
                selected_row.device,
                selected_row.name,
                interval,
                report=args.report,
                report_dir=args.report_dir,
            )
            return 0

        while True:
            selected_row, mode = await select_battery_tui(
                rows, resolve=not args.no_resolve
            )
            _clear_screen()
            if mode == "monitor":
                interval: float | None = args.watch or DEFAULT_MONITOR_SECONDS
            else:
                interval = args.watch if args.watch else None
            print(
                tr(
                    "main.selected",
                    name=selected_row.name,
                    address=selected_row.device.address,
                )
            )
            try:
                await read_battery(
                    selected_row.device,
                    selected_row.name,
                    interval,
                    report=args.report or mode == "report",
                    report_dir=args.report_dir,
                )
                return 0
            except (BleakError, OSError, ProtocolError, asyncio.TimeoutError) as exc:
                print(tr("main.error", message=exc), file=sys.stderr)
                answer = await asyncio.to_thread(
                    input, tr("main.back_to_list_prompt")
                )
                if answer.strip().lower() == "q":
                    return 1
                rows = [
                    DeviceRow(device, name, rssi)
                    for device, name, rssi in await scan_devices(
                        args.scan_seconds, args.all
                    )
                ]
                if not rows:
                    print(tr("main.empty_device_list"))
                    return 1
    except (BleakError, OSError, ProtocolError, asyncio.TimeoutError) as exc:
        print(tr("main.error", message=exc), file=sys.stderr)
        return 1


def main() -> int:
    _configure_console_encoding()
    try:
        return asyncio.run(async_main())
    except KeyboardInterrupt:
        print(tr("main.finished"))
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
