# Ubuntu 部署与自动更新

适用 Ubuntu 22.04/24.04、Linux amd64、systemd。数据库使用 schema 7；本版不迁移或清空旧库。目标主机网络和服务须按实际环境验收。

## 首次安装

1. Ubuntu 安装 `ffmpeg`（提供 `ffprobe`）和 `curl`，运行服务器不需要安装 Go。
2. 从本项目正式 Release 下载 `musicserver-linux-amd64.tar.gz`、`release-manifest.json` 和 `release-manifest.json.sig`。先验证签名及包 SHA-256，再解压；不要以 root 身份直接解压未经验证的网络文件。
3. 在普通用户的临时目录解压，执行 `sudo bash deploy/install.sh`。安装脚本拒绝覆盖已有 `/opt/musicserver/current`，启动服务后在网页设置管理员密码。
4. 在 `/etc/musicserver/musicserver.env` 设置 `MUSIC_SERVER_PUBLIC_URL=https://你的域名`，配置 HTTPS 反向代理至 `127.0.0.1:8080`，重启 `musicserver.service`。公网只开放 SSH 与 HTTPS，8080 保持回环监听。

自动化准备可使用 `sudo bash deploy/install.sh --prepare-only`：只准备目录、账户和单元，不创建管理员、不启动服务；随后执行脚本打印的启动命令。没有域名时先使用 SSH 本地端口转发，例如 `ssh -N -L localhost:18080:127.0.0.1:8080 <SSH主机>`，浏览器访问 `http://localhost:18080`，无需开放公网 8080。

无管理员时，网页显示首次设置表单。初始化授权码保存在 `/var/lib/musicserver/setup-token`，仅服务账户和 root 可读；通过 SSH 获取并在表单填写，或使用 `http://localhost:18080/#setup=<授权码>` 打开。该链接不能转发或写入日志，网页读取后立即清除地址栏片段。密码只在网页输入并确认，成功后自动关闭初始化、删除授权文件，再以新密码登录。重启不会改变尚未使用的授权码；已有管理员时不会重新开放初始化，离线恢复仍使用 `admin reset-password` 命令。

主机访问 GitHub 必须走代理时，在上述环境文件配置 `HTTPS_PROXY`、`HTTP_PROXY`，并设置 `NO_PROXY=127.0.0.1,localhost,::1`。主服务和独立更新器共同读取该文件；修改后重启主服务，更新器下次运行读取新配置。不要为了配置代理而修改其他应用的全局网络设置。已有 0.14.0 部署需同时更新独立更新器服务模板并执行 `systemctl daemon-reload`；只更新主程序不会替换 root 单元。

首次信任应从经过审查的源码或可信安装渠道取得公钥。可在有 Go 的本地开发机使用可信源码验证已下载资产，再上传到 Ubuntu；无需在云端编译：

```sh
# 在可信源码的 server/ 下运行；不启动服务、不接触数据库。
go run ./cmd/verify-release -dir /下载资产所在目录
```

## 公网 IP 直接访问 HTTPS

没有域名也可以使用受信任的公网 IP 证书。官方方式见 [Let's Encrypt IP 证书说明](https://letsencrypt.org/2026/03/11/shorter-certs-certbot.html)：Certbot 5.4+ 支持 IP webroot 验证，使用 shortlived profile，证书约六天有效，必须启用自动续期。公网 TCP 80 用于验证及 HTTP 跳转，443 提供 HTTPS；后端继续监听 127.0.0.1:8080。

1. 安装 Nginx、python3-venv；按 [Certbot 官方 pip 安装方式](https://certbot.eff.org/instructions?ws=nginx&os=pip)，在 `/opt/musicserver-certbot` 的独立 venv 安装兼容版本，不改系统 Python 包。检查 `certbot --version` 至少 5.4。
2. 准备 `/var/www/musicserver-acme`，先仅配置 HTTP 的 `/.well-known/acme-challenge/` 静态目录，其他请求返回 404。确认公网可读取测试文件，再申请证书；不要在证书就绪前开放明文登录。
3. 先在独立 staging 配置目录验证，再申请正式证书（`PUBLIC_IP` 替换为实际公网 IP；默认证书名固定为 musicserver）：

```sh
/opt/musicserver-certbot/bin/certbot certonly --non-interactive --agree-tos \
  --register-unsafely-without-email --preferred-profile shortlived \
  --webroot --webroot-path /var/www/musicserver-acme \
  --ip-address PUBLIC_IP --cert-name musicserver
```

该示例不登记个人邮件；如需要邮件登记，改用 `--email`。测试签发增加 `--staging`，并使用独立 config/work/log 目录和证书名，不能将测试证书用于正式 HTTPS。网络需要代理时通过进程环境配置。

4. 将 `nginx.conf.example` 中的 `music.example.com` 替换为实际公网 IP，安装为 Nginx 站点并运行 `nginx -t`。示例支持默认 512 MiB 上传及附件，关闭代理请求／响应缓冲，保留流式音频；若提高应用上传限制，应同步提高代理限制。
5. 在 `musicserver.env` 设置 `MUSIC_SERVER_PUBLIC_URL=https://PUBLIC_IP`，重启主服务并平滑重载 Nginx。网页会话因重启失效，使用原密码重新登录；账户、Key 和媒体不重建。此后浏览器访问 `https://PUBLIC_IP/`，HTTP 自动跳转。
6. 安装 `renew-nginx.sh` 为 `/etc/letsencrypt/renewal-hooks/deploy/musicserver-nginx`（root 所有，0755），安装 `musicserver-certbot.service` 和 `.timer` 到 systemd，执行 `daemon-reload` 并启用 timer。

```sh
systemctl enable --now musicserver-certbot.timer
/opt/musicserver-certbot/bin/certbot renew --cert-name musicserver \
  --dry-run --run-deploy-hooks --no-random-sleep-on-renew
systemctl start musicserver-certbot.service
systemctl show musicserver-certbot.service -p Result -p ExecMainStatus
systemctl list-timers musicserver-certbot.timer
```

定时器每六小时检查并增加随机偏移；服务关闭 Certbot 自身的第二层随机等待，避免超过运行时限。续期成功后先校验 Nginx 配置再平滑重载，不中断后端服务。代理环境仍来自 `musicserver.env`。证书、私钥和 ACME 账户不进入 Git；主程序自动更新不会改这些 root 配置。公网 IP 变更时需重新签发证书并同步 Nginx 和 public origin。

从外部浏览器和命令行验证受信任 HTTPS、HTTP 跳转、登录及资源后，再关闭原 SSH 转发；确认本地转发端口已释放且公网地址仍能访问。单纯停止 SSH 隧道不会自动开放公网服务，入口监听和安全组须实际可达。

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

0.14.0/0.15.0 的旧更新器受 UMask=0027 影响，可能使新程序仅 root 可执行，健康检查失败后自动回退。升级到 0.15.1 前，先从已验证的正式包通过 SSH 替换 `/usr/local/lib/musicserver/musicserver-updater`（root 所有、0755），再从网页发起升级。不能仅升级主程序而继续使用旧 root 更新器。

更新器的停机数据库快照不包含音频、暂存及 Nginx／systemd／TLS 系统文件。0.18.0 起网页“备份与恢复”另提供包含已入库音频及应用设置的完整归档，并可用 `musicserver backup restore --file ... --destination 新的空目录` 校验恢复；恢复后需核对目录和服务账户权限再启动。自动完整备份默认关闭，启用后仅按策略清理自动归档；手动归档须显式删除。网页容量页可清理应用持有的旧更新下载缓存，保留当前／待更新／回退版本；root 更新器自己的下载、已安装程序和停机快照仍需按容量归档，至少保留 current、上一个成功版本及回退数据库。安装器及应用检查可用空间，外部进程同时占盘仍可能导致失败。

Windows 用于本地开发，支持检查发行版本；自动停机安装限 Linux systemd。Windows 手工更新须停止服务、备份 `build/data` 和媒体，再替换固定 `build/musicserver.exe`，不能覆盖运行数据。

## 发布

本地完成测试、提交后推送 main，Actions 在 Windows 和 Ubuntu 上运行 Go 测试；Ubuntu 额外验证 race、实际隔离 systemd 服务的安装、数据库回退和中断恢复。main 或手动触发只生成可下载的签名构建产物；推送与代码版本一致的 `vX.Y.Z` 标签才发布正式 Release。先创建草稿，资产全部上传后再公开，避免检查到半成品。

固定资产名：`musicserver-windows-amd64.zip`、`musicserver-linux-amd64.tar.gz`、`release-manifest.json`、`release-manifest.json.sig`。清单包含版本、提交、schema、平台、大小和 SHA-256。SHA-256 校验完整性，Ed25519 签名验证发行密钥持有者；仓库或签名 Secret 被控制仍需吊销并轮换信任，签名不替代功能测试。
