# Design

## Context

见 `proposal.md` 的动机。服务端入站控制帧只处理 `hello`、`set_hosting`、`list_rooms`、`start`、`join_room`、`create_room`、`accept_room_invite`、`leave_room`、`receive_ready`、`busy`、`end`、`abort`。其它 `type`（包括客户端发出的 `chat_message`）返回 `error: unknown message type` 并中止当前会话。

实时语音不解码。发送方先发 `start`，再发二进制帧，最后发 `end`。服务端把二进制帧原样转给同房间、已处于接收状态的其他连接。服务端在转发前已经把接收方标为可接收；超过约 2 秒再回 `receive_ready` 会被当成拒绝。`start` 被接受后约 35 秒内没有匹配的 `end`，服务端会关闭发送方连接。

`chat_message` / `chat_message_update` 只由服务端在语音结束、ASR 完成、托管回复或房间通报时下发。它的 `text` 不是二进制帧的内容。接收条件是该连接 `hello` 带了 `chat_enabled: true`，且 `chat_session_id` 仍是当时固定的视图。

服务端每 20 秒发 Ping。客户端必须持续读连接，才能由 WebSocket 库自动回 Pong。管理端 `POST /api/login` 与设备 WebSocket 无关，本客户端不使用它。

## Goals / Non-Goals

**Goals:**

- 在 `client/` 形成可 `go run .` 的独立模块，用终端分栏完成规格中的 `connect`、`join`、`sendstart`、`sendtext`、`sendend`。
- 把命令解析、语音会话状态和消息列表做成可单测的纯逻辑，界面只负责渲染和按键。

**Non-Goals:**

- 不改 `server/`、固件、协议文档或管理页面。
- 不播放音频，不实现 `create_room`、`leave_room`、`list_rooms` 或托管。
- 不发送 `receive_ready` 或 `busy`，也不把二进制帧当作 Opus 解码。

## Decisions

### 独立模块放在 `client/`

模块路径为 `airadio/intercom/client`，Go 版本与 `server/go.mod` 一致（1.24）。它不 import `server`，避免测试客户端绑住服务端内部类型。

备选是把程序放进 `server/cmd`。那会让测试工具和监听进程共用一个 `package main`，不符合“独立 client 目录”。

### 终端界面使用 Bubble Tea

上方用 viewport 滚动消息，下方用单行 textinput。状态变更经 channel 进入 UI 循环，读循环不直接画屏。

备选是手写 ANSI 分栏。分栏、中文宽度和滚动都要自己处理，而本客户端的交互只有一行命令，不值得自研。

### WebSocket 使用 gorilla/websocket

与服务端相同的 `github.com/gorilla/websocket v1.5.3`。拨号不设置 `Origin`，以符合服务端对无 Origin 设备客户端的放行。读限制设为 64 KiB，避免聊天 JSON 被过小的设备入站限制误伤。

`connect` 的 URL 原样拨号，调用者写完整地址，例如 `ws://127.0.0.1:8080/ws`。

### `hello` 固定为模拟设备并开启聊天

`device_type` 使用 `simulator`，避免测试连接被记成真实设备。`chat_enabled` 固定为 `true`，否则服务端不下发 `chat_message`。只有命令给出第三个参数时才填写 `api_key`。

### 语音会话由三条命令分步驱动

本地状态为：空闲、已发送 `start`、已接受、已结束。`sendstart` 生成 `<device_id>-<递增正整数>` 并立即写控制帧，不等待 `start_ack`。`start_ack` 失败、会话相关的 `error` 或 `abort`、断线，都回到空闲。

`sendtext` 只在已接受后写二进制帧。UTF-8 按 4096 字节上限在字符边界拆分，成功写出的帧才计入 `frame_count`。某帧写出失败时发送 `abort` 并回到空闲，避免服务端把连接留到超时关闭。`sendend` 用当前计数写 `end`，写完即结束本地会话。

收到对端 `start` 后只按 `message_id` 拼接随后的二进制帧，不回 `receive_ready` 或 `busy`。`end` 时把缓冲区当 UTF-8 显示；非法 UTF-8 时显示字节数并说明无法解码。`abort` 丢弃缓冲。

备选是一条命令完成 `start`、帧和 `end`。那样不能在帧与帧之间观察转发，也不便单独发送空会话的 `end`。

### 会话状态与界面分离

会话对象保存连接、是否已注册、当前 `chat_session_id`、本地语音会话，以及两类展示：按 `message_id` 替换的聊天文字，和语音帧拼接结果。界面事件只有：追加状态行、替换展示列表、标记未注册。

视图切换（新的 `hello_ack`、成功的 `join_room_ack`、带新 `chat_session_id` 的 `room_state`）清空聊天文字和未完成的语音帧缓冲，并使本地语音会话回到空闲。仅名称变化且 `chat_session_id` 相同的 `room_state` 不清空。断开时清空这两类内容并追加“已断开”。状态行不属于聊天文字，断开后可以留下这条状态。

### 测试不启动仓库里的服务进程

`go test` 覆盖命令解析、语音会话和接收拼接。WebSocket 行为用 `httptest` 回放 `hello_ack`、`join_room_ack`、`start_ack`、`end_ack`、`start`、二进制帧、`end`、`abort` 和 `chat_message`。不依赖本机 8080 端口，也不链接 `server` 包。

## Risks / Trade-offs

- [聊天文字不是转发结果] → 消息区把语音帧拼接和 `chat_message` 分开。识别失败或文本为空不代表二进制转发失败。
- [`start` 被接受后约 35 秒内必须 `sendend`] → 命令说明和 README 写明这一点。写出失败时客户端发送 `abort`。超时关闭由断线处理清空状态。
- [同房间的真实设备会把文本帧当 Opus 播放] → 本客户端只用于测试客户端之间的转发核对，不在有真实设备的房间里发送。
- [服务端同时只有一路语音] → `start_ack` 为 `busy` 时回到空闲，消息区显示原因，用户可以稍后重试。
- [Bubble Tea 增加依赖] → 依赖只留在 `client/go.mod`，不影响固件和 `server`。
- [服务端 Ping 依赖读循环] → 读循环在连接建立后立即启动，界面退出时关闭连接。

## Migration Plan

新增目录，没有数据迁移。回退时删除 `client/` 即可。已用 `simulator` 注册过的设备 ID 会留在服务端设备表中，那是现有 `hello` 行为，本变更不增加清理接口。

## Open Questions

无。
