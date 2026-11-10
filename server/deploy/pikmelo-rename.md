# Pikmelo Server 名称升级指南

自 0.21.19 起，英文名为 Pikmelo Server，中文名为拾律服务器。Go 模块及命令使用 `pikmelo-server`。

## Windows

1. 备份现有运行目录，正常停止旧程序。
2. 使用可信源码的 `go run ./cmd/verify-release -dir <下载目录>` 验证对应清单签名、平台包大小及 SHA-256。
3. 将 `pikmelo-server-windows-amd64.zip` 内的 `pikmelo-server.exe` 和 `start.bat` 放在同一运行目录。源码构建使用 `server/build/`。
4. 保留运行目录中的数据、媒体、暂存、邮件凭据和日志。启动脚本依据自身路径定位程序，双击后通过网页访问原数据库。

源码中的 `server/start.bat` 从 `server/build/` 启动。发行包中的脚本从解压目录启动，退出后暂停。旧版发行包使用 `musicserver.exe`，对应兼容脚本仍可启动。

## Linux 与已有安装

已有安装通过网页检查并确认更新。兼容包使用已发行更新器认识的包名和成员名，保留原更新事务、最终数据库备份、健康检查和失败回退。

安装目录 `/opt/musicserver`、数据 `/var/lib/musicserver`、媒体 `/srv/musicserver/media`、配置 `/etc/musicserver`、`musicserver.service` 及 `/usr/local/lib/musicserver/musicserver-updater` 是现有受管安装契约。产品改名通过已验证程序入口衔接这些配置。

新名称包包含 `pikmelo-server` 和 `pikmelo-server-updater`。初始化脚本接受新名称或兼容包，创建对应主程序入口并将独立 worker 安装到受信任位置。新更新器在验证包身份后，通过本地硬链接提供新旧程序入口。已发行的旧更新器继续通过兼容包完成更新。

手动维护独立 worker 时，从已验签的包执行对应的更新器程序，并显式传入现有配置路径。例如使用 `pikmelo-server-updater -config /etc/musicserver/updater.json -refresh-worker <版本号>`。维护前后的版本、提交和能力核验按 [独立更新器维护指南](updater-maintenance.md) 执行。

## 发行资产

| 发行用途 | Windows | Linux | 清单 |
| --- | --- | --- | --- |
| 新名称下载 | pikmelo-server-windows-amd64.zip | pikmelo-server-linux-amd64.tar.gz | pikmelo-server-release-manifest.json |
| 既有更新器兼容 | musicserver-windows-amd64.zip | musicserver-linux-amd64.tar.gz | release-manifest.json |

两份清单分别附带 `.sig`，使用同一个固定发行公钥验证。两套资产必须具有相同版本、提交、schema 和迁移声明，包内编译身份也必须一致。

## 数据与仓库

数据库 schema 9 延续现有事务升级策略。schema 8 的桥接前置条件见 [schema 9 升级指南](schema9-upgrade.md)。持久化 Cookie、CSRF、邮件密钥派生、浏览器上传队列、音量和歌词偏好、备份及恢复标记按既有格式读取。

官方仓库为 `ShenYunyao1/Pikmelo-Server`，原仓库名称作为官方兼容别名继续识别。仓库改名后的 Git 与网页跳转规则见 [GitHub 仓库改名说明](https://docs.github.com/en/repositories/creating-and-managing-repositories/renaming-a-repository)。
