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

托管自动更新仅接受 controller_protocol=1、deployment_probe_protocol=1、
rolling_compatible=true 且 migrations_fingerprint 与当前完全一致的发行包。
fingerprint 是按排序后的 backend/migrations/*.sql 文件，将每个相对路径、NUL、
原始内容、NUL 顺序拼接后计算 SHA256。新增迁移必须由运维先审查和执行兼容方案。
回滚只允许已保留且迁移集合相同的版本；不还原整个数据库，不覆盖升级期间的账单。
新控制器代码本身需要运维另行审查更新，不通过网页远程替换。

测试：`python3 -m unittest discover -s deploy/managed-updater -p test_controller.py -v`。
