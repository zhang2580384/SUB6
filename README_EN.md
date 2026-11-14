# SUB6

SUB6 is an independently maintained open-source distribution of [Ranxi Sub2API](https://github.com/ranxi2001/sub2api). Version **0.1.6** includes the stable 0.1.5 WebSocket isolation repair, reasoning-effort usage filters and complete colored group labels. It is based on Ranxi v2.21.2 (`dbed7697aa56e76c6eb48c4914fa441496b67bdd`), synchronized at production `7827a2f5f900ce50aafa37ab71ca1918dfe7cf8a`. There are two SQL migrations relative to stable 0.1.5. Its existing managed controller rejects a direct rolling upgrade; use the operator maintenance procedure in [the 0.1.6 upgrade guide](maintenance/UPGRADE-v0.1.6.md). See [Releases](https://github.com/zhang2580384/SUB6/releases) and [feature validation](maintenance/2026-11-14-v0.1.6-usage.md).

Google OAuth application credentials are not embedded. Antigravity and Gemini CLI login/refresh require explicit client configuration; see `deploy/.env.example` and the maintenance report. Remote credential-service endpoints default to empty and must be configured independently.

[中文与完整构建命令](README.md) · [Upgrade compatibility](maintenance/2026-11-14-upstream-review.md) · [Deployment](deploy/README.md)

Requires Go 1.27.4, Node.js 22+, pnpm 9.15.9, PostgreSQL and Redis. Install frontend dependencies with `--frozen-lockfile --ignore-scripts`, build the frontend, then build `backend/cmd/server` with `-tags embed`. The optional direct-access sidecar implements cumulative-credit quotas, administrator credit inclusion and audited manual total overrides. It does not write main balances. The rolled-back custom image orchestrator is excluded; the upstream Codex image bridge remains off by default.

Only [SUB6 source and releases](https://github.com/zhang2580384/SUB6/releases) belong to this distribution. Version 0.1.6 provides source and an embedded-frontend Linux amd64 release binary with administrator update controls; no container image is published. See [build and runtime requirements](maintenance/BUILD-v0.1.6.md). Online updates require a compatible asset and mandatory checksums from this repository. CI never deploys production. Database migrations run at startup; read the maintenance report before upgrading and retain restorable backups.

Community QQ: **1126616545**.

![SUB6 community](assets/community/sub6-qq-1126616545.png)

Original LGPL-3.0 licensing, copyrights and upstream attribution are preserved. The linked BEpusdt engine is GPL-3.0; distributing the combined server must satisfy its corresponding-source and other GPL requirements. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Optional operator-installed rolling updater: [deploy/managed-updater](deploy/managed-updater/README.md). It coordinates both main and WS gateway containers; migration changes require operator review.

This version includes the stable 0.1.5 WebSocket isolation fix. See [stable/candidate synchronization](maintenance/2026-11-14-stable-candidate-sync.md).
