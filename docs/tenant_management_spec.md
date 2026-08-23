# Tenant Management 入出力仕様

package: `tolo.tenant.v1`（テナント）、`tolo.relation.v1`（関係参照）
実現するコンテキスト: tenant_context.md（ドメイン定義 tenant_domain.md）と、auth_context.mdの関係参照（ドメイン定義 auth_domain.md）
役割: テナントとイベントの識別子の正本、および所属（Membership）とロールの真実の源（関係参照）
1つのデプロイ単位が2つの package を実装する。package はコンテキストの公開言語に対応するため統合しない
グラフ編集は `GetEvent` で参照整合を取り、観測は `GetObservationSettings` で設定値を得る

対象外: Auth からの所属・ロール参照 I/F とキャッシュパージ通知（Auth と関係参照の間）

## RPC 一覧

### テナント（TenantService）

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| StartTenantRegistration | テナント（顧客組織）を未認証で仮作成し、所有権取得トークンを一度だけ返す | 管理 UI | 未認証（Service Gatewayの公開経路） | （仮登録。外部イベントなし） |
| ClaimTenantOwnership | 仮テナントの所有権を取得し、認証済みユーザーをオーナーとして登録する | 管理 UI | `tenant.claim`（所有権取得専用の最小トークン。テナント文脈なし。Auth（IdP）） | TenantRegistered |
| ChangeTenantContract | テナントの契約プランを変更する | 管理 UI | `tenant_access` + tenant.write | TenantContractChanged |
| ArchiveTenant | テナントを論理削除（アーカイブ）する。識別子と配下データは保持 | 管理 UI | `tenant_access` + tenant.write | TenantArchived |
| CreateEvent | テナント配下にイベント（催事／設置）を新設する | 管理 UI | `tenant_access` + events.write | EventCreated |
| AssignEventType | イベント種別（短期／長期）を設定する。短期は観測縮退の対象 | 管理 UI | `tenant_access` + events.write | EventTypeAssigned |
| TransitionEventStatus | イベント状態を遷移させる（許容遷移は補足の遷移表） | 管理 UI | `tenant_access` + events.write | EventOpened／EventLocked／EventClosed／EventUnlocked／EventReopened／EventArchived／EventUnarchived |
| GetEvent | イベントの存在と所属テナントを参照する。存在しない ID へのグラフ作成を防ぐ参照整合の要（関係参照の存在確認はサービス内部で行い RPC を経ない） | グラフ編集 | サービス間（token_use=service。テナント文脈必須） | （参照のみ） |
| GetObservationSettings | イベントの観測設定値を参照する | 観測 | サービス間（token_use=service） | （参照のみ） |
| UpdateObservationSettings | イベントの観測設定値を変更する | 管理 UI | `tenant_access` + events.write | ObservationSettingsChanged |
| ListEvents | テナント配下のイベントを一覧する | 管理 UI、スタッフアプリ | `tenant_access` + events.read | （参照のみ） |

### 関係参照（RelationAdminService）

所属（Membership）とロールの真実の源。認証・認可コンテキストの関係参照を本サービスが実装する（package は `tolo.relation.v1` のまま分ける）

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| AddTenantMember | ユーザーをテナントに所属させる（1ユーザーは複数テナントに所属しうる。同一テナントへの重複所属は不可）。ClaimTenantOwnership によるオーナー所属は本サービス内部のローカルトランザクションで行い、本 RPC を経ない | 管理 UI | `tenant_access` + tenant.write | UserJoinedTenant |
| ChangeTenantRole | テナントロールを付与・変更する | 管理 UI | `tenant_access` + tenant.write | TenantRoleChanged |
| GrantEventRole | イベントロールを割り当てる（event∈tenant、event-role⇒tenant-role を検証） | 管理 UI | `tenant_access` + tenant.write | EventRoleGranted |
| RevokeRole | メンバー削除またはロール解除 | 管理 UI | `tenant_access` + tenant.write | RoleRevoked |
| ListMemberships | 所属の一覧参照（管理画面用） | 管理 UI | `tenant_access` + tenant.read | （参照のみ） |

## 参考 proto 定義

```proto
syntax = "proto3";
package tolo.tenant.v1;
import "google/protobuf/timestamp.proto";

service TenantService {
  rpc StartTenantRegistration(StartTenantRegistrationRequest) returns (StartTenantRegistrationResponse);
  rpc ClaimTenantOwnership(ClaimTenantOwnershipRequest) returns (Tenant);
  rpc ChangeTenantContract(ChangeTenantContractRequest) returns (Tenant);
  rpc ArchiveTenant(ArchiveTenantRequest) returns (Tenant);
  rpc CreateEvent(CreateEventRequest) returns (Event);
  rpc AssignEventType(AssignEventTypeRequest) returns (Event);
  rpc TransitionEventStatus(TransitionEventStatusRequest) returns (Event);
  rpc GetEvent(GetEventRequest) returns (Event);
  rpc GetObservationSettings(GetObservationSettingsRequest) returns (ObservationSettings);
  rpc UpdateObservationSettings(UpdateObservationSettingsRequest) returns (ObservationSettings);
  rpc ListEvents(ListEventsRequest) returns (ListEventsResponse);
}

// テナント: 契約・課金・オンボーディングの単位＝顧客組織。改善データの保護境界
message Tenant {
  string tenant_id = 1;      // 公開 ID（ランダムな 16 文字 hex）。内部主キーは外へ出さない
  string name = 2;           // 一意
  string contract_plan = 3;  // 課金詳細はスコープ外のため識別子のみ
  bool archived = 4;
  TenantOwnershipState ownership_state = 5;
}

enum TenantOwnershipState {
  TENANT_OWNERSHIP_STATE_UNSPECIFIED = 0;
  TENANT_OWNERSHIP_STATE_PENDING_OWNER = 1;  // 未認証で仮作成され、業務利用不可
  TENANT_OWNERSHIP_STATE_OWNED = 2;          // オーナー所属が確定し、利用可能
}

// イベント: テナント配下の催事／設置。グラフ・観測・最適化・行列誘導の単位
message Event {
  string event_id = 1;       // 公開 ID（ランダムな 16 文字 hex）
  string tenant_id = 2;      // 必ず1テナントに属す
  string name = 3;
  EventType type = 4;
  EventStatus status = 5;
}

enum EventType {
  EVENT_TYPE_UNSPECIFIED = 0;
  EVENT_TYPE_SHORT_TERM = 1;  // 短期（履歴短い・観測縮退対象）
  EVENT_TYPE_LONG_TERM = 2;   // 長期（常設）
}

enum EventStatus {
  EVENT_STATUS_UNSPECIFIED = 0;
  EVENT_STATUS_DRAFT = 1;
  EVENT_STATUS_OPEN = 2;
  EVENT_STATUS_LOCKED = 3;
  EVENT_STATUS_CLOSED = 4;
  EVENT_STATUS_ARCHIVED = 5;  // 論理削除
}

// 観測が参照する設定値（履歴期間等）。GetObservationSettings でのみ返す
message ObservationSettings {
  int32 history_window_days = 1;  // 履歴パーセンタイル算出に使う期間。既定 30
}

// tenant_id／event_id はいずれも公開 ID。内部主キー（UUIDv7）は proto に現れない
message StartTenantRegistrationRequest {
  string name = 1;
  string contract_plan = 2;  // 必須。欠落は invalid_argument
}
message StartTenantRegistrationResponse {
  Tenant tenant = 1;                    // ownership_state は PENDING_OWNER
  string ownership_claim_token = 2;     // 一回限り。この応答以外では返さない
  google.protobuf.Timestamp expires_at = 3;
}
message ClaimTenantOwnershipRequest {
  string tenant_id = 1;
  string ownership_claim_token = 2;
}
message ChangeTenantContractRequest {
  string tenant_id = 1;
  string contract_plan = 2;
}
message ArchiveTenantRequest {
  string tenant_id = 1;
}
message CreateEventRequest {
  string tenant_id = 1;
  string name = 2;
  EventType type = 3;
}
message AssignEventTypeRequest {
  string event_id = 1;
  EventType type = 2;
}
message TransitionEventStatusRequest {
  string event_id = 1;
  EventStatus to = 2;  // サーバが許容遷移を検証（逆遷移も許容）
}
message GetEventRequest {
  string event_id = 1;
}
message GetObservationSettingsRequest {
  string event_id = 1;
}
message UpdateObservationSettingsRequest {
  string event_id = 1;
  ObservationSettings settings = 2;
}
message ListEventsRequest {
  string tenant_id = 1;
  bool include_archived = 2;  // 既定は false（アーカイブ済みを返さない）
}
message ListEventsResponse {
  repeated Event events = 1;
}
```

関係参照の proto は package を分けて同一サービスが実装する。

```proto
syntax = "proto3";
package tolo.relation.v1;

// tenant_id／event_id はテナントが発番する公開 ID（16 文字 hex）

service RelationAdminService {
  rpc AddTenantMember(AddTenantMemberRequest) returns (Membership);
  rpc ChangeTenantRole(ChangeTenantRoleRequest) returns (Membership);
  rpc GrantEventRole(GrantEventRoleRequest) returns (Membership);
  rpc RevokeRole(RevokeRoleRequest) returns (RevokeRoleResponse);
  rpc ListMemberships(ListMembershipsRequest) returns (ListMembershipsResponse);
}

// 所属: どのテナント／イベントにどのロールで属すか
message Membership {
  string user_id = 1;
  string tenant_id = 2;
  Role tenant_role = 3;
  repeated EventRole event_roles = 4;
}

message EventRole {
  string event_id = 1;  // event∈tenant を検証
  Role role = 2;
}

enum Role {
  ROLE_UNSPECIFIED = 0;
  ROLE_OWNER = 1;
  ROLE_STAFF = 2;
  ROLE_ADMIN = 3;  // 予約。付与できない
}

message AddTenantMemberRequest {
  string tenant_id = 1;
  string user_id = 2;
  Role tenant_role = 3;
}
message ChangeTenantRoleRequest {
  string tenant_id = 1;
  string user_id = 2;
  Role tenant_role = 3;
}
message GrantEventRoleRequest {
  string event_id = 1;
  string user_id = 2;
  Role role = 3;
}
message RevokeRoleRequest {
  string user_id = 1;
  oneof scope {
    string tenant_id = 2;  // テナント所属ごと削除
    string event_id = 3;   // イベントロールのみ解除
  }
}
message RevokeRoleResponse {}

message ListMembershipsRequest {
  oneof filter {
    string tenant_id = 1;
    string user_id = 2;
  }
}
message ListMembershipsResponse {
  repeated Membership memberships = 1;
}
```

## 補足

### 識別子

- テナント／イベントは内部主キーに UUIDv7 を使用するが、これはサービス内部に閉じ、proto には現れない
  外部に出す識別子は公開 ID のみとし、ランダムな 16 文字の hex を使用する
  proto の `tenant_id`／`event_id` はリクエスト・レスポンスとも公開 ID であり、内部 JWT の同名クレーム（internal_jwt.md）と同じ値を指す
  公開 ID は本サービスが発番し、他サービスは参照のみ行う
- 公開 ID は暗号論的乱数 8 バイトを hex エンコードして生成し、レコード作成時に発番する（初回参照時の遅延発番はしない）
  テナントとイベントで別々に一意とし、両者の間で値が重なることは禁じない
  発番済みの値と衝突した場合は再生成せず `already_exists` を返す
- 対象のテナント・イベントはリクエストで受け取り、内部 JWT に対応するクレームがあれば突合する。不一致は `permission_denied` を返す
  クレームから解決する形は採らない。`registration` 起点の呼び出しやマシン起点の `token_use=service` ではクレームが得られず、例外のない規約にできないため

### テナント

- テナント名は `pending_owner` を含めて一意とし、同名のテナントを仮作成しようとした場合は `already_exists` を返す
  一意性は保存された文字列そのままで判定する。大文字小文字は区別し、前後・連続する空白の除去も Unicode 正規化も行わない
  期限切れの `pending_owner` を物理削除した時点で、その名前を解放する
  アーカイブは名前を解放しない。アーカイブ済みテナントの名前を新規テナントが使うことはできない
- `pending_owner` は、ClaimTenantOwnership と期限切れ削除以外の操作を受け付けない
- `owned` への遷移後は、所有状態を `pending_owner` へ戻さない
- アーカイブは論理削除であり `GetEvent`／`GetObservationSettings` は archived でも返す（宙づり参照を生まない）
- アーカイブ済みテナント／イベントの扱い: 書き込み系 RPC は `failed_precondition`、参照系は継続
  既存所属は保持し（補足「関係参照」）、archived イベントへの Token Exchange は拒否（Auth（IdP））
- テナントのアーカイブは配下イベントの状態を変えない
  配下イベントは状態を据え置いたまま書き込みのみ遮断され、`GetEvent`／`ListEvents` は引き続き返す
  復元はテナントのアーカイブ状態を戻すだけでよく、配下イベントの状態を復元する処理を要さない

### イベント

- CreateEvent は `owned` かつアーカイブされていないテナントにのみ作成し、初期状態は `draft` とする
  存在しないテナントは `not_found`、`pending_owner`またはアーカイブ済みのテナントは `failed_precondition` を返す
- CreateEvent の `type` は省略できる。省略した場合は `EVENT_TYPE_UNSPECIFIED` のまま作成し、短期／長期のいずれにも丸めない
  AssignEventType で `EVENT_TYPE_UNSPECIFIED` を指定することはできず `invalid_argument` を返す
- AssignEventType は archived 以外のすべての状態で種別を変更できる。イベントが archived の場合のみ `failed_precondition` を返す
  公開後（open 以降）の変更も許容する。既存の観測スナップショットは保持し、以後の算出方法（参照値で補うか自前の履歴を使うか）だけが切り替わる
  観測データは種別に依存しない生データのため、変更しても過去の値との矛盾は生じない
- 許容する状態遷移は次の8つに限り、これ以外は `failed_precondition` を返す
  自状態への遷移（open→open 等）も許容しない

  | from → to | draft | open | locked | closed | archived |
  |---|---|---|---|---|---|
  | draft | — | ○ | × | × | ○ |
  | open | × | — | ○ | × | × |
  | locked | × | ○ | — | ○ | × |
  | closed | × | ○ | × | — | ○ |
  | archived | × | × | × | ○ | — |

  各遷移は関連ドメインイベントに対応する（draft→open＝EventOpened、open→locked＝EventLocked、locked→open＝EventUnlocked、locked→closed＝EventClosed、closed→open＝EventReopened、draft→archived と closed→archived＝EventArchived、archived→closed＝EventUnarchived）
  `draft→archived` は作成した draft イベントを破棄する経路である。復元は `archived→closed` のみで、draft へは戻らない
- ListEvents はテナント配下のイベントを作成順に返す。返却件数の上限は 1000 件とし、ページングは設けない
  アーカイブ済みイベントは既定で返さず、`include_archived` の指定時のみ含める

### オンボーディング

- ロール種別の定義はテナントコンテキストが所有し、所属の読み書きは関係参照（RelationAdminService）が所有する。どちらも本サービスが実装する
- StartTenantRegistration は未認証で呼び出し、`pending_owner` のテナントを作成する
  `contract_plan` は必須とし、欠落は `invalid_argument` を返す
  この時点では所属を作らず、TenantRegistered も成立させない
  所有権取得トークンは暗号論的に推測困難な一回限りの値とし、平文を応答で一度だけ返す
  永続化層にはトークンのハッシュだけを保存し、ログ、監査ログ、エラー本文へ平文を出さない
  Service Gateway は匿名作成にレート制限とボット対策を適用する。具体値、取得トークンのTTL、期限切れ削除の実行方式は実装フェーズで確定する
- ClaimTenantOwnership は、IdPが認証済みユーザーへ発行した`registration`トークンと、StartTenantRegistrationが返した所有権取得トークンの両方を要求する
  外部トークンのscopeは`tenant.claim`とし、Service Gatewayが主体を維持した内部JWTへ変換する
  Tenant Managementは対象が期限内の`pending_owner`であることと、所有権取得トークンのハッシュが一致することを検証する
  認証済みユーザーのオーナー所属作成、`owned`への遷移、所有権取得トークンの消費を同一DBのローカルトランザクションで確定する
  TenantRegisteredはこのトランザクションの成功時に成立する
  1ユーザーは複数テナントに所属しうるため、取得者が既に他テナントへ所属していても所有権を取得できる
- オンボーディング全体は「匿名の仮テナント作成 → Authの`POST /api/signup`でアカウント作成 → テナント未指定の認可で所有権取得専用トークンを取得 → 所有権取得とオーナー所属の確定 → tenantId指定の再認可で`tenant_access`取得」の流れ（Auth（IdP）補足）
  課金・プロビジョニング導入時に再設計の余地を残す

### 関係参照

- `ROLE_ADMIN` は enum の予約値であり、付与しようとした場合は `invalid_argument` を返す。実ロール化は将来判断
- relation model 制約の検証点: 同一テナントへの重複所属を作らない（AddTenantMember）、event∈tenant と event-role⇒tenant-role（GrantEventRole）
  違反は `failed_precondition`
  1ユーザーが所属できるテナント数に上限は置かない
- イベントの存在確認は同一サービス内のイベントレコード参照で行い、存在しない ID へ所属を作らない（RPC を経ない）
- 所属変更時の Auth 側キャッシュパージ（RelationCachePurged）は Auth と関係参照の間の内部 I/F のため対象外
- アーカイブ済みテナント・イベントへの新規所属・ロール変更は `failed_precondition`、既存所属はそのまま保持する
  復元（EventUnarchived／テナント復元）時に再割り当ては不要

### 管理系書き込みの現在権限確認

- scope はトークン発行時点の権限スナップショットであり、通常 RPC は外部トークンの TTL までその値を許容する
- `ArchiveTenant`、`ChangeTenantContract`、`AddTenantMember`、`ChangeTenantRole`、`GrantEventRole`、`RevokeRole` は例外とし、内部 JWT の scope 検証に加えて、呼び出しユーザーの現在の membership と permission を同一 DB から再確認する
- 現在権限の確認と対象の書き込みは同じローカルトランザクションで行い、確認後の権限変更との競合で許可判定が古くならないようにする
- 現在の membership が存在しない場合、または現在のロールから要求 scope を発行できない場合は `permission_denied` を返す
- Service Gateway の introspection はトークンの active／revoked だけを確認する。現在権限の確認を introspection の結果で代替しない
- 上記6 RPC以外は現在権限を都度参照せず、既発行 scope の失効反映を外部トークンの TTL に委ねる


### エラー

| 事象 | Connect エラーコード |
|---|---|
| テナント名の重複、公開 ID の衝突 | `already_exists` |
| 所有権取得トークンが不正、期限切れ、使用済み | `unauthenticated` |
| relation model 制約違反（同一テナントへの重複所属、event∈tenant・event-role⇒tenant-role の違反）、`ROLE_ADMIN` 以外の予約違反はそれぞれ上記「関係参照」のとおり | `failed_precondition`（`ROLE_ADMIN` の付与は `invalid_argument`） |
| 存在しないテナント／イベント | `not_found` |
| `pending_owner`またはアーカイブ済みテナント／イベントへの許可外の書き込み、許容外の状態遷移 | `failed_precondition` |
| 匿名の仮作成がレート制限を超過 | `resource_exhausted` |
| 必須項目の欠落、`EVENT_TYPE_UNSPECIFIED` の指定 | `invalid_argument` |
| 内部 JWT が無効、`token_use` 不一致 | `unauthenticated` |
| scope 不足、認証テナント以外のイベントの指定 | `permission_denied` |

- エラーメッセージに内部主キー・テナント名・ユーザー ID を含めない（service_gateway.md のエラー方針）
- 存在しない識別子と権限のない識別子は区別して返す。公開 ID は 64 ビットのランダムで列挙できず、推測可能な値で引ける RPC もないため、存在の推定は許容する

### 参照系 RPC とテナント境界

サービス間の参照系 RPC は2つあり、テナント境界の強制の扱いが異なる。

- `GetEvent` は境界を強制する。内部 JWT の `tenant_id` クレームと対象イベントの所属テナントが一致しない場合は `permission_denied` を返し、クレームを持たない内部 JWT（マシン起点の `token_use=service`）では `unauthenticated` を返す
  呼び出し元はグラフ編集で、管理・設計 UI 起点のためテナント文脈を必ず持つ
- `GetObservationSettings` は境界を強制しない。テナント文脈のない経路（QR 由来の計上を契機とする観測サイクル等）から呼ばれうるため、クレームがない内部 JWT も受け付ける
  境界を強制しない代わりに、応答は観測設定値のみとし、イベント名・状態・所属テナントを含めない

この分担は、参照整合と設定供給で呼び出し元の資格情報の性質が違うことに基づく。応答の内容も、境界を強制できない側が機微を持たないように分けている。

### その他

- Realtime 配信ログの保持期間などテナント設定値の追加項目は実装フェーズで決定（Realtime）
- `observation_settings` の既定は `history_window_days = 30` とする。実測にもとづき運用で調整する前提の初期値である
  観測設定値として持つのは `history_window_days` のみとし、他の項目は必要が生じた時点で追加する
