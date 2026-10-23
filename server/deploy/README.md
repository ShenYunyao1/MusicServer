# Ubuntu 部署与自动更新

适用 Ubuntu 24.04、Linux amd64、systemd。数据库使用 schema 7；本版不迁移或清空旧库。首次部署和云端连通性仍需在目标主机验收。

## 首次安装

1. Ubuntu 安装 `ffmpeg`（提供 `ffprobe`）和 `curl`，运行服务器不需要安装 Go。
2. 从本项目正式 Release 下载 `musicserver-linux-amd64.tar.gz`、`release-manifest.json` 和 `release-manifest.json.sig`。先验证签名及包 SHA-256，再解压；不要以 root 身份直接解压未经验证的网络文件。
3. 在普通用户的临时目录解压，执行 `sudo bash deploy/install.sh`。安装脚本拒绝覆盖已有 `/opt/musicserver/current`，管理员密码在终端设置。
4. 在 `/etc/musicserver/musicserver.env` 设置 `MUSIC_SERVER_PUBLIC_URL=https://你的域名`，配置 HTTPS 反向代理至 `127.0.0.1:8080`，重启 `musicserver.service`。公网只开放 SSH 与 HTTPS，8080 保持回环监听。

首次信任应从经过审查的源码或可信安装渠道取得公钥。可在有 Go 的本地开发机使用可信源码验证已下载资产，再上传到 Ubuntu；无需在云端编译：

```sh
# 在可信源码的 server/ 下运行；不启动服务、不接触数据库。
go run ./cmd/verify-release -dir /下载资产所在目录
```

固定目录：

| 位置 | 用途与权限 |
| --- | --- |
| `/opt/musicserver/current/musicserver` | 固定程序入口，root 管理，应用只读 |
| `/opt/musicserver/releases/` | 各次已验证程序，保留旧版本用于回退 |
| `/usr/local/lib/musicserver/musicserver-updater` | 独立安装器，root 管理 |
| `/var/lib/musicserver/library.sqlite` | 账户、歌单等运行数据库，musicserver 用户 |
| `/srv/musicserver/media/` | 媒体文件，musicserver 用户 |
| `/etc/musicserver/` | root 配置，不接受网页提供路径或命令 |
| `/var/lib/musicserver-update/queue/` | 版本号任务，应用可提交，包含随机任务 ID |
| `/var/lib/musicserver-update/job.json` | 最新安装结果，root 写、应用只读 |
| `/var/lib/musicserver-update/backups/` | 停机后的最终数据库备份，root 私有 |

## 日常更新

管理员打开“系统设置 → 程序更新”，检查版本、发布说明和 schema，点击“安装更新并重启”并确认。每六小时的定时检查只读取元数据，不下载程序、不安装。

独立更新服务从固定官方仓库重新取指定版本，使用内置 Ed25519 公钥验证清单，再验证包大小和 SHA-256。通过校验后：解包到独立目录 → 阻止普通 API 请求 → 停止主服务 → 校验最终数据库备份 → 原子切换 current → 启动主服务 → 核对健康接口的版本、提交号及 schema → 开放请求。后台页面重新连接后可能需要重新登录。

下载、签名、空间或兼容性错误发生在停机前，原程序继续运行。启动或健康检查失败则停止新程序，恢复旧程序和停机快照，再启动旧程序。回退期间保持维护状态；恢复失败时保留维护状态并在 `job.json` 和 journal 中报告，管理员应先排查，不能直接删除数据。

系统启动和每分钟的 recovery timer 会检查被中断的任务，完成或回退；worker 有排他锁，不会并行安装。**跨 schema 更新一律拒绝自动安装**，需要另外设计迁移或手动部署。私有发行源和任意自定义仓库不参与自动安装；自动安装只信任本项目固定仓库和公钥。

## 运维和边界

```sh
systemctl status musicserver musicserver-update.path musicserver-update-recovery.timer
journalctl -u musicserver -u musicserver-update --since today
cat /var/lib/musicserver-update/job.json
```

更新器仅替换主程序和对应 `build.json`；不自动改 root 配置、systemd 模板或独立更新器本身。后续若更新协议、信任公钥或服务模板变化，发行说明会要求管理员更新这些组件。发行私钥保存在 GitHub Actions 的 `UPDATE_SIGNING_KEY` Secret 中，不进入仓库、发行包或云服务器；本地打包默认不签名，不能被自动安装器接受。

数据库备份不包含媒体、暂存文件和配置，应独立备份。下载、已解包程序及备份暂不自动删除；按容量手工归档，至少保留 current、上一个成功版本和最近备份。安装器在下载前检查可用空间，但其他进程同时占用磁盘仍可能导致失败。

Windows 用于本地开发，支持检查发行版本；自动停机安装限 Linux systemd。Windows 手工更新须停止服务、备份 `build/data` 和媒体，再替换固定 `build/musicserver.exe`，不能覆盖运行数据。

## 发布

本地完成测试、提交后推送 main，Actions 在 Windows 和 Ubuntu 上运行 Go 测试；Ubuntu 额外验证 race、实际隔离 systemd 服务的安装、数据库回退和中断恢复。main 或手动触发只生成可下载的签名构建产物；推送与代码版本一致的 `vX.Y.Z` 标签才发布正式 Release。先创建草稿，资产全部上传后再公开，避免检查到半成品。

固定资产名：`musicserver-windows-amd64.zip`、`musicserver-linux-amd64.tar.gz`、`release-manifest.json`、`release-manifest.json.sig`。清单包含版本、提交、schema、平台、大小和 SHA-256。SHA-256 校验完整性，Ed25519 签名验证发行密钥持有者；仓库或签名 Secret 被控制仍需吊销并轮换信任，签名不替代功能测试。
