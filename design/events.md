# AIRadio 事件功能设计

## 目标

事件是跨设备共享的定时待办事项。一个设备创建事件，另一个设备负责处理；服务端保存事件、执行提醒和状态同步。事件正文只描述任务内容，不包含 `action` 或 `payload`，设备不需要理解具体业务动作。

## 数据模型

事件时间统一按北京时间（`Asia/Shanghai`）展示和传输，格式为 RFC3339，例如 `2026-10-31T12:00:00+08:00`。服务端调度使用解析后的绝对时间，避免设备或服务器本地时区差异影响触发时间。

```json
{
  "event_id": "evt_xxx",
  "target_device_id": "001",
  "creator_device_id": "002",
  "content": "关门",
  "scheduled_at": "2026-10-30T12:00:00+08:00",
  "status": "pending",
  "created_at": "2026-10-30T11:00:00Z",
  "completed_at": null,
  "cancelled_at": null
}
```

状态包括 `pending`、`completed`、`cancelled` 和 `overdue`。`overdue` 是终态：到达计划时间仍未完成时标记为逾期，并停止后续提醒。完成或取消也会停止提醒。

## MCP

固件提供以下工具：

- `self.events.create`：参数为目标设备号、事件文字和 RFC3339 时间。
- `self.events.list`：查询当前设备的 `pending` 事件，并在支持的屏幕上打开 15 秒事件列表弹窗。
- `self.events.complete`：参数为事件列表序号 `index`，例如 `index=1` 表示完成第一个事件。
- `self.events.cancel`：参数为事件列表序号 `index`，例如 `index=1` 表示取消第一个事件。

设备保存最近一次查询的序号到稳定 `event_id` 映射。弹窗关闭后映射仍保留，用户仍可说“完成第一个事件”或“取消第一个事件”；完成、取消和逾期事件从设备列表和数量中移除。

## WebSocket

设备向服务端发送 `event_create`、`event_list`、`event_complete`、`event_cancel`。服务端返回对应的 ACK。服务端主动向设备发送 `event_state` 和 `event_notification`。通知包含事件 ID、文字和状态；语音播报沿用现有 TTS/Opus 聊天通知链路。

## 提醒规则

服务端每分钟扫描事件：计划时间前 30 分钟内，目标设备每分钟收到一次 TTS 提醒。到达计划时间时发送一次“事件已到时间，请处理”。下一次扫描将事件标记为 `overdue`，发送一次逾期通知给目标设备和创建设备，之后不再提醒。完成、取消通知同时发给目标设备和创建设备。

设备离线时不补播已经错过的分钟提醒；重新上线后通过 `self.events.list` 获取当前 `pending` 事件。TTS 未启用时仍发送文字控制通知。查询结果只用于弹窗和数量更新，不写入可播放的对讲记录。

## 管理端

管理页面新增“事件管理”Tab，调用 `GET /api/events` 查询事件，显示目标设备、创建设备、内容、计划时间和状态，并支持 `POST /api/events/{id}/cancel` 管理员取消。

## 当前实现状态

服务端已经接入 SQLite 事件表和读写、Web 管理查询/取消、设备事件 CRUD、每分钟提醒、逾期终态、TTS 通知、`event_execute` 和执行确认。固件已注册四个事件 MCP，支持事件数量、15 秒倒计时列表弹窗、序号完成/取消、事件通知颜色区分和执行确认；事件时间统一使用北京时间 `Asia/Shanghai` 的 RFC3339 偏移格式。

## 文档同步

房间协议文档只描述房间消息，本功能不改变房间字段，因此无需修改 `design/intercom-protocol.md`。`design/intercom-v2.md` 的“当前限制”和验收清单应在事件功能稳定后补充事件 API、离线行为和 TTS 配置要求；本文件作为事件功能的专门设计文档。
