# 灵矽对讲服务协议

状态：当前服务端实现协议，包含房间、浏览器模拟器、会话记录、托管、设备语言及动态控制台用户；核对日期 2026-10-31。

本文以 `server/api.go`、`server/main.go` 和内置 Web 页面为准，集中描述 HTTP 和 WebSocket 接口。服务默认使用明文 HTTP/WebSocket，仅适合可信内网测试。

## 1. 通用约定

- HTTP 基础地址由启动参数 `-listen` 决定，例如 `http://127.0.0.1:9200`。
- WebSocket 地址为 `ws://host:port/ws`；HTTPS 页面对应 `wss://`。
- 管理 HTTP 接口需要 Cookie `airadio_admin`。登录成功后服务端设置该 Cookie。每次登录创建独立随机会话，有效期12小时，最多128个会话；达到上限返回429。Cookie 使用 Path=/、HttpOnly、SameSite=Strict，直接 HTTPS 请求下启用 Secure。
- JSON 使用 UTF-8。成功响应通常为 JSON；失败响应为 HTTP 状态码和纯文本错误正文。
- `device_id` 必须匹配 `[A-Za-z0-9_-]{1,32}`，且在线连接不能重复。
- 普通房间加入码为 `1000`～`9999` 的四位数字字符串；默认房间没有加入码。
- 默认房间 ID 为 `default`，名称为“默认房间”，不能关闭。

## 2. HTTP 接口

### 2.1 `POST /api/login`

登录管理端。

请求 JSON：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| `username` | string | 是 | 初始账号为 `admin`，支持用户管理中新建的账号 |
| `password` | string | 是 | 对应账号密码；初始为 `admin123` |

成功：`200`，并设置随机值的 `airadio_admin` Cookie。

```json
{"ok":true}
```

失败：`401`，正文 `invalid credentials`。

### 2.2 `GET /api/rooms`

返回所有未关闭房间。需要登录。

响应：房间对象数组。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` / `ID` | string | 稳定房间 ID |
| `name` | string | 房间名称 |
| `join_code` / `JoinCode` | string | 普通房间四位数字加入码（1000～9999，不使用前导零）；默认房间为空 |
| `is_default` / `Default` | boolean | 是否默认房间 |
| `device_ids` / `Devices` | string[] | 存储维护的实际成员；默认房间成员请通过设备接口按 room_id 查询 |
| `invite_device_ids` | string[] | 邀请名单，空表示开放加入 |
| `created_by_device_id` | string | 创建者设备 ID |

### 2.3 `POST /api/rooms`

由管理端创建房间。需要登录。

请求 JSON：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| `name` | string | 是 | 去除首尾空格后不能为空 |
| `created_by_device_id` | string | 否 | 创建者标识，记录到房间 |
| `device_ids` | string[] | 否 | 邀请设备列表；为空表示开放加入，不自动移动受邀设备 |

成功：`200`，返回单个房间对象（字段同 `GET /api/rooms`）。普通房间会生成固定四位加入码（1000～9999）。指定的创建者自动加入；邀请不自动切换受邀设备。HTTP 创建当前读取 device_ids；WebSocket 创建优先读取 invite_device_ids。

失败：`400`，正文为 `name is required` 等错误。

### 2.4 `GET /api/rooms/{room_id}/devices`

返回房间设备列表。需要登录。

设备对象字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `device_id` | string | 设备 ID |
| `last_seen_at` | string | 最近注册时间，RFC3339 |
| `last_ip` | string | 最近 hello 的 RemoteAddr，可能含端口；经过代理时可能为代理地址 |
| `online` | boolean | 当前是否存在在线 WebSocket |
| `room_id` | string | 当前逻辑房间 ID |
| `language` | string | 设备界面语言：`zh-CN` 或 `en-US` |

### 2.5 `POST /api/rooms/{room_id}/close`

关闭普通房间。需要登录。关闭后该房间设备回到默认房间。

成功：

```json
{"ok":true}
```

默认房间或不存在房间：`400`。

### 2.6 `GET /api/devices`

返回已保存设备，包含管理端创建但尚未连接的离线模拟设备。需要登录。响应为设备对象数组，字段同上。

### 2.7 静态页面

- `GET /` 或 `GET /index.html`：返回内置管理页面。
- 其它路径：`404`。

## 3. WebSocket 连接与限制

连接到 `/ws` 后，客户端第一条消息必须是 JSON `hello`。服务端限制：

- 控制帧最大 1024 字节。
- 图片内容不放入控制帧；图片通过 HTTP 上传和下载，控制帧只传图片 URL 与元数据。
- 单个二进制音频帧最大 4096 字节。
- 单段最多 500 帧、512 KiB、35 秒。
- 最多 128 个连接。
- 每个连接最多一段进行中的发送会话。
- 浏览器同源 `Origin` 和无 `Origin` 的设备客户端允许；其它跨站 Origin 拒绝。

### 3.1 `hello` 注册

客户端 -> 服务端：

```json
{"type":"hello","version":1,"device_id":"003"}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `type` | string | 固定 `hello` |
| `version` | integer | 当前必须为 `1` |
| `device_id` | string | 设备 ID，必须唯一且符合 ID 规则 |

成功返回：

```json
{"type":"hello_ack","ok":true,"room_id":"default","room_name":"默认房间","language":"zh-CN"}
```

失败返回 `ok:false` 和 `error`，例如重复 ID、非法 ID、版本不支持或连接达到上限；连接随后关闭。

### 3.2 控制帧公共字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `type` | string | 消息类型 |
| `version` | integer | hello 使用 |
| `device_id` | string | hello 使用 |
| `source_id` | string | 服务端填入的发言设备 ID，仅接收 start 使用 |
| `message_id` | string | 一段语音的会话 ID |
| `frame_ms` | integer | start 的帧时长，当前必须 60 |
| `sample_rate` | integer | start 的采样率，当前必须 16000 |
| `frame_count` | integer | end 的已发送帧数 |
| `ok` | boolean | ack 成功或失败 |
| `error` | string | 失败原因 |
| `receivers` | integer | end_ack 实际确认接收者数 |
| `room_id` | string | 房间 ID |
| `room_name` | string | 房间名称 |
| `language` | string | 设备界面语言：`zh-CN` 或 `en-US` |
| `code` | string | 房间四位加入码（1000～9999） |
| `name` | string | 创建房间名称 |
| `device_ids` | string[] | 创建房间时的成员列表 |
| `image_url` | string | 图片消息的 HTTP(S) 路径或 URL |
| `text` | string | 图片消息提示文本 |

未列出的字段会被忽略或按消息类型处理；未知 `type` 会返回 error 并中止当前会话。

## 4. 房间控制消息

### 4.0 设备语言

设备可通过已注册的 WebSocket 请求切换自身界面语言：

```json
{"type":"set_language","language":"en-US"}
```

当前只接受 `zh-CN` 和 `en-US`。服务端按连接身份保存语言并返回：

```json
{"type":"language_state","ok":true,"language":"en-US"}
```

失败时返回 `ok:false,error`。语言也会在 `hello_ack` 的 `language` 字段中返回。语言状态保存在设备表，不写入设备 NVS。

### 4.1 `create_room`

客户端 -> 服务端：

```json
{"type":"create_room","name":"房间002","invite_device_ids":["004"]}
```

创建者取已注册 WebSocket 的身份，不信任请求中的 creator_device_id。推荐传 invite_device_ids；兼容旧字段 device_ids 作为邀请名单。只有创建者自动加入，受邀设备须自行接受；空名单表示开放加入。

成功：

```json
{"type":"create_room_ack","ok":true,"room_id":"room-...","room_name":"房间002","code":"1234","device_ids":["003"],"invite_device_ids":["004"]}
```

创建者自动进入新房间。服务端会向受影响的在线设备发送 `room_state`。

### 4.2 `join_room`

客户端 -> 服务端：

```json
{"type":"join_room","code":"1234"}
```

`code` 为四位加入码（1000～9999）。正在发言时切换会返回 `ok:false,error:"busy"`。

成功：

```json
{"type":"join_room_ack","ok":true,"room_id":"room-...","room_name":"房间002"}
```

失败：`ok:false,error:"invalid room code"` 等。

### 4.3 `leave_room`

客户端 -> 服务端：

```json
{"type":"leave_room"}
```

成功返回：

```json
{"type":"room_state","room_id":"default","room_name":"默认房间"}
```

### 4.4 `room_state`

服务端主动通知设备当前房间变化。字段为 `room_id`、`room_name`；失败时可带 `ok:false,error`。

## 5. 音频对讲消息

### 5.1 `start`

发送端 -> 服务端：

```json
{"type":"start","message_id":"003-1","frame_ms":60,"sample_rate":16000}
```

服务端按发送设备当前房间路由，并锁定该段所属房间。成功后向同房间其它设备发送：

```json
{"type":"start","source_id":"003","message_id":"003-1","frame_ms":60,"sample_rate":16000}
```

发送端同时收到：

```json
{"type":"start_ack","message_id":"003-1","ok":true}
```

若全局已有其它发送者，返回 `ok:false,error:"busy"`。当前实现发言锁是全局锁；同一时间只允许一段语音。

### 5.2 `receive_ready` / `busy`

接收端收到 `start` 后回复：

```json
{"type":"receive_ready","message_id":"003-1"}
```

设备忙时回复：

```json
{"type":"busy","message_id":"003-1"}
```

服务端发送 start 时默认将接收者设为可接收，立即转发，不等待 receive_ready。接收端仍可发送 receive_ready；该确认只在约2秒窗口内有效。busy 或窗口外确认会取消该接收者本段接收资格。

### 5.3 二进制音频帧

发送端在 `start` 后发送 WebSocket Binary 消息。每条 Binary 消息承载一帧原始 Opus 数据；浏览器模拟器也使用 WebCodecs 编码后的原始 Opus 包。服务端按同房间且仍可接收本段的连接转发，发送者不会收到自己的帧。

### 5.4 `end`

发送端 -> 服务端：

```json
{"type":"end","message_id":"003-1","frame_count":35}
```

`frame_count` 必须等于服务端实际收到的帧数。成功返回：

```json
{"type":"end_ack","message_id":"003-1","ok":true,"receivers":1}
```

同时向接收端发送：

```json
{"type":"end","message_id":"003-1","frame_count":35}
```

`receivers` 只表示服务端成功排入接收连接，不保证扬声器播放完成。

### 5.5 `abort`

发送端可发送：

```json
{"type":"abort","message_id":"003-1"}
```

服务端清理发送状态并向已收到 start 的接收端发送：

```json
{"type":"abort","message_id":"003-1"}
```

### 5.6 `error`

服务端发现非法帧、超限、乱序或结束帧数不匹配时发送：

```json
{"type":"error","error":"invalid or oversized audio frame"}
```

当前会话随后被中止。

## 6. 图片消息

设备将 JPEG 以 multipart 形式上传到 `POST /api/intercom/images`。服务端返回 `image_url`，随后设备通过 WebSocket 发送 `image_message`。服务端向同房间设备广播，并补充 `message_id`、`source_id` 和接收方的 `chat_session_id`。

```json
{"type":"image_message","message_id":"...","source_id":"002","image_url":"/api/intercom/images/...","text":"收到一张图片"}
```

接收设备将相对路径按已配置的 WebSocket 地址转换为 HTTP(S) 地址，再按需下载图片。当前图片接口限制 JPEG 和 512 KiB；服务端暂存于内存，图片鉴权、持久化、过期清理和缩略图接口尚未实现。

## 6. 典型流程

1. 建立 `/ws`，发送 `hello`，等待 `hello_ack`。
2. 发送 `join_room` 或 `create_room`，等待对应 ack；创建房间时用 invite_device_ids 指定邀请名单，创建者由连接身份确定。
3. 发言方发送 `start`；服务端向同房间设备发送 start，并向发言方返回 start_ack。
4. 接收方可发送 receive_ready；服务端已默认开始转发，设备忙时发送 busy 拒绝。
5. 发言方逐帧发送 Binary Opus。
6. 发言方发送 `end`；服务端向接收方发送 end，并返回 end_ack。
7. 连接断开时，旧会话失效，不支持续传或离线补播。

## 7. 房间邀请语义（更新）

创建房间请求中的 `device_ids` 表示邀请设备列表（推荐新字段名 `invite_device_ids`）。创建者通过 `creator_device_id` 或当前 WebSocket 设备身份确定，并自动进入房间。

- 邀请列表为空：房间开放加入，任意设备可使用加入码申请加入。
- 邀请列表非空：只有列表中的设备可以加入；在线受邀设备收到 `room_invite`，不会自动切换房间。
- 设备接受邀请后发送 `accept_room_invite`，服务端校验后切换活动房间。固件保存最近一次邀请的房间和加入码，`self.intercom.accept_room_invite` 无输入参数即可接受；请求发送成功后清除待处理邀请码，没有邀请时返回 false，新邀请会覆盖旧邀请。

```json
{"type":"room_invite","room_id":"room-abc","room_name":"房间002","code":"1234","source_id":"003"}
```

```json
{"type":"accept_room_invite","code":"1234"}（由固件自动使用最近邀请发送）
```

成功返回 `accept_room_invite_ack`，随后发送 `room_state`。`room_state` 也用于通知房间名称编辑；仅名称更新不重置聊天视图。实际切换时携带新的 chat_session_id。

## 8. WebSocket 房间列表查询

已注册设备发送（不需要管理端 Cookie）：

```json
{"type":"list_rooms","request_id":"rooms-1"}
```

| 字段 | 类型 | 规则 |
|---|---|---|
| type | string | 固定 list_rooms |
| request_id | string | 必填，1～64 字节；客户端生成，用于匹配响应 |

成功响应：

```json
{"type":"list_rooms_ack","request_id":"rooms-1","ok":true,"rooms":[{"room_id":"default","room_name":"默认房间","is_default":true,"is_current":true,"can_join":true,"code":""}]}
```

| 响应字段 | 类型 | 说明 |
|---|---|---|
| type | string | 固定 list_rooms_ack |
| request_id | string | 原样回传请求标识 |
| ok | boolean | 查询是否成功 |
| error | string | 失败原因；成功时省略 |
| rooms | array | 可加入的未关闭房间；失败时省略 |
| rooms[].room_id | string | 稳定房间 ID |
| rooms[].room_name | string | 房间名称 |
| rooms[].is_default | boolean | 是否默认房间 |
| rooms[].is_current | boolean | 本机当前是否处于此房间 |
| rooms[].can_join | boolean | 是否允许本机加入；当前返回的房间均为 true |
| rooms[].code | string | 四位加入码（1000～9999），不使用前导零；默认房间为空字符串 |

服务端只返回默认房间、开放房间、本机创建或本机受邀房间，避免泄露其它受限房间加入码。响应最大 8192 字节，超出时返回 ok:false 和错误原因，不静默截断。请求控制帧仍限制 1024 字节。

固件工具 `self.intercom.list_rooms` 通过独立工作任务查询，最多等待 5 秒，返回实际响应 JSON 或明确错误；不阻塞应用主任务。3.5B会同时使用成功响应显示15秒选房弹窗，名称后附加入码；点击复用join_room／leave_room，等待现有确认消息。此交互不改变MCP参数、返回格式或WebSocket报文。可说“当前有哪些对讲房间，加入码是什么”。默认房间通过 leave_room 返回，不使用空加入码加入。

## 9. ASR 与临时聊天记录（服务端已实现）

### 管理配置

以下接口沿用管理员 Cookie 鉴权：

- `GET /api/asr/config`：返回 `enabled`（boolean）、`url`（完整 HTTPS chat/completions 地址）、`model`（string）、`key_configured`（boolean），不返回密钥。
- `PUT /api/asr/config`：JSON 字段 `enabled`、`url`、`model`、`api_key`、`clear_key`。Key 留空保留原值；clear_key=true 清除。默认模型 qwen3-asr-flash。启用需要 Key 和模型。成功返回 `{"ok":true}`；非法配置 400、保存失败 500。
- `POST /api/asr/test`：正文为 WAV 二进制（最大 960044 字节），返回 `{"text":"喂喂。"}`；上传无效 400，上游失败 502；30 秒超时。

URL 必须完整，例如 `https://<workspace>.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions`。使用 Base64 WAV 内联上传，不需要公网录音 URL。指定 -db 后配置保存在 service_config 表；无 -db 时只保留在进程内。Key 不进入设备报文或接口响应。

### 聊天能力协商

hello 可增加 `chat_enabled:true`；默认为 false，旧固件不收到聊天事件。hello_ack 和成功房间切换响应增加 `chat_session_id`、`playback_token`，二者当前均为随机连接/房间视图令牌。重连或切房生成新令牌。

### 记录事件

协商后，同段参与者（包含发送者）收到 `chat_message`，识别完成收到 `chat_message_update`。字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| type | string | chat_message / chat_message_update |
| chat_session_id | string | 接收设备当前视图标识 |
| message_id | string | 服务端生成的唯一记录 ID |
| room_id | string | 发言开始时固定的房间 |
| source_id | string | 发送设备 ID |
| created_at | string | UTC RFC3339 时间 |
| duration_ms | integer | 帧数乘 60ms |
| text | string | 最多 80 个 Unicode 字符，超长后加省略号 |
| text_truncated | boolean | 是否被截断 |
| asr_status | string | disabled / pending / completed / failed |
| audio_available | boolean | 生成记录时音频是否可用；客户端还需处理回放 410 |
| audio_path | string | 音频回放地址；指定 -public-base-url 时为完整 URL，否则为相对地址 |

断线或切房后不推送旧视图识别结果。记录内存保存，重启清空；音频最长 30 分钟、全局 32MiB、新消息生成时每视图保留最近 20 条引用。淘汰后不补历史，回放返回 410。ASR 2 并发、有界队列、30 秒超时；失败不影响实时转发。调试 WAV 开关与本功能独立。

### 回放

`GET /api/intercom/messages/{message_id}/audio`，当前不做 token、管理员登录或参与者校验，直接 GET audio_path 即可下载。音频不存在或已失效返回 410。

二进制格式均为小端：`AIR1`（4字节魔数）、uint32 采样率16000、uint16 声道1、uint16 帧时长60、uint32 包数；随后重复 uint32 包长加对应原始 Opus 包。禁止把各包直接拼接给解码器。响应 Content-Type 为 application/octet-stream，Cache-Control 为 no-store。

控制台已增加“ASR 配置”tab。3.5B 固件已声明 chat_enabled:true，右侧以彩色文字按钮显示消息并支持 AIR1 分帧下载回放；单击播放，再次点击停止。记录最多20条，切房/断线清空。

回放公网地址可配置为 `-public-base-url http://111.62.241.117:9200`，与本地监听地址独立。audio_path 不携带凭据，下载无需 Authorization 头。

原始 Opus 位于内存 `server.chat.messages -> chatMessage.packets`，不写 .opus 文件；HTTP 请求时才封装 AIR1。`recordings/*.wav` 为独立调试录音；`playback_token` 字段暂保留但回放不使用。

## 10. 房间语音通报与 TTS 配置

管理员接口：

- GET /api/tts/config：返回 enabled、url、model、voice、key_configured，不回显api_key。
- PUT /api/tts/config：字段 enabled（boolean）、url、model、voice、api_key（string）、clear_key（boolean）。Key留空保留；clear_key明确清除。-db模式保存到service_config的tts项。
- POST /api/tts/test：JSON `{"text":"设备零零三邀请你加入调度组。"}`，1～160字符；返回ok、duration_ms、opus_packets，验证TTS合成与AIR1所需Opus转码。失败返回502；该接口不自动向设备播放测试音。

默认模型qwen-audio-3.1-tts-flash，音色longanhuan_v3.1。URL为`https://<workspace>.cn-beijing.maas.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer`，使用独立TTS密钥配置。输出24kHz单声道PCM16 WAV，服务端按实际data字节数解析（兼容厂商占位长度），重采样到16kHz后编码60ms Opus包，复用AIR1回放接口。

成员实际加入成功时，目标房间当前在线且chat_enabled的设备收到系统记录；加入者文字为“你已加入…”，其它成员为“设备…加入了…”。重复加入同房间不生成加入通报；hello成功后另发member_online上线通报，包含恢复原房间的重连。创建房间也向创建者生成加入记录；被邀请设备不自动加入。

邀请事件只发送给指定在线受邀设备，原room_invite控制帧保持，另生成系统聊天记录“设备…邀请你加入…”。该记录room_id为接收者当前视图房间，event_room_id/name才是被邀请的目标房间。

系统记录沿用chat_message和chat_message_update，新增字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| event_kind | string | member_joined / member_online / room_invite；普通对讲省略 |
| event_room_id | string | 事件目标房间ID |
| event_room_name | string | 事件目标房间名称 |
| announce | boolean | TTS准备完成时为true，设备可排队自动播报 |

文字立即生成，TTS异步单工作任务处理，队列16项、单任务45秒超时。失败或未启用TTS仍保留文字，audio_available=false，不自动播报。成功更新同一message_id，audio_available=true并提供AIR1 audio_path。密钥不下发设备；本功能不依赖ASR启用。

固件系统记录用独立底色显示；收到announce后按ID去重，待播队列最多4条（满时丢最早待播项），音频空闲才播放。实时对讲和助手回复优先；已开始通报沿用可被实时对讲打断的回放链路。断线、切房清空队列，旧session识别/合成更新不显示。当前逐条通报，尚未合并多人加入播报；离线邀请不补发。

上线通报在hello_ack之后发送给当前房间在线且chat_enabled的设备（包含上线者）。其它成员收到“设备003已上线，当前在某房间”，上线者收到“你已上线，当前在某房间”。默认房间同样播报，每次重连也播报。返回默认房间会生成member_joined事件。

## 11. 托管模式

新增set_hosting(enabled:boolean)及hosting_state(ok/enabled/error)控制帧。启用需ASR、TTS和LLM已开启；仅作用于当前连接，切房/断线清除。真人ASR完成后，模型判断是否回复，自动回复通过现有start/Binary/end广播，聊天记录额外标记automated:true，不再次触发ASR或模型判断。

GET/PUT /api/hosting/config和POST /api/hosting/test沿用管理员登录。模型默认为qwen3.8-flash，配置支持URL、Key、model、prompt和enabled；查询不返回Key。详细流程和限制见[托管模式设计](intercom-hosting.md)。

## 12. 管理端房间编辑与删除

`PATCH /api/rooms/{id}`（管理员Cookie）接收完整编辑值：name（去首尾空格，1～80字符）、invite_device_ids（设备ID数组，空为开放加入）。返回房间对象，包含invite_device_ids、created_by_device_id。房间ID和加入码不变；默认或已关闭房间不可编辑。非法值返回400。

修改邀请范围不移动设备，不自动移除已有成员；仅向新增受邀在线设备发送room_invite和系统记录。名称变化向当前在线成员发送room_state，不重置聊天session。设备列表通过GET /api/devices获取，以room_id统计实际成员和online状态。

删除按钮使用POST /api/rooms/{id}/close软关闭房间。默认房间不可关闭；关闭时若该房间正在发言，服务端中止音频，向真人发送者报告 room closed，并向接收者发送 abort。关闭后设备归属持久化为默认房间，在线设备收到带新chat_session_id的room_state，清空旧记录并退出托管；加入码失效。控制台编辑弹窗每5秒更新设备在线状态。

编辑弹窗仅列出room_id等于当前房间的设备（含离线成员），无成员时显示空状态。未展示设备的原有邀请设置在保存时保留；开放加入开关启用时仍按空邀请列表保存。

## 13. 控制台用户管理

所有已登录用户具有相同功能，本阶段不区分角色或资源归属。登录POST /api/login从users表验证账号；新数据库初始账号admin、密码admin123，已有admin表首次启动时迁移到users，不覆盖已有用户密码。无-db模式使用内存用户，重启清空新增用户。

- GET /api/users：需登录，返回username、created_at数组，不返回密码或摘要。
- POST /api/users：需登录，JSON包含username和password；用户名为1～32位ASCII字母、数字、下划线或短横线，密码6～128字节。成功返回ok:true，重复用户名或非法参数返回400。
- PUT /api/users/{username}/password：需登录，JSON包含password，重置指定用户密码；成功后清除当前进程全部登录会话（包括其他用户的会话），用户需要重新登录。用户不存在或密码非法返回400；不需要提供原密码。

SQLite users表保存username主键、password_hash和created_at。密码使用PBKDF2-HMAC-SHA256（100000次、随机salt）摘要存储，不保存明文。admin表仅用于旧账号迁移，迁移后修改用户密码不会被旧表覆盖。每次登录生成独立随机Cookie会话，有效期12小时，HttpOnly及SameSite=Strict；直接HTTPS请求启用Secure。当前不提供删除用户、角色权限或公开注册接口。

控制台“用户管理”tab提供创建用户、用户列表和重置密码；密码字段不预填。新账号可直接登录使用全部功能。

### 退出登录

`POST /api/logout`：撤销当前Cookie对应的登录会话并清除airadio_admin Cookie，返回`{"ok":true}`。无Cookie或已退出时同样成功，其它用户会话不受影响；GET返回405。控制台登录后显示“退出登录”，成功后重新加载登录页，页面模拟器连接及采集随页面卸载结束。

### 设备总览

控制台“设备列表”tab位于用户管理前，GET /api/devices额外返回room_name和hosted；在线状态由当前连接判断，离线设备不展示实时工作模式。支持设备ID/房间搜索、在线筛选、每5秒刷新和手动刷新。last_seen_at目前表示最近hello注册时间，并非持续心跳时间；last_ip从新连接的RemoteAddr记录，经过反向代理时可能是代理地址，旧设备尚未重连时可能为空。

### 设备类型

hello可包含device_type字符串，允许simulator（浏览器模拟器）或physical（真实固件），未传时按physical处理。其它值拒绝注册。类型持久化到devices.device_type，GET /api/devices返回device_type，设备列表显示模拟设备/真实设备/未知。旧记录迁移为空，重连后补齐；此标识是客户端自报属性，不是身份认证。浏览器卡片不决定真实设备类型，通过POST /api/devices创建时即登记为simulator；已有卡片在成功hello后也会更新类型。

### 删除模拟设备（替代仅隐藏卡片行为）

DELETE /api/devices/{device_id}需登录，删除device_type=simulator的设备注册和房间实际成员关系，断开当前连接并取消托管；真实设备和未知类型返回400。不存在设备幂等返回ok:true。不会关闭房间或改写房间创建者、邀请名单等历史/配置字段。浏览器删除成功后释放录音/播放资源并刷新房间和设备列表；从未注册的本地卡片也可移除。删除成功后刷新不再恢复该设备，重新添加同一ID会创建新的离线模拟设备。

### 刷新页面恢复登录

GET /api/session验证当前airadio_admin Cookie：有效返回200及ok:true，失效返回401，其它方法405。页面加载时先检查已有会话，有效则恢复控制台，无需重复输入密码。登录会话仍只保存在服务进程内，服务重启、退出或重置密码清除会话后需要重新登录；不会把明文密码存入浏览器。

### 创建离线模拟设备

POST /api/devices需登录，JSON包含device_id（1～32位字母数字下划线短横线）。创建simulator类型的离线设备，初始默认房间、最近连接时间为空，不生成hello上线通报；重复ID返回400，不覆盖已有设备。控制台“添加设备”会先调用此接口，成功后显示卡片，因此不点击连接也能在设备总览看到并在刷新后恢复。WebSocket连接才更新在线状态及最近连接信息。

### 控制台展示补充

登录使用居中卡片，控制台以选项卡展示。房间卡片与编辑弹窗显示created_by_device_id，默认房间显示系统创建，旧记录无创建者时显示未记录。编辑弹窗另设已邀请设备区域，显示邀请ID、在线状态和是否加入；实际成员列表仍仅列当前房间成员，状态每5秒刷新。

SQLite设备恢复查询显式使用COALESCE(room_id,'') AS room_id，保证已加入房间在服务重启及再次hello后仍恢复，不回退默认房间。
