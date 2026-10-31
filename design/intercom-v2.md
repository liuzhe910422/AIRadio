# 灵矽语音对讲第二版：单组织房间管理

状态：当前房间与控制台实现说明，核对日期 2026-10-24。准确接口见[协议](intercom-protocol.md)。

## 1. 范围

当前基于单实例 Go WebSocket 服务提供房间、邀请、动态控制台账户和设备管理。所有登录用户拥有相同管理功能，尚无组织隔离、角色权限、账号与设备绑定或设备 token。SQLite 保存房间、设备、用户和服务配置；在线连接、登录会话、发言锁和聊天音频保留在内存。

设备继续使用第一版 hello 报文，不新增认证字段：

```json
{"type":"hello","version":1,"device_id":"truck-01"}
```

该方案只适合可信内网测试，不提供公网身份安全。

## 2. 房间规则

- 服务启动时创建固定默认房间 `default`，名称为“默认房间”。默认房间不能删除或关闭。
- 新设备首次 hello 后进入默认房间。
- 没有普通房间绑定的设备逻辑上属于默认房间。
- 一个设备同一时间只能属于一个房间。
- 普通房间有名称和固定四位数字加入码（1000～9999，不使用前导零）；加入码在房间生命周期内不变。
- 普通房间关闭后，所有设备回到默认房间。
- 创建房间时可指定邀请设备列表；创建者自动加入。邀请列表为空表示开放加入，非空时仅创建者和受邀设备可以加入，并通过 `room_invite` 通知后由设备决定是否接受。新设备不会自动加入已有普通房间。
- 设备发送加入码即可申请切换房间；服务端按邀请列表校验后切换。收到 `room_invite` 的设备必须明确接受；固件保存最近一次邀请，接受邀请 MCP 不需要用户输入加入码；离开普通房间后回到默认房间。
- 正在录音或发送时不允许切换房间。

## 3. SQLite 数据

数据库文件由 `-db` 参数指定，使用系统 `sqlite3` 命令访问并启用 WAL。当前主要表结构如下（由服务启动初始化，不应按旧设计手工建表）：

```sql
CREATE TABLE IF NOT EXISTS admin (id INTEGER PRIMARY KEY CHECK(id = 1), username TEXT NOT NULL, password_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS devices (device_id TEXT PRIMARY KEY, last_seen_at TEXT, ip TEXT, room_id TEXT, device_type TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS rooms (id TEXT PRIMARY KEY, name TEXT, join_code TEXT UNIQUE, is_default INTEGER, status TEXT, created_by TEXT, invite_device_ids TEXT DEFAULT '');
CREATE TABLE IF NOT EXISTS users (username TEXT PRIMARY KEY, password_hash TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS service_config (name TEXT PRIMARY KEY, value TEXT);
```

设备归属保存在 devices.room_id，不使用 room_devices 表；admin 表仅用于初始账号和旧数据迁移。数据库不保存聊天文字或 Opus。设备没有有效普通房间关系时按默认房间处理。

## 4. HTTP API

管理页面支持动态账户登录，所有已登录用户权限相同：

```text
POST /api/login        {"username":"admin","password":"..."}
GET  /api/rooms
POST /api/rooms        {"name":"城东车队","created_by_device_id":"truck-01","device_ids":["truck-01"]}
GET  /api/rooms/:id/devices
POST /api/rooms/:id/close
GET  /api/devices
```

HTTP 创建中的 `device_ids` 表示邀请名单，空列表表示开放加入，不会批量移动已注册设备。创建者自动加入，受邀者须自行接受。创建结果返回房间 ID、名称、四位加入码（1000～9999）、实际成员及邀请列表。新数据库默认账号为 `admin/admin123`，已有数据库沿用迁移后的密码；控制台“用户管理”支持新建用户和重置密码。使用 `-db ./data/intercom.db` 时，`./data` 相对于启动命令所在目录；推荐先 `cd server` 再启动，这样数据库位于 `server/data/intercom.db`。

### 动态账户与会话

控制台“用户管理”提供创建用户、用户列表和重置密码。用户名为1～32位ASCII字母、数字、下划线或短横线，密码为6～128字节。新账号创建后可直接登录，所有账户均可管理全部房间、设备和服务配置；当前没有删除用户、角色分级或自助注册。

| 接口 | 行为 |
| --- | --- |
| GET /api/users | 查询用户名及创建时间，不返回密码摘要 |
| POST /api/users | 使用username、password创建账户，重名或非法输入返回400 |
| PUT /api/users/{username}/password | 使用password重置密码，不要求原密码；成功后撤销全部用户的登录会话 |
| GET /api/session | 检查Cookie，有效200、无效401；页面刷新据此恢复登录 |
| POST /api/logout | 撤销当前会话，其他会话不受影响 |

上述用户管理接口均需登录。密码以随机salt及100000次PBKDF2-HMAC-SHA256摘要存储。登录会话在内存保存12小时，每次登录生成独立随机Cookie，最多128个会话。服务重启会清空会话；SQLite保存的账户和密码不受影响，无-db模式下新增账户随进程退出消失。

## 5. WebSocket 控制

hello 成功后服务端在 `hello_ack` 中返回当前房间：

```json
{"type":"hello_ack","ok":true,"room_id":"default","room_name":"默认房间"}
```

设备切换房间：

```json
{"type":"join_room","code":"1234"}
{"type":"leave_room"}
```

服务端返回 `join_room_ack` 或 `room_state`。音频 start 不携带房间 ID；服务端按设备当前房间路由，并在会话开始时固定房间。当前仍使用全局单发言权，不同房间不能并行发言。

设备也可以通过同一条 WebSocket 创建房间，不需要管理页面。创建者以已注册连接身份为准，邀请列表使用 invite_device_ids：

```json
{"type":"create_room","name":"城东车队","invite_device_ids":["truck-02"]}
```

邀请列表非空时限制加入范围，受邀设备不会自动切房；为空时开放加入。WebSocket 兼容旧字段 device_ids 作为邀请列表。服务端返回：

```json
{"type":"create_room_ack","ok":true,"room_id":"room-...","room_name":"城东车队","code":"1234","device_ids":["truck-01"]}
```

真实设备的 MCP 可以把语音识别出的房间名转换成这个控制帧；创建者会自动进入新房间。

## 6. Web 管理与设备模拟

`/` 提供登录、房间管理、设备模拟器，以及 ASR、TTS、托管 LLM、设备列表和用户管理选项卡。模拟器使用独立 WebSocket 和 WebCodecs 编解码原始 Opus，可用麦克风或合成测试音验证转发。发送按 start_ack 确认控制，忙时停止编码发送。需要支持 AudioEncoder／AudioDecoder 的浏览器；麦克风需要浏览器认可的安全上下文和用户授权。模拟器不能替代 ESP32 音频硬件验证。

## 7. 服务端调试日志

服务端使用 Go 标准 `log` 输出到启动进程的标准输出。为方便排查，以下事件都会输出 `debug` 日志：

- HTTP 请求方法、路径和远端地址。
- WebSocket hello 接受或拒绝、设备 ID、当前房间和远端地址。
- 每条控制帧的设备 ID、类型、消息 ID、房间 ID或加入码。
- 创建、加入、离开房间的结果、房间 ID、房间名称、加入码和设备列表。
- 二进制音频帧大小、start/end/abort、接收确认和错误。

例如：

```text
debug ws hello accepted device=truck-01 room=default remote=127.0.0.1:54321
debug ws control device=truck-01 type=create_room name="城东车队" device_count=0
debug room created device=truck-01 room=room-... name=城东车队 code=1234 devices=[truck-01]
debug ws binary device=truck-01 bytes=800
```

运行 `go run . ...` 时直接查看终端即可；用 systemd、Docker 或其他进程管理器运行时，应把标准输出接入对应日志系统。二进制帧只记录长度，不记录音频内容。

## 8. 验收

- 新设备进入默认房间，默认房间能正常广播。
- 创建房间返回唯一四位加入码（1000～9999）；创建者加入，受邀设备收到通知并自行接受；空邀请列表开放加入。
- 两台设备分别加入不同房间时，音频不跨房间转发；同房间仍能转发。
- 加入码错误、房间关闭、切换时发言均返回明确错误。
- 管理页面能观察在线设备和房间成员，并用两个模拟器完成上述流程。

## 9. 本地运行

需要 Go 1.24 或更高版本、CGO编译器、pkg-config、libopus 和 libopusfile 开发包；这些是服务端编译依赖，关闭调试WAV也仍然需要。SQLite模式另需系统 sqlite3 命令。安装及排错见[服务说明](../server/README.md)。固件使用ESP-IDF v6.0.1以上，优先release/v6.1。

### 9.1 使用内存数据运行

适合快速体验。进程退出后房间和设备数据会清空：

```sh
cd /Users/liuzhe/new-work/AIRadio/server
go run . -listen 127.0.0.1:9200 -save-wav=false
```

### 9.2 使用 SQLite 运行

适合验证重启后的数据保留。数据库文件不存在时会自动创建，服务启动时会创建默认房间：

```sh
cd /Users/liuzhe/new-work/AIRadio/server
mkdir -p data
go run . -listen 127.0.0.1:9200 -db ./data/intercom.db -save-wav=false
```

浏览器打开：

```text
http://127.0.0.1:9200/
```

服务端 WebSocket 地址为：

```text
ws://127.0.0.1:9200/ws
```

如果需要让局域网中的 ESP32 连接，把监听地址改成 `0.0.0.0:9200`，设备配置中填写运行电脑的局域网 IP。不要把 `0.0.0.0` 或 `127.0.0.1` 填到设备中。

默认调试 WAV 会写入 `server/recordings/`。不需要保存语音时使用 `-save-wav=false`；调试 WAV 含真实语音，不应提交到版本库或暴露给不可信用户。

## 10. 管理页面使用

1. 打开 `http://127.0.0.1:9200/`。
2. 使用默认账号登录：新数据库用户名 `admin`，密码 `admin123`；已有数据库使用当前账号密码。
3. 在“房间”区域填写房间名称和创建设备 ID。邀请设备 ID 列表可以留空，表示开放加入；不会自动移动全部设备。
4. 点击“创建房间”，保存返回的四位加入码（1000～9999）。房间卡片显示加入码、创建者、成员和在线数量，编辑弹窗可查看房间ID并修改名称和邀请范围。
5. 在“设备模拟器”输入 `truck-01`，点击“添加设备”；重复操作添加 `truck-02`。添加时先创建可持久化的离线simulator记录，点击“连接”才建立独立WebSocket并发送hello。
6. 点击模拟器“连接”并收到 `hello_ack` 后，填写新房间名称，点击“创建房间”。这会发送使用当前连接身份的 `create_room`，返回加入码，并只让该模拟器进入新房间。也可以输入已有加入码点击“加入”；点击“默认房间”会发送 `leave_room`。模拟器卡片始终显示当前房间。
7. 同一房间的两个模拟器中点击“开始麦克风对讲”，允许浏览器使用麦克风后说话，再点击“停止麦克风对讲”；同房间模拟器会显示“正在对讲”和发送者，并收到播放声音。没有麦克风权限时点击“开始对讲（测试音）”，可发送 WebCodecs 编码的 Opus 测试音验证路由。
8. 让两个模拟器加入不同房间后再次发送测试音，接收端不应收到另一房间的 `start` 或音频帧。
9. 管理员点击普通房间的“删除”后，该房间设备会回到默认房间；默认房间没有关闭按钮。

浏览器播放音频需要用户先与页面交互；麦克风权限不是当前测试流程的必要条件。页面中的测试音按钮使用 WebCodecs 将合成 PCM 编码为原始 Opus 包，不使用 MediaRecorder 容器格式。

## 11. 自动化测试

在仓库根目录执行：

```sh
cd /Users/liuzhe/new-work/AIRadio/server
go test ./...
```

测试覆盖现有 WebSocket 协议回归，以及第二版的房间创建、四位加入码（1000～9999）、设备切换、离开回默认房间和房间关闭后的回退。测试使用内存仓储，不会修改工作区中的 SQLite 文件或录音目录。

编译检查：

```sh
go build ./...
git diff --check
```

如果运行环境禁止测试绑定本地 `httptest` 端口，需要在允许本地端口监听的环境中执行 `go test ./...`。测试通过只说明服务端协议和房间逻辑正确，不代表 ESP32 的触摸、麦克风、扬声器、PSRAM 和 Wi-Fi 重连已验证。

## 12. 手工 WebSocket 测试流程

也可以使用浏览器页面完成测试；需要观察原始控制帧时，可使用 WebSocket 客户端连接 `/ws`，按以下顺序发送：

```text
设备 A -> {"type":"hello","version":1,"device_id":"a"}
设备 A <- hello_ack，确认 room_id 为 default
设备 A -> {"type":"join_room","code":"1234"}
设备 A <- join_room_ack，确认 room_name 正确
设备 A -> {"type":"start","message_id":"a-1","frame_ms":60,"sample_rate":16000}
设备 A -> Binary，发送一帧音频
设备 A -> {"type":"end","message_id":"a-1","frame_count":1}
```

设备 B 必须先加入同一个房间，才能收到 A 的 `start`、二进制帧和 `end`。设备 C 留在默认房间时不应收到这段消息。

## 13. 当前限制

- 支持动态用户，但所有用户权限相同；不支持删除用户、角色权限或设备归属隔离。
- 设备仍只使用 `device_id`，没有 token 或 TLS；只适用于可信内网。
- SQLite 通过系统 `sqlite3` 命令访问，部署机器必须安装该命令。
- 浏览器模拟器用于房间和转发业务测试，不能替代 ESP32 原始 Opus 音频链路测试。
- 浏览器模拟器已使用原始 Opus，可用于验证转发和调试 WAV；当前 3.5B 硬件录放音、房间切换和托管链路已完成实机联调。
- 已提供当前会话临时文字记录与音频回放，不提供跨会话历史查询、离线补播、永久录音检索、跨实例路由和正式审计。

## 房间列表语音查询

新增 WebSocket `list_rooms` / `list_rooms_ack` 和普通语音 MCP 工具 `self.intercom.list_rooms`。设备无需保存管理员凭据，服务端根据已注册设备身份过滤可加入房间，返回房间名称、加入码和当前房间标记。详细字段、8192 字节响应上限和失败规则见 [协议文档](intercom-protocol.md#8-websocket-房间列表查询)。固件使用独立工作任务等待实际响应，5 秒超时；查询工具不返回虚构数据或仅“请求已发送”。

3.5B固件查询成功后还会显示15秒倒计时选房弹窗，列出房间名称和加入码，当前房间高亮；点击后申请切换并等待确认，默认房间通过leave_room返回。弹窗可手动关闭，选择超时保持原房间；切换确认最多等待5秒，超时提示重新查询。具体交互见[设备界面说明](intercom-ui-480x320.md#3-房间和助手交互)。

## 定时事件

事件功能见[事件功能设计](events.md)。设备端只查询 `pending` 事件；完成、取消或逾期后从设备列表和数量中移除。事件列表弹窗显示 15 秒倒计时，关闭后仍保留最近一次序号映射，可继续语音说“完成第一个事件”或“取消第一个事件”。事件时间使用北京时间 `Asia/Shanghai`，查询结果只用于弹窗，不生成可播放的对讲记录。
