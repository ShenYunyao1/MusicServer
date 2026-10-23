MusicServer 0.14.1

- 独立更新服务读取 musicserver.env，支持必须经过代理访问 GitHub 的部署环境。
- 安装脚本增加 --prepare-only，准备完成后由管理员在终端设置密码。
- CI 增加 Ubuntu 22.04，与 Ubuntu 24.04、Windows 一同测试；构建使用 Go 1.26 最新补丁。
- 云端隔离 systemd 回归支持预编译测试程序，目标主机无需安装 Go。
- 数据库保持 schema 7，兼容 0.14.0 的账户、API Key、媒体和歌单。

Windows 支持检查与手工替换程序；网页自动安装需要先在 Ubuntu 配置独立更新服务。
0.14.0 部署如需代理，须同步更新 musicserver-update.service 并执行 systemctl daemon-reload；主程序自动更新不会修改 root 服务模板。
