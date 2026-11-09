# 增量同步协议可执行契约

本目录是 [`Docs/协议/增量同步协议-v1.md`](../Docs/协议/增量同步协议-v1.md) 的可执行部分：JSON Schema、示例报文、SSE 示例帧、零依赖 mock server 和自检脚本。

## 运行

要求 Node.js ≥ 18（仅用内置模块，无需 `npm install`）。

```bash
node contracts/verify.mjs
```

输出逐项 `PASS`，末尾为通过数量，退出码 0/1。

启动 mock：

```bash
node contracts/mock-server.mjs --port 8787
# 或 MOCK_PORT=8787 node contracts/mock-server.mjs
```

mock 只监听 `127.0.0.1`，数据固定在 `fixtures/mock-state.json`。

## mock 认证与控制端点

| 用途 | 请求头 |
| --- | --- |
| 库同步 | `Authorization: Bearer mock-key` 或 `X-API-Key: mock-key` |
| 实时（SSE/POST sync） | `Cookie: session=mock-session` |
| 实时 POST | 额外 `X-CSRF-Token: mock-csrf` |

| 控制端点 | 作用 |
| --- | --- |
| `POST /__mock/commit` | 修订号 +1，修改一条曲目/歌单/目录/角标，并向所有 SSE 连接广播 `change` |
| `POST /__mock/expire-cursors` | 清空全部实时基线游标，下次 sync 返回 `reset` |
| `POST /__mock/expire-auth` | 向所有 SSE 连接发送 `expired` 并关闭 |
| `POST /__mock/reconnect` | 向所有 SSE 连接发送 `reconnect` 并关闭 |
| `GET /__mock/state` | 返回当前 revision 与角标数 |

## 文件

| 路径 | 内容 |
| --- | --- |
| `schemas/realtime-view.schema.json` | 实时视图描述符 |
| `schemas/realtime-sync-request.schema.json` | `POST /community/sync` 请求 |
| `schemas/realtime-sync-response.schema.json` | 实时增量响应（按段约束行类型） |
| `schemas/sse-frame.schema.json` | 解析后的 SSE 帧 |
| `schemas/library-sync-response.schema.json` | 协议 1/2 库同步响应（`oneOf`） |
| `schemas/library-sync-tracks-request.schema.json` | 批量曲目解析请求 |
| `schemas/library-sync-tracks-response.schema.json` | 批量曲目解析响应 |
| `schemas/common.schema.json` | Track/Playlist/Invitation/目录摘要/错误 |
| `schemas/error.schema.json` | 错误信封 |
| `examples/` | 可直接用于客户端单测的报文和 `sse-session.txt` |
| `fixtures/mock-state.json` | mock 固定数据 |
| `lib/validate.mjs` | JSON Schema 2020-12 子集校验器 |
| `lib/sse.mjs` | SSE 帧解析/序列化 |
| `mock-server.mjs` | 参考 mock |
| `verify.mjs` | 契约自检 |

## Go 侧结构校验

`server/internal/music/protocol_contract_test.go` 会读取 `contracts/examples/*.json`，用 `DisallowUnknownFields` 解码到真实的 `SyncResponse`、`realtimeReply`、`realtimeView`、`Playlist`、`PlaylistInvitation` 等类型，并检查 SSE 示例事件集合：

```bash
go test ./server/internal/music -run TestProtocolContract -count=1
```

这样示例与 Go JSON tag 保持一致；只改文档/示例而不改代码也会被发现。

## Schema 子集

`lib/validate.mjs` 支持：`$ref`（本地与相对文件）、`type`、`required`、`properties`、`additionalProperties`、`items`、`enum`、`const`、`pattern`、`minLength`/`maxLength`、`minimum`/`maximum`、`minItems`/`maxItems`、`uniqueItems`、`oneOf`/`anyOf`/`allOf`。`format` 仅作注解，不校验。使用其他关键字会在自检时报 `unsupported keyword`。

如需用标准校验器复核，可任选 ajv 等实现；schema 均为 draft 2020-12，未使用自定义扩展。

## 不在本目录的范围

- 生产服务器实现（`server/`）。
- 上传续传、标签同步、备份/更新协议。
- 兼容性矩阵以外的版本协商（当前只有 `protocol=2` 与 `/health`）。
