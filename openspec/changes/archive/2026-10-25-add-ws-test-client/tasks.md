# Tasks

## 1. 独立模块

- [x] 1.1 在 `client/` 建立模块 `airadio/intercom/client`（Go 1.24），加入 `github.com/gorilla/websocket v1.5.3` 与 Bubble Tea、bubbles。验证 `go test ./...` 能在空测试下通过，且 `go.mod` 不引用 `server`。

## 2. 命令解析

- [x] 2.1 实现一行命令解析：空白分隔命令名，`sendtext` 取其余整段文本。验证单测覆盖 `connect` 的两个和三个参数、`sendstart`、`sendtext 你好 世界`、`sendend`、未知命令和空 `sendtext`。

## 3. 会话与消息列表

- [x] 3.1 实现会话状态：`connect` 校验设备 ID，拨号后发送 `hello`（`version` 1、`device_type` `simulator`、`chat_enabled` true，可选 `api_key`），重复连接先关闭旧连接。用 `httptest` 假服务端验证成功、失败、非法 ID，并断言写出的 JSON。
- [x] 3.2 实现 `join`：已注册才发送 `join_room`；成功切换 `chat_session_id` 并清空文字，失败保持原视图。用假服务端验证这三种结果，以及未注册时写出字节数为 0。
- [x] 3.3 实现聊天文字列表：匹配当前视图的 `chat_message` 追加 `source_id` 与 `text`，同 `message_id` 的 `chat_message_update` 原位替换，旧 `chat_session_id` 忽略；带新 `chat_session_id` 的 `room_state` 清空聊天文字和未完成的语音帧缓冲。用表驱动测试验证条数、文本，以及聊天文字不替换语音帧拼接结果。
- [x] 3.4 实现 `sendstart`、`sendtext`、`sendend`：已注册才发送 `start`（`frame_ms` 60、`sample_rate` 16000、`message_id` 为 `<device_id>-<递增正整数>`）；`start_ack` 成功后 `sendtext` 才写二进制帧，超过 4096 字节时按 UTF-8 字符边界拆帧；`sendend` 用成功写出的帧数发送 `end`，写完结束本地会话。未注册、重复 `sendstart`、空 `sendtext`、尚未接受时的 `sendtext` 或 `sendend` 写出字节数为 0。`start_ack` 失败回到空闲。用假服务端断言 JSON、帧内容和帧数。
- [x] 3.5 实现语音帧接收：按 `start` 的 `message_id` 拼接二进制帧，`end` 时展示来源、文本、实收帧数和声明帧数；帧数不一致仍展示文本；`abort` 丢弃缓冲。断言收到 `start` 后不写出 `receive_ready` 或 `busy`。读失败后进入未注册，清空聊天文字和未完成的语音帧，只保留断开状态。

## 4. 终端界面

- [x] 4.1 用 Bubble Tea 画上方 viewport 和下方单行输入。回车把命令交给会话，会话事件只经 channel 更新界面。验证 `go test ./...` 仍通过，并且 `go run .` 能启动到等待输入（人工看上下分栏即可）。
- [x] 4.2 在 `client/README.md` 写明 `connect`、`join`、`sendstart`、`sendtext`、`sendend`、示例 URL，以及 `start` 被接受后需及时 `sendend`。核对 README 中的 `go run .` 与模块路径一致。
