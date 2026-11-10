# Windows 播放器文档

[返回文档首页](../README.md)

## 当前客户端入口

Windows 客户端在工作区的 `MusicPlayer2/` 独立仓库中维护。以下链接需要该仓库位于同一工作区。

- [客户端 README](../../MusicPlayer2/README.md)
- [客户端文件与构建目录规范](../../MusicPlayer2/Documents/客户端文件与构建目录规范.md)
- [客户端打包与分发](../../MusicPlayer2/Documents/客户端打包与分发.md)
- [Actions 构建测试与独立发布计划](../../MusicPlayer2/Documents/Actions构建测试与独立发布计划.md)
- [参数化推送发布脚本](../../MusicPlayer2/Documents/参数化推送发布脚本.md)
- [自动更新使用与恢复](../../MusicPlayer2/Documents/客户端自动更新使用与恢复.md)
- [播放器自动更新开发方案与进度](../../MusicPlayer2/Documents/播放器自动更新开发方案与进度.md)
- [新版服务器连接说明](../../MusicPlayer2/Documents/新版服务器连接说明.md)
- [服务器 v2 适配方案与进度](../../MusicPlayer2/Documents/服务器v2适配方案与进度.md)
- [公开歌单订阅适配进度](../../MusicPlayer2/Documents/公开歌单订阅适配进度.md)
- [指定账户只读分享适配进度](../../MusicPlayer2/Documents/指定账户只读分享适配进度.md)
- [下载进度与在线歌单增量刷新计划](../../MusicPlayer2/Documents/下载进度与在线歌单增量刷新计划.md)
- [对象级增量同步开发记录](../../MusicPlayer2/Documents/对象级增量同步开发记录.md)
- [常听统计上报开发记录](../../MusicPlayer2/Documents/常听统计上报开发记录.md)
- [邮箱验证服务端联调记录](../../MusicPlayer2/Documents/邮箱验证服务端联调记录.md)

## 工作区早期计划与验证

- [开发环境检查，2026-10-07](开发环境检查-2026-10-07.md)
- [初版实施计划](初版实施计划.md)
- [初版使用与验证](初版使用与验证.md)
- [服务器歌单与按需缓存实施计划](播放器服务器歌单与按需缓存实施计划.md)
- [播放器 0.7.0 使用与验证](播放器0.7.0使用与验证.md)

初版记录覆盖当时的双端联调过程，构建目录与操作步骤以当前客户端文档为准。

## 历史辅助脚本

- [build-player.ps1](build-player.ps1) 保存早期 MSBuild 环境配置，项目路径相对本脚本定位到工作区的 `MusicPlayer2/`。
- [desktop-test.ps1](desktop-test.ps1) 提供早期 Windows 桌面交互检查。截图输出位于本目录，受 Git 忽略规则保护。

## 关联约定

- [开发提交与版本发布流程](../开发提交与版本发布流程.md)
- [增量同步协议](../服务器/协议/增量同步协议-v1.md)
- [可执行协议契约](../../contracts/README.md)
- [服务器文档](../服务器/README.md)
