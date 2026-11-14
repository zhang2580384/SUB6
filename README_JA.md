# SUB6

SUB6 は [Ranxi Sub2API](https://github.com/ranxi2001/sub2api) を基に独立して保守するオープンソース配布版です。**0.1.6** は正式 0.1.5 の WS 分離修正、推論強度フィルターとグループの色付き完全表示を含みます。Ranxi v2.21.2 (`dbed7697aa56e76c6eb48c4914fa441496b67bdd`) を基に、production `7827a2f5f900ce50aafa37ab71ca1918dfe7cf8a` まで同期しています。正式 0.1.5 からは SQL 移行が 2 件あります。既存の管理コントローラーは直接のローリング更新を拒否するため、[0.1.6 移行・復旧手順](maintenance/UPGRADE-v0.1.6.md) に従って運用者が実行してください。[Releases](https://github.com/zhang2580384/SUB6/releases) と [機能検証記録](maintenance/2026-11-14-v0.1.6-usage.md) を参照してください。

[ビルド手順・中文](README.md) · [移行と互換性](maintenance/2026-11-14-upstream-review.md) · [配備](deploy/README.md)

Go 1.27.4、Node.js 22 以降、pnpm 9.15.9、PostgreSQL、Redis が必要です。依存関係は `--frozen-lockfile --ignore-scripts` で導入し、フロントエンドの後に `-tags embed` でバックエンドをビルドしてください。0.1.6 はソースとフロントエンドを内蔵した Linux amd64 バイナリを提供します。コンテナイメージは公開しません。[ビルドと実行条件](maintenance/BUILD-v0.1.6.md) を参照してください。既存の自動インストーラーは無効化しており、更新は SUB6 リポジトリの検証済み資産のみを受け付けます。

Grok の少量トークン監視修正と、累計入金・管理者加算・手動上限に対応した独立 IP 許可リストを保持します。撤回済みの独自画像オーケストレーションは含めません。本番環境は自動更新しません。起動時のデータベース移行前にバックアップと復元手順を確認してください。

QQ コミュニティ：**1126616545**。

![SUB6 QQ](assets/community/sub6-qq-1126616545.png)

元の LGPL-3.0、著作権表示、上流への帰属を保持します。組み込まれた BEpusdt は GPL-3.0 です。結合されたサーバーの配布には対応ソース提供などの GPL 条件も適用されます。[第三者ライセンス](THIRD_PARTY_NOTICES.md) を参照してください。

Optional operator-installed rolling updater: [deploy/managed-updater](deploy/managed-updater/README.md). It coordinates both main and WS gateway containers; migration changes require operator review.

This version includes the stable 0.1.5 WebSocket isolation fix. See [stable/candidate synchronization](maintenance/2026-11-14-stable-candidate-sync.md).
