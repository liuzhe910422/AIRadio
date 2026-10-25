# 对讲测试客户端

独立终端客户端，模块路径 `airadio/intercom/client`。它连接现有对讲 WebSocket，不修改服务端。

在 `client/` 目录启动：

```sh
go run .
```

上方是消息区，下方是命令输入。示例：

```text
connect ws://127.0.0.1:8080/ws device-1
join 1234
sendstart
sendtext 你好 世界
sendend
```

`connect` 的地址要写完整的 WebSocket URL。设备 ID 只含字母、数字、下划线和连字符，最长 32。需要 API key 时把它放在第三个参数。

两个刚连接的模拟设备默认在同一房间，可以直接互发。`sendstart` 被接受后要及时 `sendend`。服务端大约 35 秒内没有收到结束，会关掉这条连接。

`sendtext` 把文本放进语音二进制帧，可以在一次会话里发送多次。接收端把这些帧按顺序拼回文本，用来核对转发链路。`chat_message` 里的文字来自识别或房间通报，不代表帧内容。
