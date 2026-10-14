# Supported BLE protocols

All three families are read-only: the application only sends the read requests
listed below and validates every reply before it is decoded.

## HS03 / HiLink

Used by Humsienk packs and many rebranded 4S–8S batteries (for example
`HS030902AB26170219`, model `BMC-04S001`).

| Item | Value |
|---|---|
| Service | `0x0001` |
| Write (host → BMS) | `0x0002` |
| Notify (BMS → host) | `0x0003` |

Command frame: `AA <cmd> 00 <cmd> 00`

Reply frame: `AA <cmd> <len> <payload…> <checksum LE16>`, where
`checksum = sum(frame[1 : len-2]) & 0xFFFF` and the total length is `len + 5`.

| Command | Meaning | Payload |
|---|---|---|
| `0x00` | init | – |
| `0x11` | model | ASCII model name |
| `0x20` | status | flags: charging `payload[4]&0x80`, balancer `payload[5]&0x80`, discharging `payload[6]&0x80`, problem bits `u32LE(payload[4:8]) & 0xFF7F7F7F` |
| `0x21` | analog | voltage `u32LE/1000` V, current `i32LE/1000` A, SOC `u8`, SOH `u8`, remaining `u32LE/1000` Ah, design `u32LE/1000` Ah, cycles `u16LE`, six `i8` temperatures |
| `0x22` | cells | 24 × `u16LE` millivolts, zero slots are filtered out |

## HS04 / WATT

The newer Humsienk generation. Requests need the HiLink authentication first.

| Item | Value |
|---|---|
| Service | `0xFFF0` |
| Notify | `0xFFF1` |
| Write | `0xFFF2` |
| Authentication | `0xFFFA`: write `HiLink`, then read one byte that must be `0x01` |

Request: `7E <ext> 01 03 00 <point> 00 00` (+ 7 filling bytes when `ext` is set),
followed by `CRC16-Modbus` (big endian) and `0x0D`.

Reply: `7E … 0D`, protocol version must be 4, length is `u16BE(frame[6:8]) + 11`.

Points used: `0x92` product/firmware, `0x1E` board list, `0x8C` analog values.

## JBD / Xiaoxiang

Overkill Solar and JBD boards, including many rebranded batteries.

| Item | Value |
|---|---|
| Service | `0xFF00` |
| Notify ("RX") | `0xFF01` |
| Write ("TX") | `0xFF02` |

```
request:  0xDD 0xA5 <register> <length> [data…] crc_hi crc_lo 0x77
response: 0xDD <register> <status> <length> [data…] crc_hi crc_lo 0x77
```

* `0xA5` = read, `0x5A` = write (the application never writes).
* Reply length is `length + 7`, `status` is `0x00` (ok) or `0x80` (refused).
* Checksum: 16-bit two's complement of the byte sum starting at the third frame
  byte (`frame[2:len-3]`), stored big endian. Verified against real captures of
  both directions — for example `DD A5 03 00 FF FD 77` and the cell reply
  `DD 04 00 08 0F 46 0F 3E 0F 38 0F 3E FE C2 77`.

| Register | Meaning | Payload layout (offsets in the payload) |
|---|---|---|
| `0x03` | basic info | voltage `u16BE/100` V, current `i16BE/100` A (positive = charging), remaining `u16BE/100` Ah, design `u16BE/100` Ah, cycles `u16BE`, production date `u16BE`, balance words `0x0C`/`0x0E`, protection bitmask `u16BE` at `0x10` (13 flags), software version `u8` at `0x12`, SOC `u8` at `0x13`, FET status `u8` at `0x14` (bit 0 charge, bit 1 discharge, set = conducting), cell count `u8` at `0x15`, NTC count `u8` at `0x16`, then `u16BE` temperatures in 0.1 K (`°C = (raw - 2731) / 10`) |
| `0x04` | cell voltages | `N` × `u16BE` millivolts, `N = length / 2`, at most 32 cells |
| `0x05` | device name | ASCII model string (may be absent on some units) |

Newer firmware requires an `FF AA` unlock handshake before it answers `DD A5`
frames. Such devices are detected but reading data fails with a protocol error;
the unlock exchange is not implemented yet.

## Adding another family

1. Add the framing and decoding to a new `protocol_<name>.go` file, with a
   validation function that rejects damaged or foreign frames.
2. Describe the family in `identify.go`: service UUID, required characteristics
   and, when the service alone is not conclusive, a `confirm` function that
   performs a read and validates the reply.
3. Add a branch to `runConnected` in `session.go` that polls the data and fills
   the shared `Sample` struct.
4. Add unit tests with real captured frames (see `protocol_jbd_test.go`) and a
   language entry for the protocol badge if it should be highlighted.
