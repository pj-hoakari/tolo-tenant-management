# Tenant Management 入出力仕様

package: `tolo.tenant.v1`
役割: テナントとイベントの識別子の正本
他サービス（Relation、グラフ編集、観測）は `GetEvent` で参照整合を取る

## RPC 一覧

| RPC | 説明 | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| RegisterTenant | テナント（顧客組織）をセルフサインアップで登録する。IdP 認証済みかつ作成しようとしているテナントに未所属のユーザーが自分をオーナーとして登録 | 管理 UI | `tenant.register`（登録専用の最小トークン、テナント文脈なし） | TenantRegistered |
| ChangeTenantContract | テナントの契約プランを変更する | 管理 UI | `tenant_access` + tenant.write | TenantContractChanged |
| ArchiveTenant | テナントを論理削除（アーカイブ）する。識別子と配下データは保持 | 管理 UI | `tenant_access` + tenant.write | TenantArchived |
| CreateEvent | テナント配下にイベント（催事／設置）を新設する | 管理 UI | `tenant_access` + events.write | EventCreated |
| AssignEventType | イベント種別（短期／長期）を設定する。短期は観測縮退の対象 | 管理 UI | `tenant_access` + events.write | EventTypeAssigned |
| TransitionEventStatus | イベント状態を遷移させる（draft→open→locked→closed→archived。逆遷移も許容） | 管理 UI | `tenant_access` + events.write | EventOpened／EventLocked／EventClosed／EventUnlocked／EventReopened／EventArchived／EventUnarchived |
| GetEvent | イベントを参照する。存在しない ID への所属・グラフ作成を防ぐ参照整合の要 | Relation、グラフ編集、観測 | サービス資格情報 | （参照のみ） |
| ListEvents | テナント配下のイベントを一覧する | 管理 UI、スタッフアプリ | `tenant_access` + events.read | （参照のみ） |

## 補足

- 許容外の状態遷移は `failed_precondition` を返す
- アーカイブは論理削除であり `GetEvent` は archived でも返す（宙づり参照を生まない）
- アーカイブ済みテナント／イベントの扱い: 書き込み系 RPC は `failed_precondition`、参照系は継続
  既存所属は保持し、archived イベントへの Token Exchange は拒否
- ロール種別の定義はテナントが所有するが、所属の読み書きは Relation が所有するため RPC は Relation 側
- Realtime 配信ログの保持期間などテナント設定値の追加項目（Event の設定への追加）は実装フェーズで決定
- RegisterTenant はセルフサインアップ
  成功時は作成者をオーナーとして所属させる（Relation の AddTenantMember を内部で伴う）
  オンボーディング全体は「アカウント作成（Auth の /signup）→ テナント登録・紐づけ（本 RPC）→ 再認可で tenant_access 取得」の流れ
  課金・プロビジョニング導入時に再設計の余地を残す
- RegisterTenant の補償規約: AddTenantMember が失敗した場合は RegisterTenant 全体を失敗として返し、作成済みテナントを取り消す
  オーナー不在のテナントを残さない（部分成功を外部に見せない）。取り消しの実装手段（トランザクション or 補償削除）は実装フェーズ
- RegisterTenant が Relation の AddTenantMember を呼び出す際は、受信した API Gateway 発行の内部 JWT を Authorization ヘッダーとしてそのまま転送する。Relation は JWT の subject から作成者を特定し、`tenant.register` をオンボーディング時の所属作成に許可する
