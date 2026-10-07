# 音乐服务器初版

Go 1.26+，使用 SQLite 和音频标签依赖。提供 HTTP API 和内置中文 Web 管理页；Windows 和 Linux 均可构建运行。

## 启动

```powershell
cd D:\Develop\Study\MusicPlayer\server
.\musicserver.exe -addr 127.0.0.1:18080 -media-root 'D:\Download\音乐下载\MusicPlayer目录' -data 'D:\Develop\Runtimes\MusicServerTest\library.sqlite' -api-key dev-test-key
```

浏览器打开 `http://127.0.0.1:18080/`，输入相同 API Key，连接后即可管理曲目和歌单。
以上密钥仅用于本机开发测试。实际部署使用自己的密钥，可通过 `MUSIC_SERVER_API_KEY` 环境变量提供。默认监听 `127.0.0.1:8080`。

### 管理员 Key

启动参数 `-api-key`（或环境变量 `MUSIC_SERVER_API_KEY`）是稳定的管理员 Key。它不会因为重启改变，也不依赖数据库中的 Key 记录；管理员可以用它登录 Web 管理页并创建、授权或撤销播放器 Key。

如需在本机启动日志中显示该 Key，可显式添加 `-show-api-key`：

```powershell
.\musicserver.exe -api-key 'change-this-admin-key' -show-api-key
```

日志中的完整 Key 属于敏感信息，生产部署不要启用 `-show-api-key`，也不要把日志提交或公开。未配置 `-api-key` 且数据文件中没有持久化 Key 时，服务会以无需认证的开发模式启动，并在日志中发出警告。

远程部署时监听 `0.0.0.0:8080`，配置 API Key，并通过反向代理提供 HTTPS。数据文件和媒体目录应持久化保存；备份时停止服务，再复制数据文件及媒体目录。单个数据文件只允许一个服务进程使用。

## 当前功能

- 启动扫描音频目录，包括子目录；读取 MP3/FLAC/Ogg/Opus/MP4/WAV 的标题、作者、专辑、风格、备注及支持的内嵌歌词；关联同路径同名 `.lrc`、`.elrc`、`.txt` 歌词，支持 UTF-8（含 BOM）。不请求外部歌词平台。
- 没有音频标签时按文件名 `标题 - 歌手` 提取标题和作者，专辑与风格保持为空，不使用上传文件夹名。信息可在管理页修改，人工修改在重扫后保留。
- 上传音频，编辑标题、歌手、专辑、风格、注释和歌词；创建、编辑、删除歌单，管理曲目顺序、歌单描述和歌单内曲目注释。API 支持顺序，Web 页暂不提供拖拽排序。
- 删除曲目只移除库记录并保留原文件，重扫不会重新加入已移除记录。第一版尚未提供恢复移除记录的界面。
- SQLite 文件保存曲目、歌单、曲目顺序与注释、API Key 授权、人工覆盖和排除记录；每次写入使用事务，失败会回滚内存和数据库快照。新版本不读取或导入旧 JSON，`.json` 数据路径会直接拒绝；需要从空的 `.sqlite` 数据库重新扫描媒体目录。
- SHA-256 内容哈希、带认证的媒体下载、HTTP Range 和 ETag。
- 修订号同步：有变化时返回完整快照；没有变化时返回空数组。不是逐项增量同步。

上传限制 512 MiB（含 multipart 请求）；元数据请求限制 2 MiB；内嵌标签读取预算 32 MiB、单条歌词上限 2 MiB。同名歌词读取上限 2 MiB。上传按照文件扩展名接收，当前不验证完整音频解码有效性，也不保存封面和技术参数。

## API

除 `GET /` 与健康检查外，设置了密钥时必须携带 `X-API-Key` 或 `Authorization: Bearer ...`。

健康检查响应包含 `version` 字段，可用于播放器判断服务端 API 版本。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/health` | 状态与修订号 |
| GET | `/api/v1/sync?since=修订号` | 检查更新，首次可省略 since |
| GET | `/api/v1/tracks` | 全部曲目 |
| GET / PATCH / DELETE | `/api/v1/tracks/{id}` | 查询、编辑、移除曲目 |
| POST | `/api/v1/uploads` | multipart 字段 `file` 上传音频 |
| GET / POST | `/api/v1/playlists` | 列出、创建歌单 |
| GET / PUT / DELETE | `/api/v1/playlists/{id}` | 歌单详情、替换、删除 |
| GET / HEAD | `/api/v1/media/{id}` | 音频下载，支持 Range |
| POST | `/api/v1/admin/scan` | 重新扫描目录 |

歌单字段：`id`、`name`、`description`、按顺序排列的 `track_ids`、以曲目 ID 为键的 `track_notes`。
`library` 是自动维护的全部曲目歌单，不能删除。曲目 PATCH 只接受 `title`、`artist`、`album`、`genre`、`comment`、`lyrics` 字符串字段。
歌词上传：先上传音频，然后在管理页编辑曲目的 lyrics，或用 PATCH 写入 LRC 文本。

## 构建与检查

```powershell
.\build.ps1
```

脚本使用工作机器已验证的独立 Go 工具链，并运行测试、vet 和构建。也可以自行使用 `go test ./...`、`go vet ./...`、`go build -o musicserver.exe ./cmd/musicserver`。

本版为单用户原型：没有多账号权限、scopes/设备租约/并发限制的实际执行、转码、外部歌词匹配、OpenSubsonic 兼容、后台目录监听和管理操作审计。歌单拖拽排序尚未持久化，扫描会读取所有音频计算哈希，较大媒体库的重扫可能较慢。
