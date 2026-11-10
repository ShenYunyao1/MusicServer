# Pikmelo Server v0.21.19
<!-- musicserver-release: {"version":"0.21.19","schema":9,"status":"stable"} -->

Pikmelo Server · 拾律服务器 Docker 分支，数据库 schema 9。提供容器运行、持久化数据卷和通过 Docker 命令完成的受控升级。

## 功能

- 多阶段构建 Linux amd64 镜像，包含 ffprobe、CA 证书及内置健康检查。镜像身份记录源码提交和版本。
- Compose 默认监听宿主机 `127.0.0.1:8080`，应用以非 root 用户运行，根文件系统只读。业务数据挂载 `/data`，上传临时文件使用独立磁盘卷。
- 管理员通过网页完成首次初始化。网页更新页显示 Docker 托管状态和命令提示，镜像升级由操作人通过 Docker CLI 确认执行。
- 升级前停机备份业务卷，再拉取目标镜像、重建并检查健康。schema 8 数据由应用执行备份及事务升级，保留账户、Key、UUID、歌单、标签和媒体。
- 健康失败时使用原镜像和停机快照恢复到新卷，完成账户、歌单及媒体验收后开放写入。

## 使用与升级

正式镜像为 `ghcr.io/shenyunyao1/pikmelo-server`，可选择具体版本标签或镜像摘要。Release 的 `container-release.json` 记录版本、schema、源码提交和镜像摘要，镜像附带来源证明。

完整启动、备份、升级、回退和已有安装导入步骤见仓库的 [Docker 使用说明](https://github.com/ShenYunyao1/Pikmelo-Server/blob/v0.21.19/server/deploy/docker.md)。首次 GHCR 镜像发行还需核验公开可见性与匿名拉取结果。
