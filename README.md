# SUB6

SUB6 是基于 [Ranxi Sub2API](https://github.com/ranxi2001/sub2api) 独立维护的开源 AI API 网关发行版，保留上游作者与贡献者署名。当前源码准备版：**0.1.6**，最新二进制发行是 **0.1.5**。基于 Ranxi 正式版 **v2.21.2**，精确同步 SHA：`7827a2f5f900ce50aafa37ab71ca1918dfe7cf8a`（正式标签提交 `dbed7697aa56e76c6eb48c4914fa441496b67bdd`）。

[English](README_EN.md) · [日本語](README_JA.md) · [升级与兼容性](maintenance/2026-11-14-upstream-review.md) · [部署说明](deploy/README.md)

本源码候选已合并稳定 0.1.5 的 WS 隔离修复；版本区分见 [稳定与候选分支说明](maintenance/2026-11-14-stable-candidate-sync.md)。

## 来源与功能

本次在既有追齐版本上审查 79 个新增上游提交，按文件保留运行代码和回归，排除上游 CI／演示脚本；精确范围和排除清单见 [维护报告](maintenance/2026-11-14-upstream-review.md)。新增两项数据库迁移，尚未发布 0.1.6 二进制或开放托管一键升级。

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
CGO_ENABLED=0 go build -tags embed -trimpath -ldflags='-s -w -X main.Version=0.1.6 -X main.BuildType=source' -o sub6 ./cmd/server
./sub6
```

服务需要 PostgreSQL 和 Redis；隔离测试环境可使用 [源码 Compose](deploy/compose.sub6.yml)。它把管理入口绑定到本机，不附带任何可用生产配置或密码。TLS、受信任代理、备份和资源限制需按部署环境配置。现有部署升级前必须阅读维护报告；服务启动会执行数据库迁移。

Google OAuth 客户端未内置。Antigravity/Gemini CLI 登录和令牌刷新需先配置自有授权客户端；环境变量及迁移限制见维护报告与 `deploy/.env.example`。远程凭证守护地址默认为空，必须单独配置可信服务。

## 更新与发布

只使用 [SUB6 仓库](https://github.com/zhang2580384/SUB6) 和 [SUB6 Releases](https://github.com/zhang2580384/SUB6/releases)。稳定 0.1.5 提供源码及嵌入前端的 Linux amd64 二进制；未发布容器镜像。构建来源和运行时资源见 [构建说明](maintenance/BUILD-v0.1.5.md)。内置更新器仅接受该仓库的 Release 下载路径，且强制要求 `checksums.txt`；源码版本没有兼容二进制资产时会拒绝在线替换。旧上游自动安装脚本已关闭，不能用它切回上游包。

本维护分支修正了发行构建误标为 `source` 而隐藏“立即更新”的问题。后续发行使用
`make build-release`（需先安装已审查的依赖），输出 `backend/bin/sub6-linux-amd64`，
构建类型为 `release`，保留检查更新、管理员点击更新和重启流程；普通本地源码构建
仍可保留 `source`。已发布的 0.1.1/0.1.2 资产和标签不作改写，它们仍是原有手动升级
模式，需要人工切换到后续修正版才能恢复网页升级。详见
[在线升级修复说明](maintenance/2026-11-13-online-updater-restoration.md)。

0.1.4 新增可选[托管滚动升级控制器](deploy/managed-updater/README.md)，由管理员
安装后同时管理主服务和 WS 网关、排空旧请求、保留存量连接并向网页报告进度。
含数据库迁移变化的版本须运维单独审查，独立 sidecar 仍需另行更新。

上游维护串行执行，逐次记录精确 SHA、审查、排除项和测试。公开仓库使用从已脱敏源码建立的独立历史，原始私人历史不会推送。详见 [同步流程](maintenance/SYNC.md)。CI 仅读取源码并验证，不连接或部署生产。

## 社区

SUB6 QQ 群：**1126616545**。

![SUB6 QQ 群](assets/community/sub6-qq-1126616545.png)

## 许可证

保留 [LGPL-3.0](LICENSE)、原作者版权与贡献记录。SUB6 的改动不抹除原作来源。新增内置 BEpusdt 引擎为 GPL-3.0，组合服务分发必须同时满足 GPL-3.0 的完整对应源码等义务；见 [第三方声明](THIRD_PARTY_NOTICES.md) 和 [许可证副本](LICENSES/BEpusdt-GPL-3.0.txt)。OpenAI 等协议名称和内部 Go 模块路径保留兼容用途。
