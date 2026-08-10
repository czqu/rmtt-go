# RMTT-go — RMTT 协议 Go 实现

Remote Message Telemetry Transport (RMTT) 协议的 Go 客户端与服务端库。API 设计借鉴 [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang)，熟悉 MQTT 的开发者可快速上手。

支持 **六种传输**：`tcp://` `kcp://` `tls://` `quic://` `ws://` `wss://`，客户端与服务端双向互通。

## 安装

```bash
go get -u github.com/czqu/rmtt-go
```

## 客户端

### 最小示例

```go
package main

import (
	"log"
	"time"

	"github.com/czqu/rmtt-go/client"
)

func main() {
	opts := client.NewClientOptions()
	opts.AddServer("tcp://127.0.0.1:18883")
	opts.SetCredential("dev-001")
	opts.SetConnectTimeout(5 * time.Second)
	opts.AutoReconnect = true

	c := client.NewClient(opts)
	c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
		log.Printf("received: %s", string(msg.Payload()))
	})

	if token := c.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("connect failed: %v", token.Error())
	}

	token := c.Push("hello")
	token.Wait()

	c.Disconnect(1000)
}
```

### 传输选择

`AddServer` 通过 URL scheme 选择传输，等价于 `tcp://` 的裸地址会自动补全：

```go
opts.AddServer("tcp://127.0.0.1:18883")    // TCP
opts.AddServer("kcp://127.0.0.1:18883")    // KCP over UDP
opts.AddServer("tls://127.0.0.1:18884")    // TLS over TCP，需 SetTlsConfig
opts.AddServer("quic://127.0.0.1:18885")   // QUIC (TLS 1.3 内置)
opts.AddServer("ws://127.0.0.1:18886")     // WebSocket
opts.AddServer("wss://127.0.0.1:18887")    // WSS，需 SetTlsConfig
```

TLS 相关传输需要提供证书配置：

```go
opts.SetTlsConfig(&tls.Config{InsecureSkipVerify: true}) // 仅测试环境
```

### 主要选项

| 选项 | 默认值 | 说明 |
|------|--------|------|
| `SetCredential(id)` | — | CONNECT 携带的凭证，服务端据此认证并提取设备身份 |
| `SetHeartbeat(d)` | 10s | 心跳间隔（服务端可在 CONNACK 中回推建议值） |
| `SetConnectTimeout(d)` | 30s | 连接握手超时 |
| `SetWriteTimeout(d)` | 30s | 写超时 |
| `AutoReconnect` | true | 断线自动重连（指数退避 + 抖动） |
| `ConnectRetry` | true | 首次连接失败重试 |
| `SetReconnectBase(d)` | 1s | 重连退避基数 |
| `SetReconnectJitter(j)` | 0.25 | 重连退避抖动 |

### 回调

```go
c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) { /* 收到 PUSH */ })
opts.OnConnectionLost = func(c client.Client, err error) { /* 连接丢失 */ }
opts.OnReconnecting = func(c client.Client, o *client.ClientOptions) { /* 正在重连 */ }
opts.OnConnectAttempt = func(server *url.URL, tlsCfg *tls.Config) *tls.Config { /* 每次连接尝试 */ }
```

### 日志

client 与 server 库都提供 paho 风格的可插拔 `Logger`（接口即 `log.Logger` 的 `Println/Printf`，所以 `*log.Logger` 直接可用）。包级变量默认是 `NOOPLogger`（完全静默）。

```go
client.SetLogger(log.New(os.Stdout, "", 0))        // 一个 logger 用于全部级别
client.SetDebugLogger(customLogger)                 // 也可按级别单独设置
// server 包同样提供 server.SetLogger / SetErrorLogger / SetWarnLogger / SetInfoLogger / SetDebugLogger
```

分级约定：`DEBUG`=心跳收发/adaptive 迁移；`INFO`=CONNACK serverKp/固定心跳配置/adaptive 参数/设备连接；`WARN`=重连/keepalive 超时/adaptive 丢心跳；`ERROR`=握手失败/异常。日志行形如 `[client]   ...` / `[net]     ...`。需要抑制 DEBUG 时，用只输出指定级别（如带 `[INFO]` 前缀过滤的 logger）即可。

## 服务端

### 最小示例

```go
package main

import (
	"log"

	"github.com/czqu/rmtt-go/server"
)

type auth struct{}

func (a *auth) Authenticate(credential string) (string, bool) {
	if len(credential) > 0 {
		return credential, true // 凭证即设备 ID
	}
	return "", false
}

func main() {
	opts := server.NewServerOptions()
	opts.SetPort(18883)
	opts.SetAuthenticator(&auth{})
	opts.SetMessageHandler(func(deviceID string, payload []byte) {
		log.Printf("device %s sent: %s", deviceID, string(payload))
	})
	opts.SetConnectionListener(&connListener{})

	srv := server.NewServer(opts)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

### 多传输监听

通过 `AddListener` 注册额外传输，单进程可同时监听多种传输（TCP 与 KCP 端口空间独立）：

```go
opts := server.NewServerOptions()
opts.AddListener(server.NewTCPListener(":18883"))
opts.AddListener(server.NewKCPListener(":18883"))       // UDP，可与 TCP 同端口
opts.AddListener(server.NewTLSListener(":18884", tlsCfg))
opts.AddListener(server.NewQUICListener(":18885", tlsCfg))
opts.AddListener(server.NewWSListener(":18886", "/ws"))
opts.AddListener(server.NewWSSListener(":18887", "/ws", tlsCfg))
```

> 未调用 `AddListener` 时，默认在 `SetPort` 指定端口监听单个 TCP。

### 服务端接口

```go
type Server interface {
	ListenAndServe() error
	ListenAndServeContext(ctx context.Context) error
	Push(deviceID string, payload []byte) error // 下行推送
	Kick(deviceID string, reason byte) error    // 强制断开
	Close() error
}
```

### 扩展点

| 接口 | 职责 |
|------|------|
| `Authenticator` | 认证：`Authenticate(credential) (deviceID, ok)`，由应用层注入 |
| `MessageHandler` | 处理设备上行 PUSH |
| `ConnectionListener` | 建连/断连事件回调 |
| `KeepalivePolicy` | 心跳协商策略（默认值见 `DefaultKeepalivePolicy()`） |

## 目录结构

```
client/   客户端实现（连接、心跳、重连、Token）
server/   服务端实现（连接管理、路由、六传输 Listener、Keepalive）
codec/    RMTT 协议编解码（CONNECT/CONNACK/PUSH/PINGREQ/PINGRESP/DISCONNECT）
cmd/      可直接运行的最小 client / server 示例
```

## 测试

```bash
go test ./...
```

完整跨栈回归（Go↔Java 交叉矩阵）见 `rmtt-example/run.sh`。
