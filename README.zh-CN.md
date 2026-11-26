# maas-client-go

MaaS（Model as a Service）能力平台的 Go 客户端 SDK，覆盖文字识别（OCR）、语音合成（TTS）
与实时语音识别（ASR）。

[English](README.md)

---

## 当前状态

| 模块 | 状态 |
|---|---|
| 内核（配置、错误、封套、文件上传、传输层、请求 ID） | ✅ 已实现 |
| OCR（文档、卡证、票据、通用文字识别；同步 + 异步） | ✅ 已实现 |
| TTS（流式 PCM、WAV 封装） | ✅ 已实现 |
| ASR（WebSocket 会话、帧编解码、事件流） | ✅ 已实现 |

能力层已有单元测试与基于进程内服务端的集成测试覆盖；真实链路验证需要 API Key，
可直接运行 [examples](examples/) 下的示例程序。

## 为什么需要这个 SDK

封装 HTTP 是最简单的部分。真正需要 SDK 吸收掉的是这几件事：

- **同步与异步同构**：传文件、拿结果。平台是立刻返回还是建了任务让你轮询，不该由业务方关心。
- **错误可编程**：六类错误、稳定错误码、每次失败都带 `RequestID`，并附可直接展示的 `Hint`。
- **没有隐藏行为**：不读环境变量、不对计费类提交自动重试、不静默重连、不截断音频。
- **默认可追溯**：请求 ID 会贯穿业务日志、响应与平台侧日志。

## 安装

```bash
go get github.com/hupeng12345/maas-client-go
```

要求 Go 1.21+。

## 快速开始

```go
import (
	"context"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/ocr"
)

client, err := maas.New(
	maas.WithAPIKey(apiKey),
	maas.WithBaseURL(baseURL), // 可选：SDK 有默认地址，能力级还可单独覆盖
)
if err != nil {
	log.Fatal(err)
}

res, err := ocr.New(client).ParseDocument(context.Background(), maas.File("contract.pdf"))
if err != nil {
	log.Fatal(err)
}
fmt.Println(res.MarkdownContent)
```

## 三种能力

| | OCR | TTS | ASR |
|---|---|---|---|
| 传输 | HTTP multipart | HTTP JSON → 二进制流 | WebSocket 二进制帧 |
| 调用模型 | 提交 + 轮询 | 单次请求 | 长会话 |
| 成功判定 | `code` + `task_status` | `Content-Type` 为音频 | 终态包 + `status` |
| 取消 | 仅停止轮询 | 关闭流 | `Finish` 或断开 |

### OCR

```go
// 文档解析：提交、轮询、返回结果一步到位
res, err := ocr.New(client).ParseDocument(ctx, maas.File("a.pdf"), ocr.EnableTOC(true))

// 卡证识别：调用形态一致；平台有同步端点时不轮询
res, err := ocr.New(client).Recognize(ctx, ocr.CardIDCard, maas.File("id.png"))
```

### TTS

```go
audio, err := tts.New(client).Say(ctx, "你好", tts.WithVoice(voiceID), tts.WithSample(24000))
if err != nil {
	return err
}
defer audio.Close()

io.Copy(w, audio)                 // 边收边播
// 或 audio.WriteWAVFile("out.wav")
```

### ASR

```go
sess, err := asr.New(client).Start(ctx, asr.Config{
	Request: asr.Request{
		EnableNonstream: maas.Ptr(true),
		ShowUtterances:  maas.Ptr(true),
	},
})
if err != nil {
	return err
}
defer sess.Close()

go func() {
	for ev := range sess.Events() {
		if ev.IsFinal {
			fmt.Println(ev.Text)
		}
	}
}()

for chunk := range audioChunks {
	if _, err := sess.Write(chunk); err != nil {
		return err
	}
}

res, err := sess.Finish(ctx) // 发结束包并等终态
```

ASR 开箱可用：WebSocket 路径由 SDK 内置，域名默认取客户端 endpoint。可选覆盖有两级：

```go
sess, err := asr.New(client).Start(ctx, asr.Config{
	// Host: "https://other-domain.example.com",      // 换域名，路径不变
	// URL:  "wss://other.example.com/custom/path",   // 整条覆盖
	Auth: asr.Auth{ /* 见包文档 */ },
})
```

## 错误处理

所有失败都是 `*maas.Error`，带分类，无需匹配字符串：

```go
switch {
case maas.IsAuth(err):         // 密钥缺失、无效或过期
case maas.IsInsufficient(err): // 余额不足
case maas.IsRateLimited(err):  // 按 RetryAfter 退避
case maas.IsTaskFailed(err):   // 异步任务失败终态
}
```

分类：config（配置）、local（本地校验，请求未发出）、transport（网络）、
protocol（响应不符合契约）、api（业务码）、task（异步任务失败）。

## 请求 ID 与链路追踪

```go
ctx = maas.WithRequestID(ctx, "order-123") // 可选，不传则自动生成

res, err := ocr.New(client).ParseDocument(ctx, maas.File("a.pdf"))
if err != nil {
	var e *maas.Error
	if errors.As(err, &e) {
		log.Printf("失败，请把 request_id=%s 提供给平台", e.RequestID)
	}
	return err
}
log.Printf("成功 request_id=%s 耗时=%s", res.Meta.RequestID, res.Meta.Duration)
```

服务端回传的 ID 优先于本地生成的值。ASR 的 `X-Api-Request-Id` 要求 UUID；
若你传入的不是 UUID，SDK 会为协议另生成一个，同时仍把你的值作为 `X-Request-ID` 发送。

## 日志

默认静默（写入 `io.Discard`）。打开或替换实现：

```go
client, _ := maas.New(
	maas.WithAPIKey(apiKey),
	maas.WithLogger(logit.NewSimple(os.Stderr)),
)
```

凭证不会进入日志：`Authorization`、`Cookie` 与查询串中的凭证都会被传输层脱敏。

## 接管传输层

```go
// 方式一：完全接管
client, _ := maas.New(maas.WithAPIKey(apiKey), maas.WithHTTPClient(&http.Client{Transport: myRT}))

// 方式二：在内置链上追加
client, _ := maas.New(maas.WithAPIKey(apiKey), maas.WithTripperOptions(tripper.Retry(3)))
```

两者互斥：接管了 `http.Client`，中间件链就由你负责。

## 包结构

| 包 | 用途 |
|---|---|
| `maas` | Client、配置、传输层、错误、请求 ID、文件 |
| `ocr` | 文档、卡证、票据与通用文字识别 |
| `tts` | 语音合成、流式音频与 WAV 封装 |
| `asr` | 流式识别：帧编解码、会话、事件 |
| `logit` | 结构化日志：默认实现与 `Logger` 接口 |
| `tripper` | HTTP 传输与日志/重试中间件链 |

所有能力遵循同一套配置模型：默认 endpoint + 客户端级覆盖（`WithBaseURL`）+ 调用级路径覆盖
（`ocr.WithEndpoint`、`tts.WithEndpoint`、`asr.Config.URL`）。

`logit` 与 `tripper` 刻意公开：在你自己的服务里复用它们，就能与 SDK 保持同一套日志格式、
重试语义与凭证脱敏规则。

## 示例

[examples/](examples/) 下有六个可直接运行的程序：

```bash
go run ./examples/ocr-document -api-key "$KEY" -file ./contract.pdf
go run ./examples/tts-wav      -api-key "$KEY" -model <model> -voice <voice> -text "hello"
go run ./examples/asr-file     -api-key "$KEY" -url wss://your-host/... -file ./sample.wav
```

## 设计文档

公开文档就是本 README 与包级 godoc。内部设计笔记不存放在本仓库中。

## 安全约定

- API Key 仅限服务端使用，不要下发给浏览器或移动端，也不要提交到仓库。
- SDK 不读取任何环境变量，密钥由调用方从自己的配置源显式传入。
- 本仓库不包含任何内部地址、凭证或租户数据；如发现遗漏请提 issue。

## 许可证

MIT，见 [LICENSE](LICENSE)。
