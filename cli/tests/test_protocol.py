import asyncio
import contextlib
import io
import string
import tempfile
import unittest
from pathlib import Path
from typing import Any

from humsienk_cli import (
    ANALOG_POINT,
    DEFAULT_LANG,
    HS03_ANALOG_COMMAND,
    HS03_CELLS_COMMAND,
    HS03_HEAD,
    HS03_STATUS_COMMAND,
    MESSAGES,
    MONITOR_COLUMNS,
    PRODUCT_POINT,
    BatterySnapshot,
    DeviceRow,
    ProtocolError,
    _is_humsienk_candidate,
    _safe_name,
    append_monitor_sample,
    build_hs03_command,
    build_read_request,
    crc16_modbus,
    decode_hs03,
    decode_snapshot,
    format_found_line,
    format_registry_address,
    format_scan_status,
    format_snapshot,
    monitor_sample_row,
    no_name,
    parse_args,
    protocol_badge,
    resolve_display_name,
    save_report,
    select_battery_tui,
    set_language,
    tr,
    validate_hs03_response,
    validate_response,
    visible_window,
)


def make_response(point: int, payload: bytes, version: int = 4) -> bytes:
    header = bytes((0x7E, version, 1, 3, 0, point)) + len(payload).to_bytes(2, "big")
    body = header + payload
    return body + crc16_modbus(body).to_bytes(2, "big") + b"\x0d"


def make_hs03_response(command: int, payload: bytes) -> bytes:
    body = bytes((HS03_HEAD, command, len(payload))) + payload
    return body + (sum(body[1:]) & 0xFFFF).to_bytes(2, "little")


def placeholder_arguments(template: str) -> dict[str, Any]:
    """Dummy values for every placeholder of a message template."""
    arguments: dict[str, Any] = {}
    for _, field, spec, _ in string.Formatter().parse(template):
        if not field:
            continue
        if spec.endswith("f"):
            arguments[field] = 1.5
        elif spec.endswith(("d", "g")):
            arguments[field] = 7
        else:
            arguments[field] = "x"
    return arguments


class FakeDevice:
    """Minimal device object for TUI tests (no BLE radio)."""

    def __init__(self, address: str) -> None:
        self.address = address


class ProtocolTests(unittest.TestCase):
    def setUp(self) -> None:
        set_language(DEFAULT_LANG)

    def test_crc16_modbus_standard_check_value(self) -> None:
        self.assertEqual(crc16_modbus(b"123456789"), 0x4B37)

    def test_builds_only_allowlisted_read_requests(self) -> None:
        request = build_read_request(PRODUCT_POINT)
        self.assertEqual(len(request), 11)
        self.assertEqual(request[:8], bytes((0x7E, 0, 1, 3, 0, PRODUCT_POINT, 0, 0)))
        self.assertEqual(request[-1], 0x0D)
        self.assertEqual(len(build_read_request(ANALOG_POINT, extended=True)), 18)
        with self.assertRaises(ValueError):
            build_read_request(0x99)
        with self.assertRaises(ValueError):
            build_read_request(PRODUCT_POINT, extended=True)

    def test_validates_read_response_and_crc(self) -> None:
        frame = make_response(PRODUCT_POINT, b"product-data")
        self.assertEqual(validate_response(frame, PRODUCT_POINT), b"product-data")

        corrupt = bytearray(frame)
        corrupt[8] ^= 0x01
        with self.assertRaisesRegex(ProtocolError, "CRC"):
            validate_response(bytes(corrupt), PRODUCT_POINT)

    def test_rejects_other_protocol_versions(self) -> None:
        frame = make_response(PRODUCT_POINT, b"product-data", version=3)
        with self.assertRaisesRegex(ProtocolError, "v4"):
            validate_response(frame, PRODUCT_POINT)

    def test_decodes_cells_temperatures_and_signed_current(self) -> None:
        payload = bytearray()
        payload.append(2)
        payload.extend((3300).to_bytes(2, "big"))
        payload.extend((3310).to_bytes(2, "big"))
        payload.append(2)
        payload.extend((2980).to_bytes(2, "big"))
        payload.extend((3000).to_bytes(2, "big"))
        payload.extend((0x800A).to_bytes(2, "big"))
        payload.extend((5120).to_bytes(2, "big"))
        payload.extend((1250).to_bytes(2, "big"))
        payload.extend((3000).to_bytes(2, "big"))
        payload.extend((100).to_bytes(2, "big"))
        payload.extend((2800).to_bytes(2, "big"))
        payload.extend((75).to_bytes(2, "big"))

        snapshot = decode_snapshot(bytes(payload))
        self.assertEqual(snapshot.cells_mv, (3300, 3310))
        self.assertAlmostEqual(snapshot.temperatures_c[0], 25.0)
        self.assertAlmostEqual(snapshot.temperatures_c[1], 27.0)
        self.assertEqual(snapshot.current_a, -10)
        self.assertEqual(snapshot.voltage_v, 51.2)
        self.assertEqual(snapshot.soc_percent, 75)
        self.assertEqual(snapshot.cycles, 100)

    def test_rejects_malformed_snapshot(self) -> None:
        with self.assertRaises(ProtocolError):
            decode_snapshot(b"\x00")

    def test_candidate_matching_is_tolerant(self) -> None:
        self.assertTrue(_is_humsienk_candidate("HS04", []))
        self.assertTrue(_is_humsienk_candidate("  hs-24v300a ", []))
        self.assertTrue(_is_humsienk_candidate("Humsienk-BMS", []))
        self.assertTrue(_is_humsienk_candidate("", ["fff0"]))
        self.assertTrue(
            _is_humsienk_candidate(
                "", ["0000FFF0-0000-1000-8000-00805F9B34FB"]
            )
        )
        self.assertFalse(_is_humsienk_candidate("HLK-LD2410_DD4B", []))
        self.assertFalse(
            _is_humsienk_candidate(
                "radar-sensor", ["0000af30-0000-1000-8000-00805f9b34fb"]
            )
        )

    def test_registry_address_formatting(self) -> None:
        self.assertEqual(
            format_registry_address("08f9e0f6b55a"), "08:F9:E0:F6:B5:5A"
        )
        self.assertEqual(
            format_registry_address("08:F9:E0:F6:B5:5A"), "08:F9:E0:F6:B5:5A"
        )
        self.assertEqual(format_registry_address("za-krotki"), "")
        self.assertEqual(format_registry_address("zzzzzzzzzzzz"), "")

    def test_display_name_resolution_skips_blanks(self) -> None:
        self.assertEqual(resolve_display_name("", " HS04 ", "cache"), "HS04")
        self.assertEqual(resolve_display_name("GATT", "adv", "cache"), "GATT")
        self.assertEqual(resolve_display_name("", "   "), no_name())

    def test_builds_hs03_read_commands(self) -> None:
        self.assertEqual(
            build_hs03_command(HS03_ANALOG_COMMAND),
            bytes((0xAA, 0x21, 0x00, 0x21, 0x00)),
        )
        self.assertEqual(
            build_hs03_command(0xF5), bytes((0xAA, 0xF5, 0x00, 0xF5, 0x00))
        )
        self.assertEqual(
            build_hs03_command(0x00), bytes((0xAA, 0x00, 0x00, 0x00, 0x00))
        )

    def test_validates_hs03_response(self) -> None:
        frame = make_hs03_response(HS03_CELLS_COMMAND, b"\x01\x02\x03")
        self.assertEqual(validate_hs03_response(frame, HS03_CELLS_COMMAND), b"\x01\x02\x03")

        with self.assertRaises(ProtocolError) as wrong_command:
            validate_hs03_response(frame, HS03_STATUS_COMMAND)
        self.assertEqual(str(wrong_command.exception), tr("error.hs03_wrong_command"))

        set_language("pl")
        with self.assertRaises(ProtocolError) as wrong_command_pl:
            validate_hs03_response(frame, HS03_STATUS_COMMAND)
        self.assertEqual(
            str(wrong_command_pl.exception), MESSAGES["pl"]["error.hs03_wrong_command"]
        )
        set_language("en")

        corrupt = bytearray(frame)
        corrupt[3] ^= 0xFF
        with self.assertRaises(ProtocolError) as bad_checksum:
            validate_hs03_response(bytes(corrupt), HS03_CELLS_COMMAND)
        self.assertEqual(str(bad_checksum.exception), tr("error.hs03_bad_checksum"))

    def test_decodes_hs03_snapshot(self) -> None:
        analog = (
            (51200).to_bytes(4, "little")
            + (-12500).to_bytes(4, "little", signed=True)
            + bytes((87, 99))
            + (260000).to_bytes(4, "little")
            + (280000).to_bytes(4, "little")
            + (42).to_bytes(2, "little")
            + bytes((25, 24, 26, 0xF6, 27, 28))
        )
        # The BMS sends 24 slots; only the first 4 carry real cells.
        cells = b"".join((3300 + index).to_bytes(2, "little") for index in range(4))
        cells += b"\x00\x00" * 20
        status = bytes((0, 0, 0, 0, 0x80, 0x00, 0x80, 0x00))

        snapshot = decode_hs03(analog, status, cells)
        self.assertEqual(snapshot.protocol, "HS03/HiLink")
        self.assertAlmostEqual(snapshot.voltage_v, 51.2)
        self.assertAlmostEqual(snapshot.current_a, -12.5)
        self.assertEqual(snapshot.soc_percent, 87)
        self.assertEqual(snapshot.soh_percent, 99)
        self.assertAlmostEqual(snapshot.remaining_capacity_ah, 260.0)
        self.assertIsNone(snapshot.total_capacity_ah)
        self.assertEqual(snapshot.design_capacity_ah, 280)
        self.assertEqual(snapshot.cycles, 42)
        self.assertEqual(snapshot.cells_mv, (3300, 3301, 3302, 3303))
        self.assertAlmostEqual(snapshot.temperatures_c[3], -10.0)
        self.assertTrue(snapshot.charging_mosfet)
        self.assertTrue(snapshot.discharging_mosfet)
        self.assertFalse(snapshot.balancer_active)
        self.assertEqual(snapshot.problem_code, 0)

    def test_rejects_bad_hs03_payloads(self) -> None:
        with self.assertRaises(ProtocolError):
            decode_hs03(b"\x00" * 10, b"\x00" * 8, b"\x00" * 2)
        with self.assertRaises(ProtocolError):
            decode_hs03(b"\x00" * 26, b"\x00" * 8, b"")
        with self.assertRaises(ProtocolError):
            decode_hs03(b"\x00" * 26, b"\x00" * 8, b"\x00\x00" * 24)

    def test_visible_window_scrolls_around_selection(self) -> None:
        self.assertEqual(visible_window(5, 2, 10), (0, 5))
        self.assertEqual(visible_window(50, 0, 10), (0, 10))
        self.assertEqual(visible_window(50, 25, 10), (20, 30))
        self.assertEqual(visible_window(50, 49, 10), (40, 50))
        self.assertEqual(visible_window(0, 0, 10), (0, 0))

    def test_protocol_badge_labels_devices(self) -> None:
        self.assertEqual(protocol_badge("", False), "")
        self.assertEqual(protocol_badge("HS03", True), "  [HS03]")
        self.assertEqual(protocol_badge("HS04", True), "  [HS04]")
        self.assertEqual(protocol_badge("", True), tr("badge.other"))

    def test_tui_arrow_keys_select_row(self) -> None:
        import humsienk_cli

        def fake_keys(keys, loop, stop):
            for key in ("down", "down", "up", "enter"):
                loop.call_soon_threadsafe(keys.put_nowait, key)

        rows = [
            DeviceRow(FakeDevice(f"AA:BB:CC:DD:EE:{index:02X}"), f"urzadzenie {index}", -50 - index)
            for index in range(3)
        ]
        original = humsienk_cli._read_keys_thread
        humsienk_cli._read_keys_thread = fake_keys
        try:
            selected, mode = asyncio.run(select_battery_tui(rows, resolve=False))
        finally:
            humsienk_cli._read_keys_thread = original
        self.assertEqual(selected.name, "urzadzenie 1")
        self.assertEqual(mode, "read")

    def test_tui_monitor_and_report_keys(self) -> None:
        import humsienk_cli

        rows = [DeviceRow(FakeDevice("AA:BB:CC:DD:EE:00"), "urzadzenie", -60)]
        original = humsienk_cli._read_keys_thread
        try:
            for key, expected_mode in (("m", "monitor"), ("s", "report"), ("enter", "read")):
                def fake_keys(keys, loop, stop, key=key):
                    loop.call_soon_threadsafe(keys.put_nowait, key)

                humsienk_cli._read_keys_thread = fake_keys
                _, mode = asyncio.run(select_battery_tui(rows, resolve=False))
                self.assertEqual(mode, expected_mode)
        finally:
            humsienk_cli._read_keys_thread = original

    def test_report_and_monitor_files(self) -> None:
        snapshot = BatterySnapshot(
            protocol="HS03/HiLink",
            voltage_v=13.45,
            current_a=10.0,
            soc_percent=63,
            remaining_capacity_ah=199.2,
            total_capacity_ah=None,
            cycles=0,
            design_capacity_ah=314.0,
            cells_mv=(3364, 3365),
            temperatures_c=(16.0, 26.0),
        )
        with tempfile.TemporaryDirectory() as directory:
            report = save_report(snapshot, "HS0309", "AA:BB:CC:DD:EE:FF", directory)
            content = report.read_text(encoding="utf-8")
            self.assertIn(tr("report.title"), content)
            self.assertIn(tr("report.device", name="HS0309"), content)
            self.assertIn(tr("status.protocol", protocol="HS03/HiLink"), content)
            self.assertIn("13.45", content)

            log = Path(directory) / "monitor.csv"
            append_monitor_sample(log, snapshot, "HS0309", "AA:BB:CC:DD:EE:FF")
            append_monitor_sample(log, snapshot, "HS0309", "AA:BB:CC:DD:EE:FF")
            rows = [
                line.split(",")
                for line in log.read_text(encoding="utf-8").strip().splitlines()
            ]
            self.assertEqual(rows[0], list(MONITOR_COLUMNS))
            self.assertEqual(len(rows), 3)
            self.assertEqual(rows[1][3], "HS03/HiLink")
            self.assertEqual(rows[1][4], "63")

    def test_monitor_sample_row_matches_columns(self) -> None:
        snapshot = BatterySnapshot(
            protocol="HS04/WATT",
            voltage_v=51.2,
            current_a=-12.5,
            soc_percent=87,
            remaining_capacity_ah=260.0,
            total_capacity_ah=300.0,
            cycles=42,
            design_capacity_ah=280.0,
            cells_mv=(3300, 3310),
            temperatures_c=(25.0, 27.0),
            soh_percent=99,
            problem_code=0,
        )
        row = monitor_sample_row(snapshot, "Bateria", "AA:BB:CC:DD:EE:01")
        self.assertEqual(len(row), len(MONITOR_COLUMNS))
        self.assertEqual(row[3], "HS04/WATT")
        self.assertEqual(row[5], "99")
        self.assertEqual(row[11], "3.300")

    def test_safe_name_strips_unsafe_characters(self) -> None:
        self.assertEqual(_safe_name("HS03/BMC:04"), "HS03_BMC_04")
        self.assertEqual(_safe_name("   "), "urzadzenie")
        self.assertEqual(_safe_name(""), "urzadzenie")

    def test_scan_progress_formatting(self) -> None:
        status = format_scan_status(12.4, 3)
        self.assertEqual(
            status, MESSAGES["en"]["scan.status"].format(remaining=13, found=3)
        )

        set_language("pl")
        self.assertEqual(
            format_scan_status(12.4, 3),
            MESSAGES["pl"]["scan.status"].format(remaining=13, found=3),
        )
        set_language("en")

        line = format_found_line("HS030902AB26170219", "4E:DF:C5:14:DD:67", -93)
        self.assertIn("HS030902AB26170219", line)
        self.assertIn("4E:DF:C5:14:DD:67", line)
        self.assertIn("-93 dBm", line)

    def test_message_catalogues_share_keys_and_placeholders(self) -> None:
        self.assertIn(DEFAULT_LANG, MESSAGES)
        self.assertEqual(set(MESSAGES["en"]), set(MESSAGES["pl"]))

        for key in MESSAGES["en"]:
            arguments = placeholder_arguments(MESSAGES["en"][key])
            self.assertEqual(
                set(arguments),
                set(placeholder_arguments(MESSAGES["pl"][key])),
                msg=f"placeholder mismatch for {key}",
            )
            for lang in ("en", "pl"):
                set_language(lang)
                rendered = tr(key, **arguments)
                self.assertTrue(rendered, msg=f"empty {lang} message for {key}")
                self.assertNotIn(
                    "{", rendered, msg=f"unfilled placeholder in {lang} {key}"
                )

    def test_translation_formats_placeholders_for_both_languages(self) -> None:
        self.assertEqual(tr("missing.key"), "missing.key")

        set_language("en")
        english = tr("status.voltage", voltage=13.45)
        self.assertEqual(
            english, MESSAGES["en"]["status.voltage"].format(voltage=13.45)
        )
        self.assertIn("13.45", english)

        set_language("pl")
        polish = tr("status.voltage", voltage=13.45)
        self.assertEqual(
            polish, MESSAGES["pl"]["status.voltage"].format(voltage=13.45)
        )
        self.assertIn("13.45", polish)
        self.assertNotEqual(english, polish)

        status_line = tr("status.current", current=-12.5, state=tr("state.discharging"))
        self.assertIn("-12.5", status_line)
        self.assertIn(MESSAGES["pl"]["state.discharging"], status_line)

    def test_cli_language_flag_selects_output_language(self) -> None:
        try:
            self.assertEqual(parse_args([]).lang, DEFAULT_LANG)
            self.assertEqual(
                format_scan_status(4.2, 1),
                MESSAGES["en"]["scan.status"].format(remaining=5, found=1),
            )

            self.assertEqual(parse_args(["--lang", "pl"]).lang, "pl")
            self.assertEqual(
                format_scan_status(4.2, 1),
                MESSAGES["pl"]["scan.status"].format(remaining=5, found=1),
            )

            self.assertEqual(parse_args(["--lang", "en"]).lang, "en")
            self.assertEqual(
                format_scan_status(4.2, 1),
                MESSAGES["en"]["scan.status"].format(remaining=5, found=1),
            )
        finally:
            set_language(DEFAULT_LANG)

    def test_help_documents_language_flag(self) -> None:
        output = io.StringIO()
        with contextlib.redirect_stdout(output), self.assertRaises(SystemExit):
            parse_args(["--help"])
        text = output.getvalue()
        self.assertIn("--lang", text)
        self.assertIn("en,pl", text)

    def test_snapshot_labels_follow_language(self) -> None:
        snapshot = BatterySnapshot(
            protocol="HS04/WATT",
            voltage_v=51.2,
            current_a=-12.5,
            soc_percent=87,
            remaining_capacity_ah=260.0,
            total_capacity_ah=300.0,
            cycles=42,
            design_capacity_ah=280.0,
            cells_mv=(3300, 3310),
            temperatures_c=(25.0, 27.0),
        )
        set_language("en")
        english = "\n".join(format_snapshot(snapshot, "HS0309"))
        self.assertIn(tr("status.protocol", protocol="HS04/WATT"), english)
        self.assertIn(tr("state.discharging"), english)
        self.assertIn(tr("status.sensor", index=1, temperature=25.0), english)

        set_language("pl")
        polish = "\n".join(format_snapshot(snapshot, "HS0309"))
        self.assertIn(tr("status.protocol", protocol="HS04/WATT"), polish)
        self.assertNotEqual(english, polish)

    def test_tui_quit_key_exits(self) -> None:
        import humsienk_cli

        def fake_keys(keys, loop, stop):
            loop.call_soon_threadsafe(keys.put_nowait, "q")

        rows = [DeviceRow(FakeDevice("AA:BB:CC:DD:EE:00"), "urzadzenie", -60)]
        original = humsienk_cli._read_keys_thread
        humsienk_cli._read_keys_thread = fake_keys
        try:
            with self.assertRaises(SystemExit):
                asyncio.run(select_battery_tui(rows, resolve=False))
        finally:
            humsienk_cli._read_keys_thread = original

if __name__ == "__main__":
    unittest.main()
