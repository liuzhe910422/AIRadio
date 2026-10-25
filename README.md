# Lingxi AIRadio: AI Intercom for Industry Operations

([English](README.md) | [中文](README_zh.md) | [日本語](README_ja.md))

<p align="center"><img src="homeweb/assets/airadio-architecture.svg" alt="Lingxi AIRadio system architecture" width="900"></p>

Lingxi AIRadio is an industry intercom solution built on XiaoZhi voice interaction and ESP32 firmware. It is designed for fleets, logistics parks, warehouses, and temporary field teams. AIRadio combines real-time half-duplex voice, voice-controlled rooms, online AI hosting, and short-term conversation recall in one workflow.

> **One tap to talk. One sentence to operate. An AI teammate when work gets busy.**

## The problem AIRadio solves

Drivers, warehouse operators, and field workers often have both hands occupied. A conventional radio is fast but still requires separate devices and manual room management. A voice assistant is flexible, but is not the right trigger for an urgent, immediate transmission.

AIRadio gives each action the right interface: **a large button for instant speech, voice for multi-step operations, and AI assistance when a person cannot respond immediately.** Teams can talk quickly, switch groups with less friction, and revisit what was just said.

## Key capabilities

- **One-tap real-time intercom**: a large 480×320 touch-screen control starts and ends a transmission. A dedicated WebSocket carries Opus frames to devices in the same room.
- **Voice-controlled collaboration rooms**: XiaoZhi MCP tools can list, create, join, leave, and invite devices to rooms, and enable or disable hosting. A room list can be confirmed on the display.
- **Online AI hosting**: after a human message is transcribed, a hosted device uses recent room context to decide whether to answer. If needed, TTS broadcasts the response. Human speech has priority and can interrupt an automated reply.
- **Conversation recall**: ASR produces short-term transcripts with speaker and duration, and messages can be replayed. Users can ask the assistant to summarize the currently retained conversation.
- **Clear field-ready status**: manual mode uses a blue button and hosting mode uses yellow, while room, speaker, and system events remain visible.
- **Console and simulator**: the Go service includes device and room management, a browser Opus simulator, and ASR, TTS, and hosting-LLM configuration and test APIs.

Product page: [Open the AIRadio home page](http://154.94.238.56/).

## Project video

- Direct download: [http://154.94.238.56/video/ready.mp4](http://154.94.238.56/video/ready.mp4)
- The video file is relatively large. Download it before watching for a smoother experience.
- Baidu Netdisk backup: [10月25日(1).mp4](https://pan.baidu.com/s/1T_n8MzPlYifnVEFZb5InGw)
- Extraction code: `hty9`

> The demo site currently uses HTTP. Some browsers may show a privacy or security warning. After confirming the address is `154.94.238.56`, choose the browser option to continue, proceed, or allow the page to open.

## Architecture

<p align="center"><img src="homeweb/assets/airadio-architecture.svg" alt="AIRadio architecture" width="900"></p>

The system has four layers. One important separation is that **AIRadio devices connect directly to the Lingxi voice channel for voice interaction and MCP operations; the Intercom Service handles room audio, membership, and records between devices.**

The system has four layers:

1. **AIRadio devices**: `main/intercom/` manages device identity, rooms, audio coordination, temporary records, and MCP tools. The current product target is Waveshare ESP32-S3 Touch LCD 3.5B with a 480×320 touch UI.
2. **Intercom Service**: `server/` is a Go service for WebSocket audio routing, room and member management, the HTTP console, and optional SQLite persistence. The intercom connection is independent of the XiaoZhi assistant session.
3. **AI services**: ASR transcribes completed speech, TTS plays system announcements and hosted replies, and the Hosting LLM decides whether a hosted device should answer. Bounded asynchronous queues keep AI work away from the real-time audio path.
4. **Lingxi voice channel**: AIRadio devices connect directly to the Lingxi (XiaoZhi) voice channel for natural-language understanding and MCP tool calls. Physical speech remains a large device-button action, so voice operations and intercom can coexist.

The audio path is `microphone → 16 kHz mono Opus → WebSocket → same-room device → jitter buffer → decoded playback`. After a session ends, the server submits it to ASR. Eligible hosted devices then run an LLM decision and TTS response.

## Current scope

The main target is Waveshare ESP32-S3 Touch LCD 3.5B. Devices retain up to 20 recent records, while temporary audio and text are kept in server memory. Device account binding, multi-organization roles, offline replay, permanent history, and cross-instance routing are not implemented yet. Device and replay endpoints are intended for trusted integration environments. Hosting requires ASR, TTS, and the Hosting LLM to be enabled and the device to remain online.

## Quick start

### Run the intercom service

```sh
cd server
go run . -listen 127.0.0.1:9200 -db ./data/intercom.db
```

The console is served by the same process. Configure ASR, TTS, and Hosting LLM after login; third-party API keys remain server-side.

### Build firmware

ESP-IDF v6.0.1 or newer is required. The `release/v6.1` branch is recommended; ESP-IDF 5.x is unsupported.

```sh
python3 scripts/build.py --list-boards
python3 scripts/build.py waveshare/esp32-s3-touch-lcd-3.5b --name default
```

Use the exact board name printed by `--list-boards`. After flashing, configure the independent `intercom.device_id` and intercom service URL in Wi-Fi advanced settings.

## Documentation

- [Architecture overview](design/intercom-overview.md)
- [Intercom protocol](design/intercom-protocol.md)
- [ASR and session records](design/intercom-asr-session-chat.md)
- [AI hosting mode](design/intercom-hosting.md)
- [MCP implementation](design/intercom-mcp-implementation.md)
- [480×320 UI](design/intercom-ui-480x320.md)

## License

This project is open source under the MIT License. The AIRadio service, device firmware, and product home page continue to evolve together in this repository.
