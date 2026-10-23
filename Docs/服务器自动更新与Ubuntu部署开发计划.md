# 服务器自动更新与 Ubuntu 部署开发计划

记录日期：2026-10-23。目标版本：0.14.0；数据库继续使用 schema 7，不迁移、不清空现有数据。

## 目标与边界

管理员手动检查 GitHub Release，有新版时显式选择更新；更新器下载并校验安装包，完成停机备份、替换、重启和健康检查，失败时回退。定时检查只提示，不自动安装。

本阶段完成本地实现、自动化验证、GitHub Actions 构建及 Release 发布。用户已授权准备 GitHub 登录、推送本次相关提交和发布构建；不提交凭据、运行数据或编译产物。Ubuntu 云端部署等待用户提供服务器后执行，未经实际验证不得勾选。

## 设计约束

- GitHub 仓库：`ShenYunyao1/MusicServer`，公开发行包可匿名获取。Git 使用 HTTPS 和系统凭据管理器；不在源码或日志中保存令牌。
- 检查和安装分离。检查不下载、不备份；管理员确认后才创建安装任务。任务状态持久化，服务重启后仍可查询。
- Ubuntu 由 systemd 管理主服务和独立更新器；主进程不提权、不自行覆盖正在运行的程序。更新器只接受固定目录中的任务，限制仓库、版本、平台、文件名和包内容。
- 固定入口 `/opt/musicserver/current/musicserver`，内部版本目录保留上一个版本；数据、备份、媒体和配置与程序分开。安装包内可执行文件始终叫 `musicserver`。
- 发布元数据包含版本、提交号、schema、平台、大小及 SHA-256；明确区分完整性校验和来源认证。选择可在 GitHub Actions 与 Ubuntu 上实际运行的来源校验方案，并记录信任边界。
- 安装前拒绝不兼容 schema，不自动重建数据库。停止主服务后生成最终一致备份，再切换版本；健康检查必须匹配目标版本和提交号。失败回退后保留诊断信息。
- Windows 本地开发继续使用固定 `server/build/musicserver.exe`；Ubuntu 的 systemd 安装流程通过 Linux 测试验证，不把 Windows 交叉编译当成云端部署验收。

## 实施与验收进度

- [x] 记录计划及本阶段授权范围。
- [ ] 核对仓库公开状态、HTTPS 读写权限与 GitHub CLI 登录；检查本地 Linux 测试环境。
- [ ] 发布流水线：新增 `.github/workflows/server-release.yml`，测试 Go、构建 Windows/Linux 固定名称包、生成元数据、发布 Release；支持标签发布和手动触发。
- [ ] 更新协议：重构 `server/internal/update/update.go`，将检查、下载准备和安装任务分开；覆盖无更新、缺包、坏校验、网络失败、重复任务和关闭取消。
- [ ] 安装引擎：新增独立命令与 `server/internal/update/` 安装模块，安全解包、持久状态、停机备份、原子切换、重启健康检查和失败回退；用故障注入测试验证。
- [ ] Ubuntu 接入：扩展 `server/deploy/` 中的 systemd 单元、配置示例与安装脚本；验证权限边界、并发锁和重启后的任务恢复。
- [ ] 管理后台：调整 `server/internal/music/` 更新 API 和页面，显示版本、发布说明、兼容性、安装进度及失败原因；只有管理员可安装，安装前明确确认。
- [ ] 构建文档：更新 `server/package-release.ps1`、README、更新日志和部署说明，固定产物路径与名称；说明 Windows 和 Ubuntu 支持范围。
- [ ] 本地回归：相关单测、完整 Go 测试、vet、Windows/Linux 构建、前端交互及安装失败回退验证；逐项记录真实结果。
- [ ] 本地提交并推送本次已验收改动，触发 Actions，核对构建日志、发行资产、版本及校验信息。
- [ ] Ubuntu 云端部署（等待服务器）：检查 SSH、架构、GitHub 连通性；安装 ffprobe、systemd 与 HTTPS 入口，完成升级和回退实测。

## 文件职责与验证方式

| 文件或目录 | 职责 | 主要验证 |
| --- | --- | --- |
| `.github/workflows/server-release.yml` | 测试、构建、发布与来源证明 | 实际 Actions 运行及 Release 资产 |
| `server/internal/update/` | Release 协议、任务、安装、回退 | `go test ./internal/update -count=1`，故障注入 |
| `server/cmd/` | 主服务接入及独立更新命令 | Windows/Linux 编译，隔离运行 |
| `server/internal/music/` | 管理 API、授权、更新 UI | API 回归与浏览器验证 |
| `server/deploy/` | Ubuntu 安装和 systemd 管理 | Linux CI 集成测试、后续云端实测 |
| `server/package-release.ps1` | 与 CI 对齐的固定产物 | 检查归档清单与元数据 |
| `server/README.md`、`update.md`、本计划 | 使用、维护、验收记录 | 文档与最终实现核对 |

每完成并验证一个功能，更新对应勾选和验收记录，创建相关 Git 本地提交。若平台能力、权限或工具不足，记录具体限制，继续不依赖该条件的本地工作。

## 风险和待验证条件

1. 云端尚未提供，无法提前保证其到 GitHub 的网络连通性或实际 systemd 行为。
2. 停机升级会短暂中断播放；浏览器需重连，服务器启动会撤销旧会话，可能需要重新登录。
3. 初期仅自动安装兼容 schema 7 的版本；跨 schema 更新显示手工处理要求。
4. 安装失败必须恢复旧程序；备份保留策略和磁盘空间检查不得删除原始媒体或账户数据。
5. GitHub 登录若需浏览器授权，由用户完成交互，不索取聊天中的访问令牌。

## 验收记录

- 2026-10-23：已完成现状核对：现有更新器只能下载校验包，尚无安装、重启与回退。现有服务版本 0.13.0，schema 7。本计划开始执行。
