# Proposal

## Why

联调对讲服务时，需要分步驱动现有语音会话，把文本放进二进制帧，确认同房间的转发链路。把开始、语音帧和结束绑成一次发送，看不到中间状态。浏览器控制台也不适合按固定命令做多设备手工测试。本次只增加这个客户端，不改服务端。

## What Changes

- 在仓库根目录新增独立 Go 模块 `client/`，不修改 `server/` 与固件。
- 提供上下分栏的终端界面：上方展示连接状态、服务端推送的文字和转发来的语音帧文本，下方是命令输入区。
- 命令形如 `<cmd> [<param> ...]`，实现：
  - `connect <url> <device_id> [api_key]`：建立 WebSocket，并发送 `hello` 完成设备注册。
  - `join <code>`：发送 `join_room`，用四位加入码进入房间。
  - `sendstart`：发送语音会话 `start`。
  - `sendtext <text>`：在会话已被接受后，把文本作为二进制帧发送，可重复执行。
  - `sendend`：用已发出的帧数发送 `end`。
- 上方区域展示服务端主动下发的 `chat_message` 与 `chat_message_update`，以及按 `start`、二进制帧、`end` 拼出的转发文本。收到 `start` 后不回 `receive_ready` 或 `busy`。

## Capabilities

### New Capabilities

- `intercom-test-client`: 独立终端测试客户端。用现有对讲 WebSocket 完成设备注册、加入房间，分步发送语音会话以验证二进制转发，并展示服务端转发的聊天文字。不新增服务端报文。

### Modified Capabilities

- 无。`openspec/specs/` 目前没有已有能力。

## Impact

- 新增 `client/` Go 模块及其依赖（WebSocket 客户端与终端界面库）。
- 只消费现有 `/ws` 行为：`hello` / `hello_ack`、`join_room` / `join_room_ack`、`room_state`、`start` / `start_ack`、二进制帧、`end` / `end_ack`、`abort`、`error`、`chat_message`、`chat_message_update`。
- 服务端协议、管理端 HTTP 登录、固件和房间数据均不变。
