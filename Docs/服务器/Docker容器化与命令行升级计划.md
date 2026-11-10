# Docker 容器化与命令行升级计划

2026-11-10，用户批准创建独立分支，范围为 Pikmelo Server 的容器化及通过 Docker 命令完成受控升级。分支为 `codex/docker-compose`。

## 交付范围

- 单实例 Go 服务与 SQLite，镜像包含 ffprobe、CA 证书和命令行健康检查。
- Compose 默认仅发布本机 8080 端口，数据库、媒体、附件、上传任务、邮件凭据、日志和备份集中在 `/data` 持久卷。
- 网页标明 Docker 托管状态，升级通过 Docker CLI 执行；容器采用非 root 用户，根文件系统只读。
- 用户显式选择升级时，先保留原镜像身份，停止服务并备份持久卷，再拉取、重建及检查健康。回退按 schema 与写入情况选择恢复停机快照。
- 本分支的 CI 覆盖服务端、网页与真实容器，正式标签通过门禁后发布 GHCR 镜像。客户端仓库维护在用户另行授权后开展。

## 执行进度

- [x] D01 添加 Dockerfile、构建上下文过滤、Compose 和配置模板，建立非 root、持久卷及健康检查约定。
- [x] D02 添加健康检查命令和容器更新模式，验证镜像源码身份、网页提示及更新接口边界。
- [x] D03 编写启动、初始化、备份、升级、回退和迁移指南，增加容器测试与服务端镜像 CI。
- [x] D04 本机验证实际构建、上传、重建保留数据、schema 8→9 及停机快照回退，完成本地提交。
- [x] D05 用户显式要求推送及发行后，核查 Actions、GHCR 镜像身份及正式版本发布结果。

## 文件与验证

运行配置位于 `server/Dockerfile`、`server/.dockerignore`、`server/compose.yaml` 和 `server/docker.env.example`，使用说明位于 `server/deploy/docker.md`。主程序增加健康检查子命令，更新器增加外部托管状态，网页更新卡片展示对应操作提示。

针对性验证覆盖版本元数据、健康检查 HTTP 边界、容器模式禁止网页安装和下载、Compose 解析、持久化权限、ffprobe、网页初始化与实际上传、停止后快照、版本重建以及 schema 迁移回退。schema 8 仅作为既有数据升级基线，测试预览程序使用隔离构建，不参与发行。

## 当前环境

已找到 Docker Desktop 的 CLI 和 Compose，安装路径为 `C:/Program Files/Docker/Docker/`。初次检查时 Linux 引擎尚未运行，已请求启动现有 Docker Desktop。真实容器验收以引擎可用后的实际结果记录。

## 分阶段验收

D01–D03 已完成。Docker Desktop 4.38.0、Engine 27.5.1、Compose 2.32.4 的 Linux amd64 引擎实际构建镜像成功，Go 命令、更新器、版本专项及关联 vet 通过，工作流 actionlint 通过。镜像构建上下文只包含 Go 模块、命令及内部源码。

本机构建遇到已有加速器 403，采用本次进程 PATH／代理及官方 Registry 构建参数完成验证。实际上传先复现 507，证实 256 MiB tmpfs 小于 512 MiB 预留空间，改用磁盘临时卷后通过，保留原空间预留策略。

`tests/docker-smoke.py` 已使用真实容器验证非 root／只读根、ffprobe、网页初始化、带歌词上传、更新接口拒绝原生安装、重建保留数据、停机快照及回退。隔离 schema 8 预览程序升级至 schema 9，随后从原快照与原镜像恢复 schema 8，账户、Key、歌单和媒体读取均通过。真实 Chromium 网页验证 Docker 命令提示、更新控件状态及刷新后恢复。下一步使用已提交源码身份重新构建并验收最终镜像。

D04 已完成。本机最终镜像为 `pikmelo-server:docker-local`，镜像 ID 为 `sha256:793526180c613a1c20cfcc09fd06cf02a3da33f148c53cc4d3f0439bd4c29de7`，构建源码为 `e8feceb267e5dd2e8629e53fa587ec5906dfbbc1`。实际健康接口的 commit 与该值一致，modified 为 false。最终镜像再次通过真实浏览器、完整容器回归、schema 8→9 和恢复 8 的验收，并从应用完整备份导入新卷，使用自定义数据库／媒体路径验证账户、歌单及原 Key 的媒体授权。所有本次随机测试容器、卷及基线测试镜像均已清理。

命令、发行工具、更新器及版本专项通过，默认更新 API 与撤销会话专项通过，相关 vet、Compose 解析和 actionlint 通过。未运行本地完整跨平台回归，D05 以用户授权推送后的 Actions 为准。Docker 镜像稳定通道独立推进，Docker 专用 Release 使用 `--latest=false`，保护原生更新器的签名平台资产检查入口。

## 正式镜像发行验收

2026-11-10，D05 已完成。用户明确要求发布镜像，已发布 [v0.21.19](https://github.com/ShenYunyao1/Pikmelo-Server/releases/tag/v0.21.19)，对应源码 `12ceba46e76f404bbcd12631a89a9d7e84d72fce`、schema 9 和 Linux amd64。Docker 分支与版本标签均已推送，标签 [Actions 42817706912](https://github.com/ShenYunyao1/Pikmelo-Server/actions/runs/42817706912) 的十项作业全部成功，包含 Windows／Ubuntu Go 与 vet、Linux race／systemd、六组网页及真实容器升级回退。

正式镜像为 `ghcr.io/shenyunyao1/pikmelo-server:0.21.19`，与 Docker 稳定通道 `latest` 指向同一摘要。

```text
sha256:4a3807a4c64162c0774619c7ce3feb2a360f57129a2a0911d15953f409b83eb8
```

Release 的 `container-release.json` 为 283 字节，独立下载核验 SHA-256 为 `966363a17e8486c9abd1b2cfc64a069f89d9940b6b0cb5ff499a3e45b2fe9df8`，其版本、schema、提交及镜像摘要与 Registry 一致。使用空 Docker 凭据配置完成匿名拉取，镜像平台、非 root 用户和源码标签均匹配。

镜像来源证明通过 `gh attestation verify` 校验，固定仓库、`refs/tags/v0.21.19`、上述源码提交及 `server-release.yml`，并要求 GitHub 托管 runner。本机旧版 CLI 遇到缓存跨盘重命名错误后，改用已校验官方摘要的隔离 CLI 与缓存完成验证。

再次直接运行上述正式摘要镜像，真实 Chromium、带歌词上传、非 root／只读根、原生更新 API 边界、重建持久化、schema 8→9、故意失败的健康门禁及原镜像／停机快照恢复 8、完整应用备份导入、自定义数据路径及原 Key 媒体读取均通过。随机测试容器与卷已清理，验证产物保存在已忽略的 `server/build/release-verify-0.21.19/`。

Docker Release 已公开，GitHub 原生 latest 仍为 v0.21.18。使用与升级步骤见 [Docker 使用说明](../../server/deploy/docker.md)。
