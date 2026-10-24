# 账户删除与 Key 直接删除开发计划

日期：2026-10-24。用户已授权本地实现；不推送、不发布、不操作云端。

## 规则

- 主动删除单个 Key、删除本人全部 Key、重置密码时选中清除 Key、删除账户，均物理删除 Key、授权、scopes 和调用统计。删除后不可轮换恢复，晚结束的请求不得重建统计；审计保留不含密钥的操作记录。
- 单纯停用账户仍仅暂停 Key，重新启用后未删除、未过期的同一 Key 恢复使用。
- 管理员只能删除已停用且名下管理的曲目、非系统歌单均已接管的账户；不允许删除当前操作账户。删除采用版本检查、事务内重新校验和审计。
- 账户为逻辑删除：保留 UUID、原始登录名／昵称和上传者引用，从用户列表隐藏，永久禁用、清除密码凭据／会话／Key、个人订阅及邀请。登录名继续占用，不能重置密码或重新启用。
- 删除时间以 `meta` 中的 `deleted_user/<UUID>` 持久化标记保存，复用现有元数据表，保持 schema 7 和现有数据；所有账户读取统一带出该状态，管理写操作拒绝已删除账户。待过期上传暂存由现有回收机制清理，不能再提交。若固定扫描归属是被删除账户，原子改为当前管理员。
- 用户列表默认隐藏已删除账号，不提供删除恢复入口；曲目音频、已接管歌单和其他用户资源不受删除影响。已有下载无法收回。

## 阶段与验证

- [x] D00：核对 Key 撤销、统计、账户状态和资源外键，记录规则与实现顺序。
- [x] K01：`key_delete.go`、`store.go`、`accounts.go`、`users.go`、Key 页面：主动撤销改为直接删除，更新单个／全部／重置的回归；验证跨账户拒绝、失败回滚、统计不复活及停用恢复，沿用 `stats.go` 的不存在 Key 不写统计规则。
- [x] D01：新增 `account_delete.go`、扩展 `accounts.go`／`users.go`／`users_http.go`／`ownership.go`／`account_policy.go`：删除标记与守卫、资源约束、凭据／订阅／邀请清理、扫描归属及事务审计；验证版本、权限、并发边界、持久化和其他账户隔离。
- [ ] D02：`users.js`、`administration.js`：已停用账户的删除入口、确认与失败提示，删除后回用户列表；增加隔离浏览器覆盖删除 Key、停用恢复、资源接管及账户删除，更新 README 并固定路径编译。
- [ ] D03：用户另行要求后推送，Actions 完整回归与正式发行另行验收；云端由用户网页更新。

先运行能复现原行为的针对性测试，再实现。完整跨平台 Go／race 交后续 Actions；本地仅运行相关 Go／vet、隔离浏览器和必要构建。每阶段通过后勾选、记录证据并创建本地提交。

## 验收记录

- K01：先复现撤销后 api_keys 仍有记录且可以轮换恢复，再将单个／全部／重置选项统一改为物理删除；授权与 scopes 使用既有外键级联，统计与操作审计同事务。启动清理旧已撤销记录，保留有效及过期但未删除的 Key。`go test ./internal/music -run '^TestKeyDeletion|^TestStats|^TestBulkKey|^TestUsers|^TestPasswordChangeResetAndRestart|^TestAPIKey' -count=1 -timeout 90s`、额外启动清理专项、`go vet ./internal/music`、相关 JS 语法和 diff 检查通过。覆盖删除后不复活／不重建统计、跨用户拒绝、重置选项、其他账户保留、清理失败回滚及停用再启用同 Key；网页完整验收留待 D02。CI 待推送。
- D01：先复现 DELETE 接口缺失 404，再实现受版本保护的逻辑删除。`go test ./internal/music -run '^TestAccountDeletion|^TestKeyDeletion|^TestUsers|^TestProfile|^TestMultiuser|^TestPassword|^TestRealtime|^TestStage|^TestUpload|^TestSharing' -count=1 -timeout 90s` 和 `go vet ./internal/music` 通过；新增暂存无身份参数提交专项后单跑 TestAccountDeletion 通过。覆盖启用／本人／未接管资源拒绝、CSRF／成员／Key 拒绝、失效版本、凭据与订阅邀请清理、上传者和转移资源保留、删除持久化／schema 7／外键完整性、旧名称占用、重置及重新启用拒绝、扫描归属转移和失败回滚。独立复核发现管理写请求仅入口鉴权的交错窗口，先以阻塞正文复现已删除管理员仍能创建／修改／重置其他账户（201／200／200），再为创建／重置与管理更新增加事务身份复核；本人昵称、console 和初始化保持原边界。复核确认修复后无待修问题。Linux Actions race 增加账户／Key 删除专项，CI 待后续推送执行。
