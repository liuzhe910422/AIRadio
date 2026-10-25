# Spec Delta

## Purpose

提供一个独立的终端测试客户端，用现有对讲 WebSocket 完成设备注册和加入房间，分步发送语音帧以验证同房间转发，并展示服务端转发的聊天文字。

## ADDED Requirements

### Requirement: 上下分栏界面

客户端 MUST 把终端分为上方消息区和下方命令区。消息区展示状态与收到的文字；命令区接受一行命令。

#### Scenario: 启动后的布局

- **WHEN** 客户端启动
- **THEN** 上方为消息区，下方为命令输入区

### Requirement: 命令语法

客户端 MUST 把一行输入解析为命令名和参数。命令名是第一个空白分隔的词，其余词是参数。`sendtext` 的参数 MUST 是命令名之后去掉首尾空白的整段文本。

#### Scenario: 连接命令的参数

- **WHEN** 用户输入 `connect ws://127.0.0.1:8080/ws device-1 secret`
- **THEN** 命令为 `connect`，参数依次为该 URL、`device-1` 和 `secret`

#### Scenario: 发送文本保留内部空白

- **WHEN** 用户输入 `sendtext 你好 世界`
- **THEN** 命令为 `sendtext`，文本参数为 `你好 世界`

#### Scenario: 未知命令

- **WHEN** 用户输入未实现的命令名
- **THEN** 消息区说明命令未知，且不发送任何 WebSocket 帧

### Requirement: 连接并注册

`connect` MUST 使用两个或三个参数：WebSocket URL、设备 ID，以及可选的对讲 API key。设备 ID 不匹配 `[A-Za-z0-9_-]{1,32}` 时，客户端 MUST 拒绝该命令且不拨号。合法时 MUST 连接该 URL，并发送 `type` 为 `hello`、`version` 为 `1`、`device_id` 为给定值、`device_type` 为 `simulator`、`chat_enabled` 为 `true` 的 JSON。提供第三个参数时，该 JSON MUST 包含 `api_key`。

#### Scenario: 注册成功

- **WHEN** 服务端返回 `ok` 为 true 的 `hello_ack`
- **THEN** 消息区显示注册成功和返回的房间名称，并记录响应中的 `chat_session_id`

#### Scenario: 注册失败

- **WHEN** 服务端返回 `ok` 为 false 的 `hello_ack` 或连接失败
- **THEN** 消息区显示失败原因，且客户端处于未注册状态

#### Scenario: 非法设备 ID

- **WHEN** 设备 ID 含有空格或其他不允许的字符
- **THEN** 客户端不发起连接，并在消息区说明设备 ID 非法

#### Scenario: 重复连接

- **WHEN** 已有连接时再次执行合法的 `connect`
- **THEN** 客户端先关闭旧连接，再注册新的设备身份

### Requirement: 加入房间

已注册后，`join` MUST 发送 `type` 为 `join_room`、`code` 为唯一参数的 JSON。未注册时 MUST 不发送任何帧。

#### Scenario: 加入成功

- **WHEN** 服务端返回 `ok` 为 true 的 `join_room_ack`
- **THEN** 消息区显示房间名称，并把当前视图切换为响应中的 `chat_session_id`

#### Scenario: 加入失败

- **WHEN** 服务端返回 `ok` 为 false 的 `join_room_ack`
- **THEN** 消息区显示 `error`，当前房间视图保持不变

#### Scenario: 未注册时加入

- **WHEN** 尚未注册成功就执行 `join`
- **THEN** 不发送 WebSocket 帧，并提示需要先连接

### Requirement: 发起语音会话

已注册且没有未结束的语音会话时，`sendstart` MUST 发送 `type` 为 `start`、`frame_ms` 为 60、`sample_rate` 为 16000 的 JSON。`message_id` MUST 为 `<device_id>-<递增正整数>`，且长度不超过 64。该命令 MUST 在不等待确认的情况下返回。未注册或已有未结束会话时，客户端 MUST NOT 写入任何帧。

#### Scenario: 发起会话

- **WHEN** 已注册的用户执行 `sendstart`
- **THEN** 客户端发送符合上述字段的 `start`，并在消息区显示所使用的 `message_id`

#### Scenario: 未注册时发起

- **WHEN** 尚未注册成功就执行 `sendstart`
- **THEN** 不发送 WebSocket 帧，并提示需要先连接

#### Scenario: 会话尚未结束

- **WHEN** 上一次 `sendstart` 尚未收到失败确认，也尚未成功执行 `sendend`
- **THEN** 再次执行 `sendstart` 不发送任何帧

#### Scenario: 开始被拒绝

- **WHEN** 服务端返回 `ok` 为 false 的 `start_ack`
- **THEN** 消息区显示失败原因，本地语音会话回到空闲

### Requirement: 发送语音文本

只有当前语音会话的 `start_ack` 为成功时，`sendtext` MUST 把参数的 UTF-8 字节作为 WebSocket 二进制帧发送。单帧 MUST NOT 超过 4096 字节；超出时 MUST 在 UTF-8 字符边界拆成多帧并按顺序发送。空文本、未注册，或尚未收到成功的 `start_ack` 时，客户端 MUST NOT 写入任何帧。

#### Scenario: 短文本作为一帧

- **WHEN** 会话已被接受后执行 `sendtext 集合`，且文本不超过 4096 字节
- **THEN** 客户端发送一帧二进制数据，内容为该文本的 UTF-8 字节，本地帧数变为 1

#### Scenario: 保留内部空白

- **WHEN** 会话已被接受后执行 `sendtext 你好 世界`
- **THEN** 二进制帧的内容为 `你好 世界` 的 UTF-8 字节

#### Scenario: 超长文本拆帧

- **WHEN** 文本的 UTF-8 长度超过 4096 字节
- **THEN** 客户端按字符边界依次发送多帧，每帧不超过 4096 字节，本地帧数增加相应数量

#### Scenario: 空文本

- **WHEN** 用户只输入 `sendtext`
- **THEN** 不发送帧，并提示缺少文本

#### Scenario: 尚未接受会话

- **WHEN** 尚未收到 `ok` 为 true 的 `start_ack` 就执行带文本的 `sendtext`
- **THEN** 不发送任何帧

#### Scenario: 多次发送累加帧数

- **WHEN** 同一次已接受的会话中两次成功执行短文本 `sendtext`
- **THEN** 本地帧数为 2

### Requirement: 结束语音会话

当前语音会话的 `start_ack` 已成功且尚未结束时，`sendend` MUST 发送 `type` 为 `end` 的 JSON，其 `message_id` 为本次会话，`frame_count` 为已成功写出的二进制帧数，可以为 0。写出后本地会话 MUST 结束，从而可以再次 `sendstart`。条件不满足时，客户端 MUST NOT 写入任何帧。

#### Scenario: 按已发帧数结束

- **WHEN** 已成功发送 2 帧后执行 `sendend`
- **THEN** 客户端发送 `frame_count` 为 2 的 `end`，本地会话结束

#### Scenario: 未发送文本即结束

- **WHEN** `start_ack` 成功后、尚未 `sendtext` 就执行 `sendend`
- **THEN** 客户端发送 `frame_count` 为 0 的 `end`

#### Scenario: 没有已接受的会话

- **WHEN** 尚未收到成功的 `start_ack` 就执行 `sendend`
- **THEN** 不发送任何帧

#### Scenario: 结束确认

- **WHEN** 服务端返回 `end_ack`
- **THEN** 消息区显示其中的 `receivers`

### Requirement: 展示转发的语音帧

客户端 MUST 按收到的 `start` 中的 `message_id` 缓冲随后的二进制帧。收到匹配的 `end` 时，MUST 展示 `source_id`、按顺序拼接的文本、实收帧数，以及 `end` 中的 `frame_count`。帧数不一致时仍 MUST 展示已拼接的文本。收到匹配的 `abort` 时 MUST 丢弃缓冲并提示中止。客户端 MUST NOT 发送 `receive_ready` 或 `busy`。

#### Scenario: 两帧拼成一行

- **WHEN** 先收到 `start`，再收到两帧二进制文本，然后收到帧数匹配的 `end`
- **THEN** 消息区显示来源设备 ID 和两帧拼接后的文本，并显示实收帧数与声明帧数均为 2

#### Scenario: 帧数不一致

- **WHEN** 实收帧数与 `end` 的 `frame_count` 不同
- **THEN** 消息区仍显示已拼接的文本，并同时显示实收帧数和声明帧数

#### Scenario: 对端中止

- **WHEN** 缓冲尚未结束时收到同一 `message_id` 的 `abort`
- **THEN** 不展示已缓冲的文本，并提示该会话已中止

#### Scenario: 不回应接收邀请

- **WHEN** 收到服务端发来的 `start`
- **THEN** 客户端不发送 `receive_ready` 或 `busy`

### Requirement: 展示服务端转发的文字

客户端 MUST 在消息区展示 `chat_session_id` 等于当前视图的 `chat_message` 和 `chat_message_update`。展示内容 MUST 包含 `source_id` 和 `text`。同一 `message_id` 的更新 MUST 替换原聊天文本，不得追加重复行。不匹配当前视图的事件 MUST 被忽略。聊天文本与语音帧拼接结果 MUST 分开显示，互不替换。

#### Scenario: 收到新文字

- **WHEN** 收到与当前视图匹配、且 `message_id` 未见过的 `chat_message`
- **THEN** 消息区追加该来源设备 ID 和文本

#### Scenario: 收到文字更新

- **WHEN** 收到同一 `message_id` 且视图匹配的 `chat_message_update`
- **THEN** 该条聊天文本被替换，消息条数不增加

#### Scenario: 旧视图事件

- **WHEN** 事件的 `chat_session_id` 与当前视图不同
- **THEN** 消息区不变化

#### Scenario: 聊天文字不覆盖语音转发

- **WHEN** 已展示一条语音帧拼接结果后又收到 `chat_message`
- **THEN** 语音帧拼接结果仍在，聊天文字另成一条

### Requirement: 视图切换时清空文字

注册成功、加入房间成功，或收到带有新 `chat_session_id` 的 `room_state` 时，客户端 MUST 改用新的视图标识，清空旧聊天文字和未完成的语音帧缓冲，并使本地语音会话回到空闲。

#### Scenario: 房间状态带来新视图

- **WHEN** 已展示文字后收到 `chat_session_id` 不同的 `room_state`
- **THEN** 旧文字被清空，当前视图改为新的 `chat_session_id`

#### Scenario: 切换时丢弃未完成转发

- **WHEN** 语音帧尚未收齐时视图标识发生变化
- **THEN** 未完成的缓冲不再显示，本地语音会话回到空闲

### Requirement: 断开连接

读循环结束或连接关闭时，客户端 MUST 进入未注册状态，清空聊天文字和未完成的语音帧缓冲，结束本地语音会话，并显示连接已断开。

#### Scenario: 服务端关闭连接

- **WHEN** WebSocket 读操作失败
- **THEN** 消息区不再保留断开前的聊天文字和未完成的语音帧，并显示已断开
