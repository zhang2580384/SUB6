# SUB6

SUB6 是基于 [Ranxi Sub2API](https://github.com/ranxi2001/sub2api) 独立维护的开源 AI API 网关发行版，保留上游作者与贡献者署名。本分支源码版本为 **0.1.16**，正式资产以[发行页](https://github.com/zhang2580384/SUB6/releases)为准，基于已审查 Ranxi **v2.21.3**，精确同步 SHA：`aa8e596e6d6f3e4648d6beba73b39b240dd5357a`。发行与生产部署分别验收。

[English](README_EN.md) · [日本語](README_JA.md) · [升级与兼容性](maintenance/2026-11-14-upstream-review.md) · [部署说明](deploy/README.md)

本版本已整合稳定 0.1.5 的 WS 隔离修复；历史版本区分见 [稳定与候选分支说明](maintenance/2026-11-14-stable-candidate-sync.md)。

## 来源与功能

0.1.16 在「系统设置 → 使用记录」增加请求流量采集开关，修正模型广场 `deepseek-pro` 的官方参考价，并在原账号单模型暂时冷却时向续聊客户端返回重试时间。详见[升级说明](maintenance/UPGRADE-v0.1.16.md)。

0.1.15 移植了 Wei-Shaw v0.2.24 的网关、计费、账号可用性和界面更新。新增 Exa 搜索、Claude 费用估算和模型 ID 复制。网关会拒绝含重复字段的请求，文献搜索权限不足时只结束本次请求。详见[升级说明](maintenance/UPGRADE-v0.1.15.md)。

0.1.14 的渠道监控 V4 在 90 分钟、24 小时、7 天和 30 天四个范围均可显示高位缓存率（P90）。将鼠标移到数值上，可查看样本数和同期总体缓存率。后台可选择「时间段采样」或「近18次有采样」。详见[升级说明](maintenance/UPGRADE-v0.1.14.md)。

0.1.11 保留完整 0.1.9，并修复普通 WS→HTTP 桥接在 `store=false` 时仅携带 `previous_response_id` 的首次续接。支持的明文会话在原逻辑连接内保留助手说明和已完成工具调用，全量历史不重复，多会话独立；账号、凭据、模型、路由和交付检查继续生效。详见 [修复范围与限制](maintenance/2026-11-17-ws-http-bridge-history.md)。本次没有新增 SQL、依赖或设置。

本版整合正式 v2.21.3 的内容审核范围、人工任务租约、临时冷却、HTTP/WS 会话恢复和诊断修复，保留正式 0.1.5 的 WS 隔离、0.1.6 的推理筛选与分组配色及连接池关闭修复。新一轮没有增加 SQL 迁移；相对正式 0.1.5 仍有两项迁移。本版已整合 0.1.7 的升级资格预检及经审查的在线计划，需先由运维安装协议 2 控制器；未知迁移继续拒绝自动安装。提供完整安装包和容器镜像归档；协议 2 更新器须通过预检后安装。新增 Kimi/GLM/Gemini 计价修复；Carbon 到期时间及自定义价卡优先级见 [计价说明](maintenance/PRICING-gemini-20261115.md)。合并说明见 [稳定修复同步](maintenance/2026-11-15-v0.1.8-stable-sync.md)。具体范围、排除项与验证见 [本轮审查](maintenance/2026-11-15-upstream-review.md)。

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
CGO_ENABLED=0 go build -tags embed -trimpath -ldflags='-s -w -X main.Version=0.1.14 -X main.BuildType=source' -o sub6 ./cmd/server
./sub6
```

服务需要 PostgreSQL 和 Redis；隔离测试环境可使用 [源码 Compose](deploy/compose.sub6.yml)。它把管理入口绑定到本机，不附带任何可用生产配置或密码。TLS、受信任代理、备份和资源限制需按部署环境配置。现有部署升级前必须阅读维护报告；服务启动会执行数据库迁移。

Google OAuth 客户端未内置。Antigravity/Gemini CLI 登录和令牌刷新需先配置自有授权客户端；环境变量及迁移限制见维护报告与 `deploy/.env.example`。远程凭证守护地址默认为空，必须单独配置可信服务。

## 更新与发布

只使用 [SUB6 仓库](https://github.com/zhang2580384/SUB6) 和 [SUB6 Releases](https://github.com/zhang2580384/SUB6/releases)。正式发行包提供完整源码、嵌入前端的 Linux amd64 二进制、运行资源包和干净构建的 Docker 镜像归档（未向镜像仓库发布）。精确来源、镜像 ID、基础镜像与资产摘要见发行 build-info.json 和 checksums.txt；参阅 [构建说明](maintenance/BUILD-v0.1.16.md) 与 [升级说明](maintenance/UPGRADE-v0.1.16.md)。内置更新器仅接受该仓库的 Release 下载路径，且强制要求 `checksums.txt`；源码版本没有兼容二进制资产时会拒绝在线替换。旧上游自动安装脚本已关闭，不能用它切回上游包。

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
