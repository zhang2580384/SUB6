# SUB6 0.1.4 构建

使用干净公开发行提交、Go 1.27.4、Node.js 22+、pnpm 9.15.9。

```sh
pnpm --dir frontend install --frozen-lockfile --ignore-scripts
make build-release
```

根 Makefile 先构建前端，再用 backend/Makefile 的 build-release 目标构建嵌入前端
的 Linux amd64 可执行文件。发行必须使用 BuildType=release；只设置版本号不能
恢复管理员更新按钮。保留 backend/third_party、docs/legal、resources 和许可证。
可用 RELEASE_OUTPUT 指定仓库外输出位置，RELEASE_COMMIT/RELEASE_DATE 指定该干净
公开提交的真实来源。用 `go version -m` 核对 vcs.revision 和 vcs.modified=false。

运行包布局与 0.1.2 相同，根目录为 sub6-linux-amd64、resources/、LICENSES/、
LICENSE、THIRD_PARTY_NOTICES.md、Dockerfile.prebuilt、README.txt、build-info.json。
checksums.txt 必须包含运行包 SHA256；完整对应源码需随发行提供。
build-info.json 增加 managed_upgrade 兼容声明，字段定义和迁移 fingerprint 算法
见 deploy/managed-updater/README.md。仅在审查真实兼容性后设置 rolling_compatible。

多容器部署需要运维先安装可选控制器；BuildType=release 本身不等于滚动升级。
复用本地已审查 runtime 镜像的离线 Docker 构建方法见 BUILD-v0.1.2.md，版本参数
改为 0.1.4，使用本发行二进制。旧文档中 BuildType=source 仅描述旧发行，不适用于
0.1.4 正式构建。有更新时显示“立即更新”，当前已是最新版本时不会显示该按钮。
