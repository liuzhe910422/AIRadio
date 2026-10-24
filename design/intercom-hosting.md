# 对讲托管模式

状态：服务端和3.5B固件已接入，待实机联调。

## 数据流

真人对讲完成→独立ASR得到文字→同房间在线托管设备进入LLM判断队列→qwen3.8-flash返回should_reply和text→需要回复时百炼TTS合成→转16kHz/60ms Opus→按start/Binary/end在房间播放并生成automated聊天记录。

只处理真人ASR完成消息，不处理系统通知或自动回复，因此不形成模型间循环。输入含目标设备ID、发言者、当前文字和最近10条同房间文字。当前单个LLM工作任务、有界16项队列；每设备最多一个任务，成功回复后10秒冷却。20秒任务超时、25秒任务有效期；回复最多80字符。不同设备依次判断，不保证所有设备都回复。

真实发言优先于合成回复，可中止自动音频。等待发言权超过有效期丢弃。退出托管、切房、断线通过设备身份、视图令牌和托管代次使旧任务失效；当前不会中断已发出的HTTP请求，但结果不再使用。托管状态只保存在连接内存，重连恢复手动。

## 控制台及HTTP

独立“托管LLM配置”tab：enabled、url、api_key、model、prompt。默认qwen3.8-flash；提示词为全局默认，暂不支持每设备覆盖。支持测试判断。

- GET /api/hosting/config：管理员Cookie，返回enabled/url/model/prompt/key_configured，不回显Key。
- PUT /api/hosting/config：相同配置字段及clear_key；Key留空保留，clear_key明确清除；默认模型和提示词可自动补齐。HTTPS地址须以/chat/completions结束。-db模式持久化到service_config的hosting项。
- POST /api/hosting/test：JSON包含device_id、text，返回should_reply、text；只测试判断，不发送房间语音。

## WebSocket

```json
{"type":"set_hosting","enabled":true}
```

```json
{"type":"hosting_state","ok":true,"enabled":true}
```

失败返回ok:false和error，不修改设备已确认状态。启用需要ASR、LLM、TTS全部开启；发言过程中不允许切换。set_hosting请求来源由已注册连接确定，不信任客户端声明其它设备ID。

自动语音使用现有start/source_id/message_id/frame_ms/sample_rate及Binary/end。聊天记录automated:true区分托管回复；不再次提交ASR或触发托管。

## 固件

新增普通MCP self.intercom.set_hosting(enabled:boolean)、self.intercom.get_hosting()。设置工具返回仅表示请求排队，hosting_state成功后更新实际状态。开启时主按钮黄色、文字“托管模式”，点击请求退出，服务端确认后恢复手动。语音开关仍通过小智通道，沿用MCP回复完成后关闭小智会话规则。

托管设备保持对讲连接并接收房间音频；服务端合成回复也向托管本机播放。自动消息在右侧标记“托管回复”。当前未实现设备后台离线代答、逐设备提示词、外部工具执行或长记忆。

## 验证

Go模拟上游测试结构化结果解析、托管范围及退出取消、防自动消息循环；Go实际百炼调用及3.5B编译作为检查。仍需实机验证黄色状态、MCP开关、TTS音频和真人抢占，持续观察房间切换/重连及队列上限。

## 语音切换与当前验证边界

set_hosting的中文工具描述已明确：

- “切换为普通模式”“普通模式”“切回手动模式”“退出托管”“关闭自动回复”传enabled=false。
- “开启托管”“切换托管模式”“无人值守”“帮我自动回复对讲”传enabled=true。
- 普通模式是手动对讲，不是改变助手人设或回答风格；必须实际调用设置工具，不应只口头确认。
- 查询当前模式使用get_hosting，查询结果为已确认状态。设置返回true仅表示请求入队，不保证服务端接受。

联调时检查 `Assistant MCP tools/call: self.intercom.set_hosting` 和 `hosting_state` 成功响应。模型是否选择工具仍由小智服务端决定，工具描述不保证必然触发。

启用前先在控制台分别启用ASR、TTS及托管LLM服务，再通过设备MCP或模拟器开关启用当前设备。全局配置保存不等于设备进入托管。最新工具描述修改已通过差异检查，尚未单独重新编译；此前核心托管实现已通过Go race测试、真实百炼调用和3.5B编译，端到端自动代答仍需实机验收。
