# 托管的主服务 / WS 网关滚动升级

此控制器是可选的 Linux Docker 运维组件，必须由管理员安装。普通单进程安装
保留原来的原位更新方式；设置托管控制器后，更新、回滚和重启只委托给控制器，
控制器失联时不会退回到退出当前进程。

控制器需要 Python 3.11+、Docker CLI 和已存在的本地 runtime 镜像。它不拉取
第三方运行脚本，只下载本仓库最新稳定 Release，校验 SHA256、标签对应源码提交、
构建模式和兼容声明，然后离线 COPY 构建镜像。校验和证明文件完整性；它不能
代替对发行源码和发布账号的信任审查。

## 安装与权限

1. 审查 controller.py、systemd unit 和站点代理配置，保存现有容器 inspect、
   Compose、代理配置和恢复步骤到私有目录。不要公开配置文件。
2. 安装控制器到 `/opt/sub6-updater/controller.py`，配置到
   `/etc/sub6-updater/config.json`，权限 root:root 0600。
3. 创建持久目录 `/opt/sub6-updater/control`，root:应用 GID，0750。
   使用 `python3 -c 'import secrets; print(secrets.token_hex(32))'` 生成 token，
   保存为该目录的 `token`，root:应用 GID，0440。不要放进源码或日志。
   控制器启动后创建 `control.sock`，0660。目录持久化可避免重启后 token 丢失。
4. 按 config.example.json 填写真实本地路径、网络、旧容器和端点；确认两个模板
   的角色分别为 full 和 gateway，入口会正确切换至应用用户，现有 env、数据、
   内存限额、SMTP host 映射均准确。示例地址不可直接部署。
5. 安装并启动 sub6-updater.service。只有主服务需要把控制目录只读挂载到
   `/run/sub6-updater`，并设置 `SUB6_UPDATE_CONTROLLER_SOCKET=/run/sub6-updater/control.sock`
   及 `SUB6_UPDATE_CONTROLLER_TOKEN_FILE=/run/sub6-updater/token`。无需挂载 Docker socket。
   控制器会为后续候选容器添加这些设置。首次从旧版升级由运维通过 Unix socket
   发起 `POST /v1/operations`，JSON 为 `{"action":"update","version":"0.1.3"}`，
   Authorization 使用本地 token；不要把 token 粘贴到共享终端或网页。

## 切换与恢复边界

先启动新 gateway，将新模型请求导向它，保留旧 WS 连接。临时拒绝管理写操作，
等待旧主服务请求连续两次为零，再停止旧 full、启动新 full 并恢复代理路由。
旧版必须明确配置 deployment_probe_protocol=0，使用所有入口 Caddy 的在途计数；
新版使用协议 1 的鉴权探针。不要为探针异常的新版启用旧版兜底。

所有代理必须由控制器覆盖；它只支持示例中的 Caddy reverse_proxy 块结构，
安装前要验证真实站点。WS 块须保留足够的 stream_close_delay，防止 Caddy
reload 关闭已有连接。旧 gateway 会一直保留到没有已建立的入站 TCP 连接。
请求未排空时操作失败并恢复旧路由，不强制打断。管理页面、登录或支付回调在
主服务切换时可能短暂重试；这是单 full 设计，不能承诺完全无感或零中断。

进度通过管理员 `/api/v1/admin/system/update-status` 轮询。操作失败或进程异常
重启要先检查 root 私有状态与代理现场；不要盲目删除 state.json 后再发起升级。
Compose 实际镜像、数据目录及网络会写入 compose.active.json；应使用该文件，
避免旧 Compose 把实例重建回旧版。保留旧镜像、数据和最近三次部署信息；本工具
不会删除历史容器/镜像/数据。运维需要另行规划磁盘清理。

托管更新协议 2 增加升级前只读检查：`GET /v1/preflight?version=0.1.7`。
管理员界面保留新版提示，但预检拒绝或不可用时显示具体原因，不启动安装；
提交更新时还会重新预检。旧控制器没有该接口，须先由运维审查和更新控制器文件。
控制器安装不会重启业务容器。不要用应用原位替换绕过托管模式。

迁移集合相同的包继续兼容协议 1。只有 `rolling_compatible=true`、
`deployment_probe_protocol=1` 的正式发行包可以自动安装。协议 2 仅增加一个
已审查的在线计划 `usage-platform-v1`：精确的 0.1.5 迁移 fingerprint
`a6990935dd92d8132f2d487ba2b3320ac10d8819e72363db2d240bca23e4063f`
到 343/344 两项新增后的
`63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b`。
包还必须声明 `controller_protocol=2` 和 `online_migration_plan=usage-platform-v1`。
不会依据包提供的任意脚本、SQL、URL 或路径增加允许范围。

使用该计划须在运维私有配置增加 `migration_database`，包含已有 PostgreSQL
容器名 `container`、`user`、`database` 和宿主数据盘路径 `data_directory`。
安装前检查主库可写、长事务、可用空间、现有字段及新增迁移校验和。使用已验证的
新程序运行 `--migrate-only --migration-timeout=10m`，限制锁等待 2 秒、语句 10 分钟；
旧服务继续运行。迁移完成后核对可空字段、有效索引及两项真实账本，才启动候选节点。
锁超时或索引中断会保留旧服务，后续由同一迁移器重试；不能手工补记成功。
迁移扫描会消耗 I/O 和 WAL，升级前仍需保存可恢复备份并评估业务规模。

真实数据库状态独立记录为 `database_migrations_fingerprint`；即使候选启动或切换失败，
也不会回写旧值冒充撤销迁移。新增列、索引和交易保持不动。跨该边界仅允许回退到
已保留且验证过的正式 0.1.5 精确提交；其他跨迁移回退仍拒绝。

fingerprint 是按排序后的 backend/migrations/*.sql 文件，将每个相对路径、NUL、
原始内容、NUL 顺序拼接后计算 SHA256。所有其他新增迁移仍需独立审查和兼容方案。
不还原整个数据库，不覆盖升级期间的账单。
新控制器代码本身需要运维另行审查更新，不通过网页远程替换。

SUB6 0.1.6 相对正式 0.1.5 增加两项平台快照迁移，真实 fingerprint 不同，
发行明确声明 rolling_compatible=false。当前控制器会拒绝安装，这是预期保护。
使用[0.1.6 人工维护窗口和应用回退流程](../../maintenance/UPGRADE-v0.1.6.md)，
不要修改 fingerprint、账本或此检查来绕过门禁；网页 has_update 仅表示发现新版本。
后续 0.1.7 包具有独立的协议 2 声明，可按上述已审查的在线计划升级；先更新控制器。

测试：`python3 -m unittest discover -s deploy/managed-updater -p test_controller.py -v`。
