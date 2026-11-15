# SUB6

SUB6 是基于 [Ranxi Sub2API](https://github.com/ranxi2001/sub2api) 独立维护的开源 AI API 网关发行版，保留上游作者与贡献者署名。当前版本：**0.1.7**；安装包见 [Releases](https://github.com/zhang2580384/SUB6/releases)。基于 Ranxi 正式版 **v2.21.2**，精确同步 SHA：`7827a2f5f900ce50aafa37ab71ca1918dfe7cf8a`（正式标签提交 `dbed7697aa56e76c6eb48c4914fa441496b67bdd`）。

[English](README_EN.md) · [日本語](README_JA.md) · [升级与兼容性](maintenance/2026-11-14-upstream-review.md) · [部署说明](deploy/README.md)

本版本已整合稳定 0.1.5 的 WS 隔离修复；历史版本区分见 [稳定与候选分支说明](maintenance/2026-11-14-stable-candidate-sync.md)。

## 来源与功能

本版本保留已审查的 79 个上游提交范围、正式 0.1.5 的 WS 隔离修复，以及 0.1.6 的推理强度筛选和完整分组配色标签。0.1.7 修复更新资格预检，运维先安装协议 2 控制器后，可在旧节点继续服务时执行已审查的两项增量迁移；本版没有新增 SQL。未知迁移仍拒绝自动安装，原 0.1.6 人工安装声明保持不变。详见 [0.1.7 在线升级说明](maintenance/2026-11-15-v0.1.7-online-upgrade.md)、[上游审查](maintenance/2026-11-14-upstream-review.md) 与 [使用记录报告](maintenance/2026-11-14-v0.1.6-usage.md)。

保留多平台网关、分组与账号调度、WS/SSE、支付与计费、管理权限及 Grok 低 token 探活修复。独立 `deploy/direct-access` 辅助服务提供 IP 白名单、每累计成功入账 $100 一个名额、管理员加款计入累计、管理员指定总名额与操作审计。它需要单独配置，不会自动修改防火墙或主系统余额。

不包含已回退的定制融合生图分支。上游 Codex 图片桥接默认关闭；现有图片能力不等同于该定制功能。

## 从源码运行

安装 Go **1.27.4**、Node.js **22+** 和 pnpm **9.15.9**。先阅读脚本，再安装依赖：

```sh
pnpm --dir frontend install --frozen-lockfile --ignore-scripts
pnpm --dir frontend run build
cd backend
go mod download
go mod verify
CGO_ENABLED=0 go build -tags embed -trimpath -ldflags='-s -w -X main.Version=0.1.7 -X main.BuildType=source' -o sub6 ./cmd/server
./sub6
```

服务需要 PostgreSQL 和 Redis；隔离测试环境可使用 [源码 Compose](deploy/compose.sub6.yml)。它把管理入口绑定到本机，不附带任何可用生产配置或密码。TLS、受信任代理、备份和资源限制需按部署环境配置。现有部署升级前必须阅读维护报告；服务启动会执行数据库迁移。

Google OAuth 客户端未内置。Antigravity/Gemini CLI 登录和令牌刷新需先配置自有授权客户端；环境变量及迁移限制见维护报告与 `deploy/.env.example`。远程凭证守护地址默认为空，必须单独配置可信服务。

## 更新与发布

只使用 [SUB6 仓库](https://github.com/zhang2580384/SUB6) 和 [SUB6 Releases](https://github.com/zhang2580384/SUB6/releases)。0.1.7 提供源码及嵌入前端的 Linux amd64 二进制；未发布容器镜像。构建来源和运行时资源沿用 [构建说明](maintenance/BUILD-v0.1.6.md)，升级资格与协议以 [0.1.7 说明](maintenance/2026-11-15-v0.1.7-online-upgrade.md) 为准。内置更新器仅接受该仓库的 Release 下载路径，且强制要求 `checksums.txt`；源码版本没有兼容二进制资产时会拒绝在线替换。旧上游自动安装脚本已关闭，不能用它切回上游包。

本维护分支修正了发行构建误标为 `source` 而隐藏“立即更新”的问题。后续发行使用
`make build-release`（需先安装已审查的依赖），输出 `backend/bin/sub6-linux-amd64`，
构建类型为 `release`，保留检查更新、管理员点击更新和重启流程；普通本地源码构建
仍可保留 `source`。已发布的 0.1.1/0.1.2 资产和标签不作改写，它们仍是原有手动升级
模式，需要人工切换到后续修正版才能恢复网页升级。详见
[在线升级修复说明](maintenance/2026-11-13-online-updater-restoration.md)。

0.1.4 新增可选[托管滚动升级控制器](deploy/managed-updater/README.md)，由管理员
安装后同时管理主服务和 WS 网关、排空旧请求、保留存量连接并向网页报告进度。
协议 2 仅自动执行明确审查过的迁移计划，其他迁移变化须运维单独审查。独立 sidecar 仍需另行更新。

上游维护串行执行，逐次记录精确 SHA、审查、排除项和测试。公开仓库使用从已脱敏源码建立的独立历史，原始私人历史不会推送。详见 [同步流程](maintenance/SYNC.md)。CI 仅读取源码并验证，不连接或部署生产。

## 社区

SUB6 QQ 群：**1126616545**。

![SUB6 QQ 群](assets/community/sub6-qq-1126616545.png)

## 许可证

保留 [LGPL-3.0](LICENSE)、原作者版权与贡献记录。SUB6 的改动不抹除原作来源。新增内置 BEpusdt 引擎为 GPL-3.0，组合服务分发必须同时满足 GPL-3.0 的完整对应源码等义务；见 [第三方声明](THIRD_PARTY_NOTICES.md) 和 [许可证副本](LICENSES/BEpusdt-GPL-3.0.txt)。OpenAI 等协议名称和内部 Go 模块路径保留兼容用途。
