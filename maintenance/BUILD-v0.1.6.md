# SUB6 0.1.6 构建与发行契约

从最终干净公开提交使用 Go 1.27.4、Node.js 22+、pnpm 9.15.9 构建。先审查依赖和脚本，安装时不运行依赖钩子：

```sh
pnpm --dir frontend install --frozen-lockfile --ignore-scripts
make build-release
```

根构建先生成前端，再以 CGO_ENABLED=0、`-tags embed -trimpath` 构建。Version=0.1.6、BuildType=release、Commit 和 Date 必须来自最终公开提交。运行 `go version -m` 核对 linux/amd64、CGO=0、vcs.revision 和 vcs.modified=false，并实际执行 `-version` 核对版本及来源。运行时和 build-info 的上游同步均为 `7827a2f5f900ce50aafa37ab71ca1918dfe7cf8a`，正式标签提交为 `dbed7697aa56e76c6eb48c4914fa441496b67bdd`。

最小发行资产为：

- `sub6-linux-amd64`：嵌入前端的真实发行二进制。
- `sub6_0.1.6_linux_amd64.tar.gz`：归档根目录为该二进制、resources/、LICENSES/、LICENSE、THIRD_PARTY_NOTICES.md、Dockerfile.prebuilt、README.txt 和 build-info.json，遵循既有 SUB6 更新器布局。
- `sub6_0.1.6_source.tar.gz`：最终公开提交的逐文件对应源码，包含本地第三方模块、许可证和构建说明。
- `build-info.json`：记录最终 source_commit/tree、目标平台、构建方式、上游、二进制 hash/size 及下述兼容声明。
- `checksums.txt`：上述四项资产的 SHA256。发布后再下载并核对 GitHub digest、大小、内容及源码 blob／文件模式。

真实迁移文件集合为 421 项，fingerprint 为 `63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b`。不复用正式 0.1.5 的 fingerprint。对本包的 `managed_upgrade` 声明为：

```json
{
  "rolling_compatible": false,
  "controller_protocol": 1,
  "deployment_probe_protocol": 1,
  "migrations_fingerprint": "63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b",
  "installation_mode": "operator-maintenance",
  "upgrade_guide": "maintenance/UPGRADE-v0.1.6.md"
}
```

现有控制器会在启动候选容器前拒绝本包；人工维护安装通过不等于原有按钮可以直接滚动升级。此版本不扩展更新协议、不修改控制器运行代码、不自动更改控制器状态。执行 [升级与回退流程](UPGRADE-v0.1.6.md) 后才能登记真实新状态；跨迁移集合的应用回退也需人工维护，保留新账务及账本。

现有管理员检查更新入口、原位包格式和真实 release 构建保留。独立 sidecar 不包含在主程序更新中。服务仍需 PostgreSQL、Redis、CA 证书、时区数据、resources/、既有配置和加密密钥。保留 SUB6 品牌、LGPL 归属及 GPL-3.0 BEpusdt 的对应源码义务；不发布第三方容器镜像，不恢复定制融合生图。
