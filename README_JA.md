# SUB6

SUB6 は [Ranxi Sub2API](https://github.com/ranxi2001/sub2api) を基に独立して保守する配布版です。[正式版](https://github.com/zhang2580384/SUB6/releases/latest) は審査済みの Ranxi **v2.21.3** (`aa8e596e6d6f3e4648d6beba73b39b240dd5357a`) に基づきます。0.1.5 の WS 分離、0.1.6 のフィルターと配色、0.1.7 の更新修正を保持し、Kimi/GLM/Gemini の料金を修正します。0.1.7 以降の追加 SQL 移行はありません。プロトコル 2 の更新前確認が必要です。[移行条件](maintenance/UPGRADE-v0.1.17.md)、[料金と期限](maintenance/PRICING-gemini-20261115.md)、[上流審査](maintenance/2026-11-15-upstream-review.md) を参照してください。本番配備は別途承認が必要です。

[ビルド手順・中文](README.md) · [移行と互換性](maintenance/2026-11-14-upstream-review.md) · [配備](deploy/README.md)

Go 1.27.4、Node.js 22 以降、pnpm 9.15.9、PostgreSQL、Redis が必要です。依存関係は `--frozen-lockfile --ignore-scripts` で導入し、フロントエンドの後に `-tags embed` でバックエンドをビルドしてください。正式版は完全なソース、フロントエンド内蔵 Linux amd64 バイナリ、実行資源と Docker イメージアーカイブを提供します（レジストリへの公開はありません）。[ビルドと実行条件](maintenance/BUILD-v0.1.17.md) を参照してください。既存の自動インストーラーは無効化しており、更新は SUB6 リポジトリの検証済み資産のみを受け付けます。

Grok の少量トークン監視修正と、累計入金・管理者加算・手動上限に対応した独立 IP 許可リストを保持します。撤回済みの独自画像オーケストレーションは含めません。本番環境は自動更新しません。起動時のデータベース移行前にバックアップと復元手順を確認してください。

QQ コミュニティ：**1126616545**。

![SUB6 QQ](assets/community/sub6-qq-1126616545.png)

元の LGPL-3.0、著作権表示、上流への帰属を保持します。組み込まれた BEpusdt は GPL-3.0 です。結合されたサーバーの配布には対応ソース提供などの GPL 条件も適用されます。[第三者ライセンス](THIRD_PARTY_NOTICES.md) を参照してください。

Optional operator-installed rolling updater: [deploy/managed-updater](deploy/managed-updater/README.md). It coordinates both main and WS gateway containers; migration changes require operator review.

This version includes the stable 0.1.5 WebSocket isolation fix. See [stable/candidate synchronization](maintenance/2026-11-14-stable-candidate-sync.md).
