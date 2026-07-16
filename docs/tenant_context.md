# テナントコンテキスト 入出力整理

## ドメインイベント↔RPC 対応

| ドメインイベント | 対応 RPC |
|---|---|
| TenantRegistered | TenantService.RegisterTenant |
| TenantContractChanged | TenantService.ChangeTenantContract |
| TenantArchived | TenantService.ArchiveTenant |
| EventCreated | TenantService.CreateEvent |
| EventTypeAssigned | TenantService.AssignEventType |
| EventOpened／EventLocked／EventClosed／EventUnlocked／EventReopened／EventArchived／EventUnarchived | TenantService.TransitionEventStatus |

## 他コンテキストとの接点

| 接点 | RPC |
|---|---|
| 識別子の参照整合（Relation、グラフ編集、観測） | TenantService.GetEvent |
| 観測への設定値・履歴期間の供給 | GetEvent 応答の `observation_settings` |
| 所属の書き込み（関係参照が所有） | Relation サービス側 |
