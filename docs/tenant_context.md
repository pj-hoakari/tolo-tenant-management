# テナントコンテキスト 入出力整理

ドメイン定義: tenant_domain.md
デプロイ単位: tenant_management_spec.md

## ドメインイベント↔RPC 対応

| ドメインイベント | 対応 RPC |
|---|---|
| TenantRegistered | TenantService.ClaimTenantOwnership |
| TenantContractChanged | TenantService.ChangeTenantContract |
| TenantArchived | TenantService.ArchiveTenant |
| EventCreated | TenantService.CreateEvent |
| EventTypeAssigned | TenantService.AssignEventType |
| EventOpened／EventLocked／EventClosed／EventUnlocked／EventReopened／EventArchived／EventUnarchived | TenantService.TransitionEventStatus |

## 他コンテキストとの接点

| 接点 | RPC |
|---|---|
| 識別子の参照整合（グラフ編集） | TenantService.GetEvent |
| 観測への設定値・履歴期間の供給 | TenantService.GetObservationSettings |
| 所属の書き込み（関係参照が所有） | RelationAdminService（tenant_management_spec.md が実装） |
