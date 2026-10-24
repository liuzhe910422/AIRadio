# AIRadio 对讲与 AI 服务

这是单实例 Go WebSocket 服务。服务端注册本机 ID、限制同时只能上传一段语音，并把原始 Opus 帧转发给同一房间的其他在线设备。设备初始进入默认房间，可创建或加入其他房间。只有一台设备在线时也可以正常连接和发送，`end_ack` 的 `ok` 为 `true`、`receivers` 为 `0`，不会回放给发送者。

调试 WAV 默认开启。服务端将收到的 Opus 解码为 16 kHz、单声道 PCM，完整上传后保存为 WAV。中断的会话删除临时文件。WAV 仅用于本地调试，不提供离线补播。

需要 Go 1.24 或更高版本、CGO 编译器、`pkg-config`，以及 `libopus` 和 `libopusfile` 开发包。安装原生依赖：

```sh
# macOS（Homebrew）
brew install opus opusfile pkg-config

# Debian/Ubuntu
sudo apt install libopus-dev libopusfile-dev pkg-config
```

如果 `go run` 报错 `Package 'opusfile' not found`，说明编译时无法找到 `libopusfile`。Go 依赖 `github.com/hraban/opus` 默认同时链接 `opus` 和 `opusfile`，只安装 `opus` 不够；即使传入 `-save-wav=false`，仍需要这些编译依赖。安装后可用 `pkg-config --modversion opus opusfile` 检查两个库是否都能找到，再重新运行服务。如果仍报错，检查 `PKG_CONFIG_PATH` 是否包含相应 `.pc` 文件所在目录。

在仓库根目录运行：

```sh
cd server
go run . -listen 0.0.0.0:8080 -db airadio.db
```

管理页面地址为 `http://<服务器局域网IP>:8080/`，新数据库初始账号为 `admin/admin123`；已有数据库保留迁移后的账号密码。控制台“用户管理”可动态创建用户、查看用户列表、重置任意用户密码；所有登录用户权限相同，尚未区分角色或设备归属。密码使用加盐 PBKDF2-HMAC-SHA256 摘要保存，不提供删除用户或公开注册接口。会话有效期为12小时，刷新页面可恢复有效登录；退出只撤销当前会话，重置任意用户密码会撤销全部会话，重启后需重新登录。登录 Cookie 使用 HttpOnly、SameSite=Strict，直接 HTTPS 请求下启用 Secure 标记。

`-db airadio.db` 启用 SQLite 持久化，需要系统安装 `sqlite3` 命令；不传 `-db` 时房间、设备、用户和服务配置仅保存在内存。管理端关闭房间使用 `POST /api/rooms/<房间ID>/close`，在线成员会停止原房间播放、退出托管并收到默认房间状态。

默认录音目录为当前工作目录下的 `recordings/`，按上面的命令运行时即 `server/recordings/`。可用 `-record-dir /path/to/recordings` 指定目录，或用 `-save-wav=false` 关闭保存。服务启动时会打印目录，完整录音保存后会打印文件路径。WAV 含真实语音，不会自动清理；服务只适合可信局域网调试。

设备进入现有 Wi-Fi 配网模式后，在 `http://192.168.4.1` 的“高级设置”中填写各不相同的本机 ID，以及 `ws://<服务器局域网IP>:8080/ws`。两项都留空表示停用对讲。保存后完成 Wi-Fi 配网并退出配网模式，设备联网后配置生效。设备屏幕点击“开始对讲”后实时发送音频，再次点击结束；当前没有独立 X 取消按钮，BOOT 键仍用于原有助手操作。

目标板型是 `waveshare/esp32-s3-touch-lcd-3.5b`。固件使用现有 ESP-IDF 音频编解码器和独立对讲 WebSocket。触摸、录放音、断线恢复与 PSRAM 峰值仍需至少两台实机验证。设备 WebSocket 目前没有鉴权、内建加密、离线补播或送达保证；管理 API 使用控制台用户会话鉴权。

控制台还支持房间编辑／邀请／软关闭、设备总览和离线模拟设备创建／删除。浏览器模拟器使用 WebCodecs 编解码原始 Opus，需要支持相应API的浏览器；麦克风采集需要安全上下文及授权。服务端仍使用全局单发言权，不同房间不能同时发言。

ASR、TTS 和托管 LLM 分别在独立选项卡配置。完整对讲结束后异步识别并推送当前会话文字，3.5B右侧记录可点击回放；TTS用于上线／加入／邀请通报，托管模式可判断并自动回复。托管启用要求三个服务均开启，切房或重连恢复普通模式。配置按-db模式持久化，密钥不回显。

聊天文字与原始 Opus 仅存于进程内存，重启清空；设备最多保留20条，切房或断线清空。音频最长30分钟、全局缓存32MiB及512条记录上限，AIR1回放接口当前无需鉴权。调试WAV与该缓存独立。详细接口见[协议](../design/intercom-protocol.md)，架构见[总体设计](../design/intercom-overview.md)。
