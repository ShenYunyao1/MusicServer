# 音乐服务器 0.9.0

Go 1.26+，SQLite schema 4，HTTP API `/api/v2`，内置中文管理页。支持 Windows 和 Linux。

固定产物路径、运行目录布局、编译打包与 Git 提交规则见 [服务器编译与运行目录规范](../Docs/服务器编译与运行目录规范.md)。

本版采用账号密码登录和账号所属的播放器 Key。schema 1–3、旧启动管理员 Key、匿名管理员模式、JSON 文件及 `/api/v1` 均不支持，不执行旧库迁移。使用新的数据库路径初始化；旧库原样保留。重新扫描不会恢复旧歌单和人工覆盖。

## 启动

Windows 使用两个批处理脚本：`build.bat` 执行测试、`go vet` 和编译，固定生成 `build/musicserver.exe`，成功后明确输出 `BUILD SUCCEEDED` 和程序路径；失败输出 `BUILD FAILED` 并返回非零状态。`start.bat` 固定启动该程序，首次启动引导设置管理员密码。从其他目录调用也可；源码更新后先停服再编译。

两个 BAT 在正常结束或出错后默认暂停，按任意键再关闭，避免双击时错误信息一闪而过；暂停后仍返回原退出码。如果提示数据库已占用，说明已有实例正在使用该数据库，请使用现有服务或先停服再启动。自动化调用可设置环境变量 `MUSIC_SERVER_NO_PAUSE=1` 跳过暂停；PowerShell 下可使用 `$env:MUSIC_SERVER_NO_PAUSE='1'`，CMD 下使用 `set MUSIC_SERVER_NO_PAUSE=1`。

```bat
cd server
call build.bat
call start.bat
```

首次启动自动在终端隐藏输入并确认密码，账号名为 `admin`，密码至少 12 个字符、最多 1024 字节；不设置默认密码。后续启动直接进入服务。也可在 `server` 目录提前执行 `build\musicserver.exe admin init`；`--data` 和 `--username` 仅用于覆盖默认值，均可省略。自定义用户名为 3–64 个 ASCII 字母、数字、点、短横线或下划线，首字符为字母或数字，忽略大小写。

程序默认监听 `127.0.0.1:8080`，媒体库为 **musicserver.exe 所在目录及其子目录，即 build/**，数据库为 `build/data/library-v4.sqlite`，无需显式传参。音频可直接放在程序旁，建议放入 `build/media/`。数据库目录、`.staging` 和 `.server-archive` 不参与媒体扫描。上传音频保存在 `build/uploads`，备份和更新暂存在 `build/data` 下。源码目录中的音频不会被默认扫描。

```text
server/
  cmd/ internal/ scripts/ tests/ deploy/   源码、脚本和测试
  build.bat build.ps1 start.bat            固定构建与启动入口
  package-release.ps1                     可选发布打包入口
  build/                                  Git 整体忽略
    musicserver.exe                       Windows amd64 固定产物
    musicserver                           Linux amd64 交叉编译产物（可选）
    data/ media/ uploads/ .staging/        运行数据，按需创建
    packages/                             发布压缩包（可选）
```

每次编译更新同一位置、同一名称的程序，保留运行数据，不生成带日期或版本后缀的可执行文件；不要将整个 `build/` 作为临时编译缓存自动清空。Git 只管理源码、脚本、测试和文档。构建嵌入 Go 的 VCS 信息，启动日志和 `/api/v2/health` 返回 `commit` 和 `modified`，用于辨认源码提交及构建时是否存在未提交修改；不在 Git 仓库内编译时提交号为 `unknown`。

当前处于开发阶段，数据仅作测试，后续升级不提供旧数据格式兼容或迁移。需要重置时停服后删除 `build/data/`，下次启动重新设置管理员账号；媒体会重新扫描，但原 UUID、歌单和人工修改不保留。日常编译不会自动执行重置。

默认路径和显式传入的相对路径都以 **程序所在目录** 为基准，不受终端当前目录影响。`start.bat` 支持全部可选参数，例如 `-media-root "D:\Music"`、`-data` 和 `-no-scan`；只有使用自定义数据库时，初始化/重置才需要传入相同的 `--data`。按 Ctrl+C 停止服务。

`build.bat` 自动查找现有 Go 安装，也可传入 `-GoRoot "C:\Program Files\Go"`。Windows 固定为 amd64；`build.bat -Target linux` 将 Linux amd64 程序写入同一 `build/` 目录，名称固定为 `musicserver`。依赖 ffprobe 的上传校验仍要求安装 ffprobe，见下文；远程部署仍需 HTTPS 和外部地址配置，本机默认使用无需这些参数。

也可直接使用 Go 和 PowerShell 启动：

```powershell
cd server
go build -trimpath -buildvcs=auto -o ./build/musicserver.exe ./cmd/musicserver
.\build\musicserver.exe
```

浏览器打开 `http://127.0.0.1:8080/`，输入账号密码登录。管理页支持修改密码、查看和撤销登录会话、创建和撤销播放器 Key。Key 是播放器凭证，不能用于管理接口。首次启动若处于无交互的后台环境，退出并提示先在终端初始化，不创建默认密码。

密码采用 Argon2id，独立 16 字节盐、19 MiB 内存、2 次迭代、并行度 1；数据库只保存密码哈希和会话令牌哈希。Cookie 使用 HttpOnly、SameSite=Strict，HTTPS 部署启用 Secure。浏览器写请求验证 Origin/Referer 和 `X-CSRF-Token`；登录验证来源，失败提示统一，账号每分钟最多 5 次尝试、IP 每分钟 30 次，最多 2 个并发密码哈希任务。成功登录重置该账号的尝试计数。

会话空闲 2 小时、绝对 7 天过期；后台轮询不续期，真实用户活动限频续期。刷新页面保持登录，服务重启撤销全部旧会话，播放器 Key 不受重启影响。改密撤销全部浏览器会话，普通改密不撤销播放器 Key；页面可显式撤销我的全部 Key。

远程部署通过 HTTPS 反向代理，并配置 `-public-url https://music.example.com` 或 `MUSIC_SERVER_PUBLIC_URL`。非回环监听必须配置 HTTPS 外部地址；HTTP 配置仅允许监听地址和外部主机均为回环的本机开发环境。服务不信任任意转发头，代理后的登录 IP 限流以代理连接地址计。建议后端仅绑定回环或受限内部接口。

忘记密码时停服后在终端执行（可选 `--revoke-keys` 同时撤销该账号播放器凭证）：

```bat
build\musicserver.exe admin reset-password
```

服务和本机管理命令共用 OS 文件锁，服务运行时拒绝初始化/重置同一库，进程退出时自动释放锁。初始化和重置要求真实交互终端，不接受密码参数或重定向密码输入。

启动自动开始后台快速扫描，`-no-scan` 可关闭。Ctrl+C 或 SIGTERM 会取消扫描、停止更新任务并尝试等待 HTTP 请求退出。数据库和媒体目录应放在持久存储中，一个数据库只运行一个服务进程。

上传要求安装 ffprobe：在设置中指定 `ffprobe_path`，或配置 `MUSIC_SERVER_FFPROBE`，或将 ffprobe 加入 PATH。没有可用探测工具时上传校验失败；本地目录扫描仍可使用有限的 WAV/FLAC/MP3 参数解析。

## 曲目身份与数据

曲目文件位置以媒体库根目录下的相对路径保存。同一数据格式下，停服后将 **整个 build/ 运行目录（含程序、数据库、音频）一起移动**，直接运行其中的程序即可继续使用，UUID、歌单、人工修改和账号保持不变，不需要携带源码。只复制音频到新库可重新扫描导入，但不会带回数据库中的 UUID、歌单等信息。若显式配置了外部绝对路径，该外部目录需要一起保留或重新指定；单独移动/重命名音频按下述哈希匹配规则识别，不保证重复文件的无歧义匹配。

- 曲目 `id` 是首次建库时生成的 UUID v4，与数据库记录一一对应。歌单和人工修改引用 UUID。
- `path` 是媒体目录下的唯一相对路径，可变化；`content_hash` 是文件 SHA-256，用于完整性校验、上传去重和移动识别，不作为曲目主键。
- 相同路径重扫沿用 UUID。在同一次扫描中，旧路径消失、新路径出现，且哈希仅对应一个旧候选和一个新候选时，认定移动或重命名并沿用 UUID。存在重复内容歧义时创建新记录，旧记录保留为缺失，避免错误合并。
- 文件丢失标记 `missing=true`，保留曲目、歌单顺序、成员备注及人工覆盖；原路径文件恢复后可通过扫描清除缺失状态。媒体接口对不可用文件返回错误。
- 移除曲目标记 `removed=true`，保留原文件和用户歌单关系；扫描不会自动恢复被移除的曲目。管理页概览的已移除列表可显式恢复，恢复后 UUID 不变。常规曲目列表和同步不展示已移除曲目；用户歌单管理接口保留成员 ID，便于恢复及排序。
- 直接在原路径替换文件内容仍视为该记录的文件更新。若需表示新的独立曲目，使用新的文件路径；系统不依据音频相似度猜测关联。

SQLite 为权威数据源。写入针对相关记录，通过同一事务提交业务数据、版本、增量日志与审计；不再维护常驻的全库内存快照，也不在单项修改时清空并重写所有表。封面按内容哈希独立保存，原音频标签不会因管理页编辑而被重写。

## 扫描与上传

`POST /api/v2/admin/scan` 返回 `202`，使用查询参数 `?full=true` 请求全量重算，默认快速扫描。`GET /api/v2/admin/scan/status` 查询状态，`POST /api/v2/admin/scan/cancel` 取消；同一时间只运行一个扫描。管理页提供快速/完整扫描、进度和取消入口。

快速扫描比较文件大小、修改时间和同目录歌词/封面状态，未变化的文件跳过读取和哈希；完整扫描重新读取全部候选。若外部工具修改内容却刻意保留大小和时间，需要完整扫描。`processed` 为实际检查候选数，`changed` 为成功提交的曲目变化数；单文件错误最多保留 100 条。

文件解析、哈希和探测在数据库写事务之外进行，最后一次事务应用结果，重新读取人工覆盖和当前版本，保留并发编辑。单文件失败保留原记录并报告错误；遍历目录失败、取消或事务失败不提交扫描变更。成功扫描没有变化时不增加库修订号。扫描结果提交仍为单事务，大库提交阶段可能短暂阻塞其他数据库请求；此版本使用单连接和 DELETE journal。

支持音频标签、同名 `.lrc`/`.elrc`/`.txt` 外置歌词、内嵌/同名/专辑/目录封面。歌词支持 UTF-8/BOM、带 BOM 的 UTF-16 和 GB18030。封面和歌词匹配规则见 `../Docs/外置歌词与封面处理.md`。不调用外部歌词平台。

管理页支持多文件、文件夹和拖放目录。MP3 可在浏览器本地预览，点击上传校验后才发送文件、暂存并返回 `validation=audio_stream_verified`，确认后入库。ffprobe 验证至少存在可识别音频流，**不代表完整解码或全文件无损坏**。暂存有效期 30 分钟，后台每分钟清理；确认时再次检查内容重复，重复上传复用已有可用曲目且不覆盖其元数据。上传文件发布与扫描读取协调，避免扫描半成品。

默认上传限制 512 MiB，可在设置调整至最多 4 GiB；HTTP 限制包含 multipart 开销。元数据请求限制 2 MiB，标签读取预算 32 MiB，单条歌词最多 2 MiB；封面限制 8 MiB、20 MP。浏览器标签解析使用内置 jsmediatags 3.9.7，许可和本地补丁说明见 `internal/music/vendor/`。

## 条件修改

曲目和歌单返回 `version`。对已有曲目、歌单及其成员/附件的修改、删除，以及曲目恢复，均提交当前版本：

```http
PATCH /api/v2/tracks/曲目UUID
Cookie: music_session=登录得到的会话令牌
Origin: https://music.example.com
X-CSRF-Token: 登录或auth/me返回的csrf_token
If-Match: "3"
Content-Type: application/json

{"title":"新标题"}
```

缺少有效 `If-Match` 返回 `428 VERSION_REQUIRED`；版本已变化返回 `409 VERSION_CONFLICT`。刷新后由用户重新确认修改，不自动覆盖他人的更改。创建歌单时 ID 已存在返回 `409`。

歌单成员排序同时提交 `track_ids` 和读取时的 `expected_track_ids`；歌单显示排序提交 `order` 和 `expected_order`，以检测并发变化。系统 `library` 歌单自动维护曲目，不允许手工修改成员。用户歌单成员备注独立于曲目备注。

## 增量同步

首次 `GET /api/v2/sync?limit=100` 获取分页快照，以后传入上一页返回的 `cursor`。默认每页 100 项，最多 500 项；按实体或日志条数分页，单条曲目歌词及单个歌单成员列表不会再拆分。

响应包含 `changed_tracks`、`playlists`、`deleted_tracks`、`deleted_playlists`、`cursor`、`has_more`、`full_snapshot`、`reset` 和 `revision`。客户端应：

1. `reset=true` 时清空该服务器的旧缓存；仅新快照的第一页发出 reset。
2. 按 UUID 应用更新和删除，`has_more=true` 时继续请求下一页。
3. 完成所有页后保存最终游标；后续轮询使用游标，不能用 `revision` 替代游标。

游标由 HMAC 签名，绑定账号 UUID、权限版本、会话/Key、角色、scopes、歌单授权和可见成员集合；有效期 24 小时。过期、授权或可见成员变化会重新发快照并返回 reset，客户端必须清理失去权限的数据。快照分页期间库发生变化返回 `409 SYNC_RESTART`，丢弃中间结果并不带游标重试。

变更日志持久保留最近 10,000 条事件，游标超出窗口自动退回快照。增量页读取实体当前状态，在连续变化期间以完成后续页达到最终一致；客户端须完成分页。管理页的普通曲目/歌单列表仍是完整列表，大库客户端应使用分页同步。压缩可在反向代理启用。

## 权限与并发

浏览器使用账号会话，管理接口只接受管理员账号会话。播放器认证头使用 `X-API-Key` 或 `Authorization: Bearer ...`，Key 归属账号、只保存密钥哈希，创建/轮换时仅返回一次完整密钥。Key 不再具有独立角色，只能配置 `library:read`、`media:read`，默认无歌单授权；显式空 scopes 禁用全部能力。有效访问范围为账号当前可访问的歌单与 Key 授权的交集。显式 Key 优先于 Cookie，失败时不回退，也不合并权限。

| Scope | 接口能力 |
| --- | --- |
| `library:read` | 曲目、歌单、同步 |
| `media:read` | 音频、封面 |

`max_concurrency` 为 Key 元数据请求并发数，默认 4，范围 1–64；`max_downloads` 为音频和封面下载并发数，默认 1，范围 1–16。两者独立，下载不会占满元数据限额；超限返回 `429` 和 `Retry-After: 1`。账号会话不受 Key 限额约束。并发计数仅在当前进程有效；设备绑定和心跳租约不在此版本范围内。

本期只开放管理员界面和本机首次管理员初始化。底层账号、歌单、Key 和暂存已有归属和隔离，停用账号会使会话及 Key 失效；多账户管理页面和歌单分享尚未开放，未增加公开注册或分享链接。

调用统计按会话/Key 持久记录次数、成功/失败、字节和耗时；健康检查不计入，登录请求归入未认证统计。审计记录操作人、凭证类型、账号 ID、凭证 ID、动作、实体和时间，密码和令牌不写入审计。审计和备份可能包含私人元数据，应限制文件访问；Windows 权限还需要由部署目录 ACL 控制。

## 接口清单

以下均以 `/api/v2` 开头；除健康检查和登录外需要认证。账号写请求还需 CSRF 令牌和同源 Origin。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/health` | 版本、修订号 |
| POST | `/auth/login` | JSON username/password，返回 user、csrf_token，并设置会话 Cookie |
| GET | `/auth/me` | 当前账号与 CSRF 令牌 |
| POST | `/auth/logout`、`/auth/activity` | 撤销当前会话、更新活动时间 |
| PUT | `/auth/password` | JSON old_password/new_password，成功后重新登录 |
| GET / DELETE | `/auth/sessions`、`/auth/sessions/{id}` | 列出、撤销自己的会话 |
| GET | `/sync?cursor=...&limit=100` | 分页快照与增量同步 |
| GET | `/tracks` | 完整曲目列表 |
| GET / PATCH / DELETE | `/tracks/{id}` | 查询、编辑、软移除 |
| PUT | `/tracks/{id}/cover`、`/tracks/{id}/lyrics` | multipart `cover` / `lyrics_file` |
| GET / HEAD | `/media/{id}`、`/covers/{id}` | 音频支持 Range/ETag；封面返回 ETag |
| GET / POST | `/playlists` | 歌单列表、创建 |
| GET / PATCH / PUT / DELETE | `/playlists/{id}` | 详情、编辑、替换、删除 |
| POST | `/playlists/{id}/tracks` | 批量添加 `track_ids` |
| PATCH / DELETE | `/playlists/{id}/tracks/{trackID}` | `note` 备注、移出成员 |
| PUT | `/playlists/{id}/tracks/order` | 成员排序 |
| PUT | `/playlists/order` | 歌单显示排序 |
| POST | `/uploads` | multipart `file` 直接校验并入库 |
| POST | `/uploads/stage` | 暂存，`file`，可选 `lyrics_file`、`cover`、元数据 |
| GET / DELETE | `/uploads/stage/{id}` | 查看、取消暂存 |
| GET | `/uploads/stage/{id}/cover` | 暂存封面 |
| POST | `/uploads/stage/{id}/commit` | 可选 JSON 元数据；返回 `track`、`duplicate` |
| POST | `/admin/scan`、`/admin/scan/cancel` | 开始、取消扫描 |
| GET | `/admin/scan/status` | 扫描进度、错误和状态 |
| GET | `/admin/removed` | 已移除曲目 |
| POST | `/admin/removed/{id}` | 条件恢复 |
| GET | `/admin/audit?limit=100&before=审计ID` | 审计倒序分页 |
| POST | `/admin/backups` | 创建并校验 SQLite 备份，返回本机路径 |
| GET / PATCH | `/admin/settings` | ffprobe、上传限制、扫描开关、更新仓库 |
| GET | `/admin/stats`、`/admin/update` | 调用统计、更新状态 |
| POST | `/admin/update/check` | 异步检查正式发布并准备更新包 |
| GET / POST / DELETE | `/auth/keys` | 管理员列出/创建 Key，撤销当前账号全部 Key |
| PATCH / DELETE | `/auth/keys/{id}` | 更新策略、撤销 |
| POST | `/auth/keys/{id}/rotate` | 轮换密钥 |

## 备份、恢复和更新

可选 `package-release.ps1` 复用统一构建入口，生成 `build/musicserver.exe`、`build/musicserver` 和 `build/packages/musicserver-windows-amd64.zip`、`build/packages/musicserver-linux-amd64.tar.gz`，文件名不随版本变化。Windows 包仅包含程序和 README；Linux 包另含部署模板；均不包含数据库、媒体、密码或运行配置。解压后可直接运行包内程序，默认数据目录仍跟随程序位置。

管理页概览或备份接口使用 `VACUUM INTO` 生成一致的 SQLite 快照，运行 `integrity_check` 成功后才返回路径。文件位于数据库旁 `backups/`，包括曲目元数据、歌单、Key 哈希、覆盖、封面、审计等数据库内容；**不包含媒体和暂存文件**，这些目录须另外备份。未设置备份自动清理策略，应按实际容量归档或删除旧备份。

恢复步骤：停止服务，保留当前数据库及媒体副本，将已校验的 schema 4 备份复制为启动 `-data` 指定的数据库，恢复匹配的媒体目录，再启动。启动会撤销旧会话；备份仍可能恢复旧密码和旧 Key，需核对并轮换。不要对运行中的数据库直接覆盖。schema 1–3 不能用于本版恢复，需配合原版本程序使用。

配置 `update_repository` 为 GitHub 仓库后，启动、修改配置及每 6 小时检查正式 Release；仅准备当前平台包，验证大小和 Release SHA-256。发现可用新包时先创建数据库备份，备份失败则停止准备；状态返回 `backup_path`、`package_path`。下载完成不会安装、替换程序或自动重启。准备备份到实际安装之间的数据修改需在停服前另做备份。私有仓库使用 `MUSIC_SERVER_GITHUB_TOKEN`。

## 验证

```sh
go test ./...
go vet ./...
go test -race ./...
go build -o ./build/musicserver ./cmd/musicserver
```

Race 检查需要支持的 C 工具链。隔离浏览器回归 `tests/management-ui.cjs` 自建临时媒体、数据库和子进程，不连接现有服务器：

```powershell
$env:MUSIC_SERVER_TEST_BINARY = (Resolve-Path '.\build\musicserver.exe').Path
$env:MUSIC_SERVER_PLAYWRIGHT = 'playwright'
$env:MUSIC_SERVER_TEST_PYTHON = 'python'
node tests/management-ui.cjs
```

安装 Playwright 及其 Chromium、Python 后运行；终端初始化测试在 Windows 需要 pywinpty，其他系统需要 pexpect。`MUSIC_SERVER_PLAYWRIGHT` 也可为模块绝对路径。浏览器回归覆盖真实终端隐藏密码初始化、登录、刷新保持会话、改密、注销、撤销会话和原有管理流程。

Windows 可另运行 `python tests/runtime-layout.py`（需要 pywinpty），用临时副本验证 `start.bat` 从其他工作目录启动时仍使用 `build/` 的程序、数据库和音乐，不扫描源码目录或在调用目录写入数据。

`scripts/verify-management.cjs` 和 `scripts/verify-local-preview.cjs` 另依赖真实音乐样本，只能指向隔离测试服务，使用 `MUSIC_SERVER_UI_USERNAME`、`MUSIC_SERVER_UI_PASSWORD` 提供该隔离库的账号。
