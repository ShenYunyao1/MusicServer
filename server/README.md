# 音乐服务器 0.6.3

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

- 启动扫描音频目录，包括子目录；读取 MP3/FLAC/Ogg/Opus/MP4/WAV 的标题、作者、专辑、风格、备注及支持的内嵌歌词；关联同路径同名 `.lrc`、`.elrc`、`.txt` 歌词，支持 UTF-8/BOM、带 BOM 的 UTF-16 和 GB18030。不请求外部歌词平台。
- 没有音频标签时按文件名 `标题 - 歌手` 提取标题和作者，专辑与风格保持为空，不使用上传文件夹名。信息可在管理页修改，人工修改在重扫后保留。
- 上传页面支持多文件、文件夹和拖放目录；MP3 添加后立即在浏览器预览 ID3v2 标签、内嵌歌词和封面，并展示已选择的外置附件；其他格式及本地未支持的 MP3 标签帧等待服务端确认。本地预览不上传音频，点击“上传校验”后才暂存，由服务端最终解析、校验和去重，确认后才入库；已有暂存可“重新校验”。可以修改预览字段，展开“歌词与注释”查看和编辑文本，手动指定歌词/封面附件；相同 SHA-256 的音频使用已存在曲目，不覆盖其元数据。
- 编辑曲目元数据；创建、编辑、删除歌单，并在歌单详情中添加、移出曲目、编辑成员备注和拖拽排序。曲目列表显示所属用户歌单并支持筛选孤立曲目，不把系统 `All music` 计作歌单归属。
- 外置歌词按同名 `.lrc`、`.elrc`、`.txt` 自动关联，来源返回为 `sidecar`；封面支持内嵌图片、同名图片、精确匹配作者和专辑的 `AlbumCover - 作者 - 专辑` 图片，以及同目录 `cover`、`folder`、`albumcover` 图片，返回 `cover_id`、`cover_source` 和 `cover_mime`。详细规则见 `Docs/外置歌词与封面处理.md`。
- 曲目返回 `duration_ms`、`bitrate_kbps`、`sample_rate`、`channels`、`bits_per_sample` 等技术参数；封面可通过 `/api/v1/covers/{cover_id}` 获取。
- 删除曲目只移除库记录并保留原文件，重扫不会重新加入已移除记录。第一版尚未提供恢复移除记录的界面。
- SQLite 文件保存曲目、歌单、顺序与注释、API Key 授权、人工覆盖、排除记录、封面资产、设置、暂存上传和调用统计；库变更使用事务，失败会回滚内存和数据库快照。当前 SQLite schema v1 自动升级为 v2；不读取旧 JSON，`.json` 数据路径会直接拒绝。
- SHA-256 内容哈希、带认证的媒体下载、HTTP Range 和 ETag。
- 修订号同步：有变化时返回完整快照；没有变化时返回空数组。不是逐项增量同步。
- 设置页可修改 ffprobe 路径、上传限制、外置歌词/封面扫描开关，保存后立即用于后续扫描/上传；已有曲目需要重扫才能更新。音乐目录由 `-media-root` 指定，在页面只读。
- 调用统计按 Key 记录请求数、成功/失败、传输字节和耗时，并持久化；包括启动管理员与匿名认证失败，健康检查不计入。当前并发数只存在于当前服务进程。

上传限制默认 512 MiB（含 multipart 请求），可在服务器设置页调整；元数据请求限制 2 MiB；内嵌标签读取预算 32 MiB、单条歌词上限 2 MiB。同名歌词读取上限 2 MiB。上传按照文件扩展名接收，当前不验证完整音频解码有效性。

技术参数优先调用设置的 ffprobe、环境变量 `MUSIC_SERVER_FFPROBE` 或 PATH 中的 ffprobe；没有 ffprobe 时使用 WAV、FLAC、MP3 的有限解析。读取不到的参数保持为空，MP3 没有定义 PCM 位深时不会显示伪造的位深。

浏览器本地标签解析使用随服务端打包的 jsmediatags 3.9.7，不依赖 CDN。BSD-3-Clause 许可见 `internal/music/vendor/jsmediatags-LICENSE.md`，本地 UTF-16 文本解码补丁说明见 `internal/music/vendor/README.md`。

## Key 权限与并发

持久化 Key 默认角色为播放器、无歌单授权、最大并发请求数为 1。角色、scopes、歌单授权分别检查；管理员写入必须同时具有管理员角色和接口需要的 scope。显式空 scopes 表示禁用全部功能。启动管理员 Key 可用于恢复管理权限，不受持久化 Key 的并发上限约束。

| Scope | 功能 |
| --- | --- |
| `library:read` | 同步、歌单、曲目读取 |
| `media:read` | 音频和封面下载 |
| `library:write` | 曲目/歌单编辑、排序、扫描 |
| `uploads:write` | 暂存预览、确认、删除暂存与直接上传 |
| `keys:manage` | API Key 管理 |
| `settings:manage` | 设置读取和修改 |
| `stats:read` | 调用统计 |

超出并发上限返回 `429 KEY_CONCURRENCY_LIMIT`，缺少 scope 返回 `403 SCOPE_DENIED`。并发上限统计正在处理的请求，包括音频传输，不等同于独占一个客户端；设备登记与心跳租约尚未实现。管理网页使用串行请求队列，适配并发上限为 1 的管理员 Key。

## API

除 `GET /` 与健康检查外，设置了密钥时必须携带 `X-API-Key` 或 `Authorization: Bearer ...`。

健康检查响应包含 `version` 字段，可用于播放器判断服务端 API 版本。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/health` | 状态与修订号 |
| GET | `/api/v1/sync?since=修订号` | 检查更新，首次可省略 since |
| GET | `/api/v1/tracks` | 全部曲目 |
| GET / PATCH / DELETE | `/api/v1/tracks/{id}` | 查询、编辑、移除曲目 |
| PUT | `/api/v1/tracks/{id}/cover` | multipart 字段 `cover` 指定封面 |
| PUT | `/api/v1/tracks/{id}/lyrics` | multipart 字段 `lyrics_file` 指定歌词 |
| POST | `/api/v1/uploads` | multipart 字段 `file` 上传音频 |
| POST | `/api/v1/uploads/stage` | `file`，可选 `lyrics_file`/`cover` 和元数据，返回解析预览 |
| GET / DELETE | `/api/v1/uploads/stage/{id}` | 查询或删除暂存上传 |
| GET | `/api/v1/uploads/stage/{id}/cover` | 暂存封面预览 |
| POST | `/api/v1/uploads/stage/{id}/commit` | 可选 JSON 元数据，确认入库；返回 `track` 与 `duplicate` |
| GET / POST | `/api/v1/playlists` | 列出、创建歌单 |
| GET / PATCH / PUT / DELETE | `/api/v1/playlists/{id}` | 歌单详情、修改元数据、兼容替换、删除 |
| POST | `/api/v1/playlists/{id}/tracks` | 批量添加歌单成员 |
| PATCH / DELETE | `/api/v1/playlists/{id}/tracks/{trackID}` | 修改成员备注、移出歌单（不删除曲目） |
| PUT | `/api/v1/playlists/{id}/tracks/order` | 排序成员，需提交预期成员列表 |
| GET / HEAD | `/api/v1/media/{id}` | 音频下载，支持 Range |
| GET / HEAD | `/api/v1/covers/{id}` | 获取封面图片 |
| POST | `/api/v1/admin/scan` | 重新扫描目录 |
| PUT | `/api/v1/playlists/order` | 保存歌单显示顺序 |
| GET / PATCH | `/api/v1/admin/settings` | 读取、修改服务器设置 |
| GET | `/api/v1/admin/stats` | 查看 API 调用统计 |
| GET / POST | `/api/v1/auth/keys` | 列出/创建 Key |
| PATCH / DELETE | `/api/v1/auth/keys/{id}` | 修改/撤销 Key |

歌单字段：`id`、`name`、`description`、按顺序排列的 `track_ids`、以曲目 ID 为键的 `track_notes`。
`library` 是自动维护的全部曲目歌单，不能删除。曲目 PATCH 只接受 `title`、`artist`、`album`、`genre`、`comment`、`lyrics` 字符串字段。
歌词可以随音频暂存上传，也可以在曲目右键菜单中更换歌词文件，或用 PATCH 写入文本。浏览器无法自动读取没有被选择的相邻文件：需要同时选择歌曲和资源文件，选择文件夹，或手动指定附件。暂存接口 `cover_source` 可为 `manual`（默认）或 `external`；自动匹配的 `external` 不覆盖内嵌封面。上传歌词保存为媒体目录内同名 `.lrc`，自动匹配封面保存为同名图片，手工指定封面保存为数据库资产和覆盖值，重新扫描不会丢失。

## 构建与检查

```powershell
.\build.ps1
```

脚本使用工作机器已验证的独立 Go 工具链，并运行测试、vet 和构建。也可以自行使用 `go test ./...`、`go vet ./...`、`go build -o musicserver.exe ./cmd/musicserver`。

## 程序更新

管理员在“服务器设置”填写本项目仓库地址 `https://github.com/ShenYunyao1/MusicServer`。服务器在启动后、保存设置后，以及之后每 6 小时检查一次该仓库的最新正式版 Release；有更高版本时自动下载当前系统对应的升级包。管理页显示当前版本、最新版本、下载结果和文件路径，也可手动点击“检查更新”。未配置仓库时不会访问 GitHub。

私有仓库需要在启动服务的进程环境中设置 `MUSIC_SERVER_GITHUB_TOKEN`，该令牌须具备该仓库的读取权限；令牌不保存在服务器设置或数据库。已经通过 GitHub CLI 登录的本机可在启动服务前执行：

```powershell
$env:MUSIC_SERVER_GITHUB_TOKEN = (& gh auth token)
.\musicserver.exe -api-key 'your-admin-key'
```

发布包资产名固定为 `musicserver-windows-amd64.zip` 和 `musicserver-linux-amd64.tar.gz`。Release 标签使用 `v主版本.次版本.修订版本`，例如 `v0.6.3`。GitHub API 必须返回资产的 `sha256:` digest；服务器只接受 HTTPS、校验文件大小与 SHA-256，并将包原子写入数据库文件旁的 `updates/版本/` 目录。下载成功后需要停服、备份数据库与程序，并由管理员解压替换可执行文件再重启；服务器不会自动替换自身或执行下载包。

维护者在 Windows 上运行 `./package-release.ps1`，完成测试、vet、Windows/Linux 构建并生成两个资产包；发布前核对脚本输出的 SHA-256。运行中的旧程序不会因新包下载而中断。

本版仍是单进程原型：没有转码、外部歌词平台匹配、OpenSubsonic 兼容、后台目录监听和完整管理操作审计。扫描会读取所有音频计算哈希并占用媒体库锁，较大媒体库的重扫可能较慢；暂存文件保留 30 分钟，启动及每分钟清理过期文件和无记录的服务暂存文件。
