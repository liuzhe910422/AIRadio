# 对讲多语言与翻译设计

状态：已实现中文／英文设备语言切换、英文对讲翻译和 3.5B 对讲界面动态文案；通用 `Lang::Strings` 启动与系统提示仍使用固件编译期语言资源。

## 1. 范围

设备语言由对讲服务端保存，当前支持 `zh-CN` 和 `en-US`。固件不新增语言 NVS 项；连接时从服务端同步语言，只在内存中维护当前语言。语言切换只影响 AIRadio 对讲界面、对讲状态提示、房间／事件提示和服务端系统通知。

通用小智系统文案仍由现有编译期 `Lang::Strings` 提供，例如启动唤醒提示、Wi-Fi、OTA 和普通助手状态。真人对讲原文保留原语言；英文设备额外收到翻译文字和翻译音频。

## 2. 语言切换流程

用户说“切换为英文模式”“切回中文”“使用英文界面”等表达时，助手必须调用 `self.intercom.set_language`，不能只用自然语言回复。工具只接受：

```json
{"language":"zh-CN"}
```

或：

```json
{"language":"en-US"}
```

固件通过现有对讲 WebSocket 发送 `set_language`。服务端以当前连接身份确定设备并持久化语言，随后返回：

```json
{"type":"language_state","ok":true,"language":"en-US"}
```

固件收到确认后更新内存语言并立即刷新 3.5B 对讲页面。重连时服务端在 `hello_ack` 返回语言：

```json
{"type":"hello_ack","ok":true,"language":"en-US"}
```

历史数据库设备没有语言值时按 `zh-CN` 处理。非法语言不会写入任意值，会回退到中文。`self.intercom.get_language` 用于查询当前语言，模型不能根据上下文猜测。

## 3. 系统通知

上线、加入房间和房间邀请由服务端根据接收设备语言直接生成文字和 TTS 文本。英文设备示例：

```text
You are online in room 100.
Device 004 is online in room 100.
Device 004 invited you to join room 100.
```

中文设备继续收到中文通知。系统通知不再依赖二次 LLM 翻译，避免额外延迟和费用；设备切换语言后，新通知使用新语言，已有会话记录不重新翻译。

## 4. 对讲翻译链路

实时原始 Opus 仍直接转发，保证原始语音低延迟。完整语音结束后，英文接收设备的异步任务按以下顺序执行：

```text
原始 Opus → ASR → LLM 翻译 → TTS → 16kHz Opus → 设备播放
```

LLM 提示词要求只返回英文译文，保留人名、数字、地点和操作指令。翻译消息使用独立记录，示例字段：

```json
{
  "type":"chat_message",
  "message_id":"translated-id",
  "parent_message_id":"original-id",
  "language":"en-US",
  "audio_kind":"translation",
  "source_id":"004",
  "text":"Please gather at the east gate.",
  "audio_available":true
}
```

翻译消息只发送给英文设备。原始消息仍保留 `audio_kind: original` 语义并发送给参与者。翻译任务使用有界队列和超时；ASR、LLM 或 TTS 失败时保留原始文字和音频，不阻塞实时对讲。

翻译音频必须等待设备当前实时播放结束后再发送。固件在播放原始音频时缓存翻译 `start` 控制帧，原始播放排空后自动开始翻译音频，避免出现 `音频播放中` 拒绝和丢帧。

## 5. 3.5B 界面

3.5B 对讲页面按内存语言立即刷新以下文案：标题、设备和待处理事件、对讲状态、房间选择、加入码、切换中／失败、事件弹窗、房间查询错误、语音回放错误、播放按钮、识别占位和翻译标记。

房间名称、事件内容、设备 ID 以及真人原始发言属于业务数据，保留服务端提供的原文。英文翻译作为独立记录显示 `Translation`，不覆盖原始记录。

## 6. 持久化与兼容

SQLite `devices` 表新增 `language` 字段，默认 `zh-CN`，启动时兼容旧数据库迁移。设备既有 `intercom` NVS（设备 ID、服务地址、API Key）不变。服务端内存模式仍只在进程生命周期内保存语言。

## 7. 验证边界

服务端 Go 测试覆盖语言默认值、SQLite 重启恢复、非法语言规范化和既有对讲回归。真实设备仍需验证语言切换后的页面刷新、系统通知 TTS、原始音频与翻译音频顺序，以及长时间运行时的队列和内存稳定性。
