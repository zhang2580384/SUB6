# Responses WebSocket 线程隔离修复

日期：2026-11-14。本次以已公开的稳定 SUB6 0.1.4
`caa43cebc94b95b1eae78ad619d8275b771ab46c` 为基线回迁 WS 修复，
独立发行 SUB6 0.1.5。上游继续锁定
`ccf997c73868083d96c0bec56e71c1a34d26c98a`，没有引入其他上游改动、
依赖、数据库迁移或前端设置。

## 问题与行为

原接入把一个物理 WS 连接绑定到首次出现的线程，同时复用该连接的桥接历史、
工具状态、账号选择和计费生命周期。连接后续承载另一个真实线程时，会触发本地
身份切换拒绝；直接删除拒绝检查会造成上下文和工具结果混用。

现在一个物理连接由单一读取器分发到独立的逻辑会话。不同线程分别保留自己的
Gin 上下文、请求头副本、转发历史、上游连接生命周期、风控状态、用量和诊断记录。
线程切换后可以回到原线程续接；续轮省略正文线程标识时，执行状态和用量仍继承
该逻辑会话的身份，不回落到共享的静态 session 头。

命名 `stream_id` 是传输通道，不作为真实线程身份。不同命名通道可并行；同通道
跨线程仍按 FIFO 执行。`stream_id` 在转发旧版 WS 或 HTTP `/responses` 前移除，
下游事件再带回所属通道。未命名请求保持串行；单独使用默认通道的客户端保留
原来的关闭语义；多个逻辑会话共存时，一个会话的失败不会关闭其他会话。

真实的同类头部／正文身份冲突、非法身份仍在上游请求前拒绝。缺失身份继续遵守
原有严格模式开关。WS 的协议身份错误使用 `websocket_session_identity_*`，
Ops 类型为 `websocket_session_identity_rejected`，不再标成内容违规拦截。
没有改写客户端指纹、账号加密归属或注入提示词。

## 生命周期与资源边界

- 每个连接最多 32 个已使用的命名通道、65 个存活逻辑会话、16 个活动请求、
  8 个等待的 create；等待正文与控制消息合计受已有 WS 读取字节上限约束。
- 下一条 create 需要等当前轮计费回调交接、准入释放且终止消息写出后才能开始。
  失败后的账号重选仍属于原逻辑轮，不会把排队请求当成重试执行。
- 多通道连接的取消只取消本逻辑会话；核对 active response_id；已完成请求不能被
  再次取消。取消清理后返回 `response.cancelled`，同会话未执行的排队请求逐条
  返回 `websocket_request_not_executed`，不自动重放。重新使用该会话需提交完整上下文。
- 保留首次消息超时、正常断开、请求取消及接入租约失效的原关闭握手和日志。
  保留物理接入租约，以及各逻辑轮原有的权限、余额和并发准入。
- 各逻辑会话有独立的并发租约取消器和动态图片权限记录。一个逻辑租约失效只
  中断自己的上游；物理鉴权控制失效仍中断所有子会话。鉴权复核闭包只共享不可变
  的身份快照，始终读取当前轮权限；关闭错误仅在实际成功写出后标记，避免重复通知。
- opt-in 请求抓包按逻辑会话单独记录，容量不足时停止该会话的抓包，不回落到
  共享录制器，避免诊断中的 attempt／terminal 状态串线。

## 验证

以下检查通过，使用 Go 1.27.4、合成 localhost 上游与独立测试容器；测试阶段
容器禁用外网，CPU 上限为 2 核、内存上限为 8 GiB：

```sh
go test -tags unit -p 1 ./internal/handler ./internal/service \
  -run 'Test(.*WS|.*WebSocket|Issue600|ExtractOpenAIClientSessionID|.*SessionIdentity|APIKeyAdmission)' \
  -count=1 -timeout 300s
go test -p 1 ./internal/service/openai_ws_v2 ./internal/requestcapture \
  -count=1 -timeout 180s
go test -tags unit -p 1 ./internal/server/middleware \
  -run 'Test(APIKeyQueue|NewAPIKeyQueueAuth|APIKeyAdmissionOwner)' \
  -count=1 -timeout 300s
go test -race -tags unit -p 1 ./internal/handler ./internal/service ./internal/requestcapture \
  -run 'Test(OpenAIWSStream|OpenAIWSLogical|APIKeyAdmissionFork|Issue600(ClientIdentityNetwork|InitialIdentityPolicy|DisabledSessionBlockKeepsIdentityPolicy|NestedIdentityNetwork)|OpenAIResponsesWebSocket_.*FirstMessage|WebSocketFork)' \
  -count=1 -timeout 300s
go build -p 1 -o /tmp/sub6-ws-test ./cmd/server
```

最终鉴权隔离改动另行重跑了 WS／Issue600／准入回归、鉴权中间件回归、上述竞态
用例和后端构建，均通过。`git diff --check` 通过。

实际网络场景覆盖 HTTP bridge、passthrough、ctx_pool 的线程 A→B→A 续接、
省略身份的工具回传、逐轮用量隔离；命名通道并行及同通道 FIFO；取消一个活动
通道而另一通道继续完成；队列满、已完成响应取消拒绝、取消后未执行请求逐条通知；
真实头／正文冲突零上游调用、默认通道失败不影响命名通道、外来续接引用拒绝，
以及原有首次消息超时／取消／断开策略。相关 Issue600 既有断言仅按有意改变的
线程切换行为、错误分类和非法身份始终拒绝政策更新，账号命名空间与权限边界保留。
另通过真实 API Key 鉴权中间件及 Redis 准入验证：单会话租约失效会停止它的
HTTP 上游、不会中断并行会话或物理鉴权，后续密钥停用仍会拒绝续轮且不访问上游。

测试不包含客户流量、凭据或生产数据库。生产吞吐与真实上游可用性不在这些测试的
证明范围；没有进行真实付费请求或生产压测。

## 兼容边界与部署

本修复不是完整的官方 WS 多通道协议实现：跨命名通道引用另一会话的已知
`previous_response_id` 会返回 `previous_response_not_found`，要求移除该引用并提交
完整上下文；尚未支持跨通道 fork。上游未知、跨连接或缓存淘汰后的引用继续走
原有所有权与续接检查，不能把本地有限的 response 索引当作全局授权依据。
首次选中的新逻辑会话仍需提供模型名。命名通道本地取消会结束该逻辑会话，
不承诺保留其内存中的桥接历史。

上游轮换账号后无法解密原账号的历史，是独立的上游账号归属问题；本修复不会
通过伪造标识绕过它，也不会保证消除全部上游 403／409。

本补丁只回迁 WS 修复，迁移集合与 0.1.4 完全相同。正式 release 构建继续支持
管理员更新入口；多容器部署由可选托管控制器保留已有 WS 并排空旧主服务。
保留原镜像与配置，回退应用版本无需回退账务数据库。已有 WS 要重连后才会使用
新隔离逻辑；控制器自身无需更新。单 full 切换仍可能导致管理页面短暂重连。

协议参考：[OpenAI WebSocket mode](https://developers.openai.com/api/docs/guides/websocket-mode)。
