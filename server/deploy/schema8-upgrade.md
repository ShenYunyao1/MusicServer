# schema 8 标签版手动升级步骤

适用正式 0.21.11／schema 8。以下操作由服务器维护者在自己的环境执行；发布程序不会自动登录服务器、替换 root 工具或安装更新。

## 先确认现有版本并保存备份

先在网页创建并下载完整备份，保存服务器配置与邮件凭据的原有受限文件。不要删除数据目录、重新初始化管理员、修改 SQLite 版本标记或重新导入音频代替数据库迁移。

Linux 已安装旧独立更新器、现有数据库为 schema 7 时，顺序必须是 **0.20.3 桥接主程序 → 0.20.3 独立更新器维护 → 0.21.11 标签主程序**。当前已是 schema 8 的服务不需要重复迁移。

0.21.11 成为 latest 后，旧网页可能直接提示“不支持跨 schema 更新”。这时不能靠反复点击检查更新取得桥接版，请按下面步骤指定固定的 0.20.3，勿用 bootstrap install.sh 覆盖现有安装。

## Linux：安装桥接主程序

若已安装 0.20.3／schema 7，跳到下一节。尚未安装时，由维护者向**现有独立更新器**提交指定版本请求。以下命令读取标准配置 `/etc/musicserver/updater.json`；自定义安装应先核对配置文件及队列路径。已有活动更新或维护状态时停止，按原恢复流程处理，不删除状态来强行继续。

```sh
sudo python3 - <<'PY'
import json, os, pathlib, secrets
config = json.loads(pathlib.Path('/etc/musicserver/updater.json').read_text())
state, queue = pathlib.Path(config['state']), pathlib.Path(config['queue'])
assert state.is_absolute() and queue.is_absolute() and queue.is_dir()
job_file = state / 'job.json'
if (state / 'maintenance').exists():
    raise SystemExit('Updater is in maintenance; finish recovery first.')
if job_file.exists() and json.loads(job_file.read_text()).get('phase') not in ('completed', 'rolled_back', 'failed'):
    raise SystemExit('An update is active; wait for it to finish.')
if any(queue.glob('*.json')):
    raise SystemExit('An update request is already queued.')
request_id = secrets.token_hex(16)
temporary = queue / ('.' + request_id + '.tmp')
fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as file:
    json.dump({'id': request_id, 'version': '0.20.3'}, file)
    file.flush()
    os.fsync(file.fileno())
os.replace(temporary, queue / (request_id + '.json'))
directory = os.open(queue, os.O_RDONLY | os.O_DIRECTORY)
try:
    os.fsync(directory)
finally:
    os.close(directory)
print('Queued signed bridge update 0.20.3:', request_id)
PY
sudo systemctl start musicserver-update.service
sudo systemctl status musicserver-update.service --no-pager
curl --fail http://127.0.0.1:8080/api/v2/health
```

使用自己的实际回环端口。确认任务完成、健康接口是 **version 0.20.3、schema 7**，并核对原有账户和歌单。现有 root 更新器按固定官方仓库的 v0.20.3 资产下载并验签，正常停机快照和回退仍有效。失败时先查看更新状态及日志，不直接切换 current 链接或覆盖数据库。

## Linux：维护独立更新器

1. 从 [正式 v0.20.3](https://github.com/ShenYunyao1/MusicServer/releases/tag/v0.20.3) 下载 Linux 归档、清单和签名。先使用可信源码的固定公钥验证清单及包摘要。可在已有 Go 的开发机执行 `go run ./cmd/verify-release -dir <下载目录>`；云服务器无需安装 Go。只把已验证文件传到服务器。
2. 将已验证的 0.20.3 归档解压到 root 拥有、组和其他用户不可写的维护目录。使用 **0.20.3 包内**的辅助程序，不能在当前主程序仍为 schema 7 时用 0.21.11 辅助程序跳过桥接。
3. 在该已验证目录执行：

```sh
sudo ./musicserver-updater \
  -config /etc/musicserver/updater.json \
  -refresh-worker 0.20.3
sudo cat /var/lib/musicserver-update/capabilities.json
```

确认命令成功、能力文件由 root 拥有且不可由组／其他用户写入，列出支持 schema 7、8 及 7→8 迁移。维护命令重新下载并核对正式包身份，替换固定 root 工具及能力文件；此步骤不切换主程序或迁移数据库。完整权限和失败恢复约束见 `deploy/updater-maintenance.md`。

## Linux：安装标签版

完成上述两步后，在 0.20.3 网页检查更新，确认安装 0.21.11 并重启。独立更新器会停服备份；新程序先检查完整性、外键及磁盘空间，保存独立旧格式快照，再事务升级。

升级成功后健康接口应显示 **version 0.21.11、schema 8**。重新登录并刷新网页，检查账户、Key、歌单及标签／组合筛选入口。原密码、播放器 Key、曲目 UUID、媒体摘要、路径、邮箱和配额保留。文件风格归一属于可选扫描整理，不自动创建猜测词条或覆盖人工标签。

自动安装的健康检查失败或中断时，更新器恢复匹配的旧程序和旧库快照。不要仅替换旧程序去打开已经升级的库；手动恢复时先停服，保留现场，并恢复程序与数据库的对应版本。

## Windows 与首次安装

Windows 服务器没有 Linux 的独立 root 更新器：备份后关闭程序，替换已验证的 `musicserver.exe`，保留运行数据并通过原 `start.bat` 启动。schema 7 自动生成旧格式快照并迁移为 8；无须先更换 Linux 辅助工具。失败时保留日志和 `.schema-7-to-8-*/snapshot.sqlite`，不要清库重新导入。

首次安装直接使用正式 0.21.11，创建 schema 8 空库并通过网页设置管理员。原有安装继续使用原密码，不重新初始化管理员。
