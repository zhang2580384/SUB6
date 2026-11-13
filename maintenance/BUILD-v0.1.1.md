# SUB6 0.1.1 Linux 构建和来源

源码以本发行标签/提交为准，不复用继承的私人 Git 历史。依赖：Go 1.27.4、
Node.js 22+、pnpm 9.15.9。Go replace 包含本仓库的 BEpusdt 和 SQLite 源码，
不能省略 backend/third_party。前端还需要 docs/legal；其产物被嵌入 Go 二进制。

在干净公开源码检出中，从仓库根执行（输出目录选在仓库外）：

```sh
pnpm --dir frontend install --frozen-lockfile --ignore-scripts
pnpm --dir frontend run build
SUB6_COMMIT=$(git rev-parse HEAD)
SUB6_DATE=$(git show -s --format=%cI HEAD)
cd backend
GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -mod=readonly -trimpath -tags embed \
  -ldflags="-s -w -X main.Version=0.1.1 -X main.Commit=$SUB6_COMMIT -X main.Date=$SUB6_DATE -X main.BuildType=source" \
  -o /absolute/output/sub6-linux-amd64 ./cmd/server
go version -m /absolute/output/sub6-linux-amd64
sha256sum /absolute/output/sub6-linux-amd64
```

二进制为 Linux amd64、CGO=0，运行需要 CA 证书、时区及已有 PG/Redis/配置。
`backend/resources` 是运行时回退资源，需保持在工作目录的 resources/。
保留原入口、服务用户、挂载、加密密钥、SMTP 中转/解析与 gateway/full 角色。
BuildType=source 保持人工升级流程；不为此次升级启用网页原位二进制替换。
二进制 `-version` 显示构建提交和上游来源，SHA256 以 Release/私有交付清单为准。

如果复用已经核对的本地 runtime 镜像，将二进制、resources/、LICENSES/ 和
THIRD_PARTY_NOTICES.md 放入独立构建上下文，复制 Dockerfile.prebuilt 后执行：

```sh
docker image inspect verified-local-runtime:existing
docker build --pull=false --network=none -f Dockerfile.prebuilt \
  --build-arg RUNTIME_IMAGE=verified-local-runtime:existing \
  --build-arg SUB6_SOURCE_COMMIT=REVIEWED_PUBLIC_COMMIT \
  -t sub6:0.1.1 .
```

示例镜像名称是占位符，不会提供或拉取生产镜像。先核对该本地镜像 ID、
架构、/app/sub2api 入口、用户/权限、运行库、健康检查和资源路径；本任务
不声称已构建/验证每个现有 runtime 镜像。不要将含现场配置的镜像公开推送。

独立副本迁移演练：使用副本专属数据库配置执行
`sub6-linux-amd64 --migrate-only --migration-timeout=30m`，重复执行应幂等。
这会写入指定数据库，因此只在运维另行控制的目标执行；源码 CI 使用一次性
测试库。完整切换/回滚边界见 2026-11-13-v0.1.1-upgrade.md。
