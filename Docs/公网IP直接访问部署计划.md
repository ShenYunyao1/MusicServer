# 公网 IP 直接访问

用户确认关闭 SSH 本地转发，改为直接访问云服务器公网 IP。本次沿用 0.15.1 程序及正式数据库，不重建管理员，不迁移数据，不修改其他应用。

使用 Nginx 作为 80/443 公网入口，HTTP 仅用于 ACME 验证及跳转；HTTPS 反向代理到 127.0.0.1:8080。使用 Let's Encrypt shortlived IP 证书及 Certbot webroot，配置自动续期和成功后平滑重载。实际地址、证书、私钥、ACME 账户及主机配置仅在忽略目录／云端保存。

- [x] 核对端口、已有服务、ACME 连通性和官方 IP 证书支持。
- [x] 安装 Nginx 与兼容 IP 证书的 Certbot，先通过公网 HTTP challenge 验证入口。
- [x] 先测试 CA 验证，再签发受信任正式 IP 证书；配置 HTTPS、HTTP 跳转、上传与流式媒体代理。
- [x] 设置应用 public origin，验证数据库、初始化关闭、HTTPS Cookie、登录来源及网页资源。
- [x] 配置自动续期与重载，实际 dry-run 验证；核对服务自启。
- [x] 从外部验证证书和访问，关闭本次 SSH 隧道，确认公网访问仍正常。
- [x] 更新通用部署模板及文档，记录验收并本地提交；不提交主机信息或密钥。

外部依赖：云安全组需允许 TCP 80/443。若主机外部端口被阻断且无控制台权限，报告实测结果并向用户提出放行要求。证书短期有效，续期定时器必须实际验证。切换 public origin 会重启主服务、撤销旧浏览器会话；用户在新 HTTPS 地址重新登录。

参考：[Let's Encrypt IP 证书与 Certbot](https://letsencrypt.org/2026/03/11/shorter-certs-certbot.html)、[Certbot pip 安装](https://certbot.eff.org/instructions?ws=nginx&os=pip)、[Nginx 代理模块](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)。

## 验收记录（2026-10-23）

- Ubuntu 软件源安装 Nginx，独立 venv 安装 Certbot 5.8.0；未修改其他应用的配置或重启其他服务。
- 外部直连 HTTP challenge 成功，staging 和正式 IP shortlived 证书均签发成功。Windows curl 和 Chromium 均在不忽略 TLS 错误的情况下通过验证；证书包含实际 IP SAN。
- HTTP 308 跳转到 HTTPS；浏览器安全上下文、管理脚本、移动端布局及来源校验通过。旧 localhost 来源登录为 403，新 HTTPS 同源登录进入凭证校验，初始化保持关闭。HTTPS public origin 启用 Secure Cookie。
- Certbot 实际模拟续期成功，deploy hook 完成 nginx -t 和重载。timer 已启用自启，每六小时检查；实际 service 执行 Result=success、ExecMainStatus=0。为避免额外随机等待超过单元时限，服务使用 --no-random-sleep-on-renew。
- 已通过工具持有的原 SSH 会话正常退出转发，本地 18080 不再监听；公网健康和完整浏览器检查仍通过。最初的进程枚举关闭组合命令被自动审批拒绝，未执行；后续采用已有会话正常结束，无需关闭其他 SSH 连接。
- 用户已确认使用原密码在公网 HTTPS 地址成功登录。主程序仍为 0.15.1、schema 7、发行提交 ff0c6aad5bb08c9189e2ae10a29cba9f028cef88，modified=false，8080 仅回环监听。
- 切换前后账户、Key、曲目、歌单、订阅及邀请原标识保持，数据库 quick_check 正常，用户新登录会话已确认。Nginx、证书 timer 均启用自启，主服务和更新器继续正常。
- 新增通用 Nginx、续期 service/timer 和 reload hook 模板，纳入发行包显式文件清单；bash 语法、release 命令编译和 vet 通过。实际主机地址与全部证书／私钥／运行配置不进入 Git。
