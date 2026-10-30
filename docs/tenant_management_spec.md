# Tenant Management 入出力仕様

package: `tolo.tenant.v1`（テナント）、`tolo.relation.v1`（関係参照）
実現するコンテキスト: tenant_context.md（ドメイン定義 tenant_domain.md）と、auth_context.mdの関係参照（ドメイン定義 auth_domain.md）
役割: テナントとイベントの識別子の所有、および所属（Membership）とロールの真実の源（関係参照）
1つのデプロイ単位が2つの package を実装する。package はコンテキストの公開言語に対応するため統合しない。
グラフ編集は `GetEvent` で参照整合を取る。

対象外: Auth からの所属・ロール参照 I/F とキャッシュパージ通知（Auth と関係参照の間）

## RPC 一覧

### テナント（TenantService）

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| StartTenantRegistration | テナント（顧客組織）を未認証で仮作成し、所有権取得トークンを一度だけ返す | 管理 UI | 未認証（Service Gatewayの公開経路） | （仮登録。外部イベントなし） |
| ClaimTenantOwnership | 仮テナントの所有権を取得し、認証済みユーザーをオーナーとして登録する。テナントのサブドメイン名とオーナーの表示名を確定する | 管理 UI | `registration` + tenant.join（テナント文脈を持たない最小トークン。Auth（IdP）） | TenantRegistered |
| ResolveTenantSubdomain | サブドメイン名からテナントの公開 ID を引く。Web がホスト名からテナントを決めるために使う | 管理 UI、ゲスト（いずれも BFF のホスト名の解決） | 未認証（Service Gatewayの公開経路） | （参照のみ） |
| ChangeTenantContract | テナントの契約プランを変更する | 管理 UI | `tenant_access` + tenant.write | TenantContractChanged |
| ArchiveTenant | テナントを論理削除（アーカイブ）する。識別子と配下データは保持 | 管理 UI | `tenant_access` + tenant.write | TenantArchived |
| CreateEvent | テナント配下にイベント（催事／設置）を新設する | 管理 UI | `tenant_access` + events.manage | EventCreated |
| TransitionEventStatus | イベント状態を遷移させる（許容遷移は補足の遷移表） | 管理 UI | `tenant_access` + events.manage | EventOpened／EventLocked／EventClosed／EventUnlocked／EventReopened／EventArchived／EventUnarchived |
| GetEvent | イベントの存在と所属テナントを参照する。存在しない ID へのグラフ作成を防ぐ参照整合の要（関係参照の存在確認はサービス内部で行い RPC を経ない） | グラフ編集 | サービス間（token_use=service。テナント文脈必須） | （参照のみ） |
| ListEvents | テナント配下のイベントを一覧する | 管理 UI、スタッフアプリ | `tenant_access` + events.read | （参照のみ） |

### 関係参照（RelationAdminService）

所属（Membership）とロールの真実の源。認証・認可コンテキストの関係参照を本サービスが実装する（package は `tolo.relation.v1` のまま分ける）。

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| CreateTenantInvitation | テナント、テナントロール（スタッフに限る）、表示名を固定した招待を作り、一回限りの招待トークンを一度だけ返す | 管理 UI | `tenant_access` + tenant.write | TenantInvitationCreated |
| ListTenantInvitations | 受諾も取り消しもされておらず期限内の招待を一覧する。招待トークンは返さない | 管理 UI | `tenant_access` + tenant.write | （参照のみ） |
| RevokeTenantInvitation | 招待を取り消す | 管理 UI | `tenant_access` + tenant.write | TenantInvitationRevoked |
| AcceptTenantInvitation | 招待を受諾し、認証済みユーザーを招待で固定したテナントロールと表示名で所属させる | 管理 UI | `registration` + tenant.join（テナント文脈を持たない最小トークン。Auth（IdP）） | UserJoinedTenant |
| AddTenantMember | ユーザーをテナントに所属させる（1ユーザーは複数テナントに所属しうる。同一テナントへの重複所属は不可）。管理 UI から人を加える経路は招待とし、本 RPC はサービス間に限る。ClaimTenantOwnership と AcceptTenantInvitation による所属は本サービス内部のローカルトランザクションで行い、本 RPC を経ない | サービス間 | サービス間（token_use=service。テナント文脈必須） | UserJoinedTenant |
| ChangeTenantRole | テナントロールを付与・変更する。オーナーへの昇格は本 RPC だけで行う。最後のオーナーの変更は拒否する | 管理 UI | `tenant_access` + tenant.write | TenantRoleChanged |
| GrantEventRole | イベントロールを割り当てる（event∈tenant、event-role⇒tenant-role を検証） | 管理 UI | `tenant_access` + tenant.write | EventRoleGranted |
| RevokeRole | メンバー削除またはロール解除。最後のオーナーのテナント所属の削除は拒否する | 管理 UI | `tenant_access` + tenant.write | RoleRevoked |
| ListMemberships | 所属の一覧参照（管理画面用） | 管理 UI | `tenant_access` + tenant.read | （参照のみ） |

## 参考 proto 定義

```proto
syntax = "proto3";
package tolo.tenant.v1;
import "google/protobuf/timestamp.proto";

service TenantService {
  rpc StartTenantRegistration(StartTenantRegistrationRequest) returns (StartTenantRegistrationResponse);
  rpc ClaimTenantOwnership(ClaimTenantOwnershipRequest) returns (Tenant);
  rpc ResolveTenantSubdomain(ResolveTenantSubdomainRequest) returns (ResolveTenantSubdomainResponse);
  rpc ChangeTenantContract(ChangeTenantContractRequest) returns (Tenant);
  rpc ArchiveTenant(ArchiveTenantRequest) returns (Tenant);
  rpc CreateEvent(CreateEventRequest) returns (Event);
  rpc TransitionEventStatus(TransitionEventStatusRequest) returns (Event);
  rpc GetEvent(GetEventRequest) returns (Event);
  rpc ListEvents(ListEventsRequest) returns (ListEventsResponse);
}

// テナント: 契約・課金・オンボーディングの単位＝顧客組織。改善データの保護境界
message Tenant {
  string tenant_id = 1;      // 公開 ID（ランダムな 16 文字 hex）。内部主キーは外へ出さない
  string name = 2;           // 表示用。重複を許す
  string contract_plan = 3;  // 課金詳細はスコープ外のため識別子のみ
  bool archived = 4;
  TenantOwnershipState ownership_state = 5;
  string subdomain = 6;      // サブドメイン名。所有権取得で確定し、変更しない。PENDING_OWNER の間は空
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
  EventStatus status = 5;
  reserved 4;
  reserved "type";
}

enum EventStatus {
  EVENT_STATUS_UNSPECIFIED = 0;
  EVENT_STATUS_DRAFT = 1;
  EVENT_STATUS_OPEN = 2;
  EVENT_STATUS_LOCKED = 3;
  EVENT_STATUS_CLOSED = 4;
  EVENT_STATUS_ARCHIVED = 5;  // 論理削除
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
  string subdomain = 3;      // 必須。規則は補足「サブドメイン名」
  string display_name = 4;   // 必須。オーナーの所属の表示名。規則は補足「表示名」
}
message ResolveTenantSubdomainRequest {
  string subdomain = 1;
}
message ResolveTenantSubdomainResponse {
  string tenant_id = 1;
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
  reserved 3;
  reserved "type";
}
message TransitionEventStatusRequest {
  string event_id = 1;
  EventStatus to = 2;  // サーバが許容遷移を検証（逆遷移も許容）
}
message GetEventRequest {
  string event_id = 1;
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
import "google/protobuf/timestamp.proto";

// tenant_id／event_id はテナントが発番する公開 ID（16 文字 hex）

service RelationAdminService {
  rpc CreateTenantInvitation(CreateTenantInvitationRequest) returns (CreateTenantInvitationResponse);
  rpc ListTenantInvitations(ListTenantInvitationsRequest) returns (ListTenantInvitationsResponse);
  rpc RevokeTenantInvitation(RevokeTenantInvitationRequest) returns (TenantInvitation);
  rpc AcceptTenantInvitation(AcceptTenantInvitationRequest) returns (Membership);
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
  string display_name = 5;  // テナントの中だけで使う表示名。規則は補足「表示名」
  google.protobuf.Timestamp joined_at = 6;  // 所属が確定した時刻（招待の受諾、または所有権取得）
}

// 招待: テナント、テナントロール、表示名を固定し、一回限りの招待トークンで受諾させる
message TenantInvitation {
  string invitation_id = 1;
  string tenant_id = 2;
  Role tenant_role = 3;     // ROLE_STAFF だけ
  string display_name = 4;
  google.protobuf.Timestamp expires_at = 5;
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

message CreateTenantInvitationRequest {
  string tenant_id = 1;
  Role tenant_role = 2;
  string display_name = 3;
}
message CreateTenantInvitationResponse {
  TenantInvitation invitation = 1;
  string invitation_token = 2;  // 一回限り。この応答以外では返さない
}
message ListTenantInvitationsRequest {
  string tenant_id = 1;
}
message ListTenantInvitationsResponse {
  repeated TenantInvitation invitations = 1;
}
message RevokeTenantInvitationRequest {
  string tenant_id = 1;
  string invitation_id = 2;
}
message AcceptTenantInvitationRequest {
  string tenant_id = 1;
  string invitation_token = 2;
}

message AddTenantMemberRequest {
  string tenant_id = 1;
  string user_id = 2;
  Role tenant_role = 3;
  string display_name = 4;  // 必須。規則は補足「表示名」
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

- テナント名は表示用であり、重複を許す
- `pending_owner` は、ClaimTenantOwnership と期限切れ削除以外の操作を受け付けない
- `owned` への遷移後は、所有状態を `pending_owner` へ戻さない
- アーカイブは論理削除であり `GetEvent` は archived でも返す（宙づり参照を生まない）
- アーカイブ済みテナント／イベントの扱い: 書き込み系 RPC は `failed_precondition`、参照系は継続
  既存所属は保持し（補足「関係参照」）、archived イベントへの Token Exchange は拒否（Auth（IdP））
- テナントのアーカイブは配下イベントの状態を変えない
  配下イベントは状態を据え置いたまま書き込みのみ遮断され、`GetEvent`／`ListEvents` は引き続き返す
  復元はテナントのアーカイブ状態を戻すだけでよく、配下イベントの状態を復元する処理を要さない

### サブドメイン名

- サブドメイン名は、Web がホスト名からテナントを決めるための識別子であり、テナント名とは別に持つ（ADR-0104）
- 英小文字・数字・ハイフンからなり、63 文字以内で、先頭と末尾はハイフン以外とする
- 次の名前は拒否する
  - 16 文字の hex（`^[0-9a-f]{16}$`）
  - 3 文字目と 4 文字目がともにハイフンの名前（`xn--` を含む）
  - 予約語の `www`、`api`、`admin`、`auth`（`auth` は IdP のホスト。ADR-0119）
  - `s` の後に数字だけが続く名前（`^s[0-9]+$`）
- 規則に合わない名前は `invalid_argument` を返す
- ClaimTenantOwnership で確定する。`pending_owner` のテナントはサブドメイン名を持たない
- 一意性はアーカイブ済みのテナントを含めて判定し、既に使われている名前は `already_exists` を返す
- 変更できない。`owned` のテナントは物理削除しないので、一度使った名前はほかのテナントに使わせない
- ResolveTenantSubdomain は、`owned` のテナントのサブドメイン名に対して公開 ID を返す。アーカイブ済みのテナントも返す。見つからない名前は `not_found` を返す
  応答は公開 ID だけとし、テナント名、所有状態、アーカイブの有無を返さない

### イベント

- CreateEvent は `owned` かつアーカイブされていないテナントにのみ作成し、初期状態は `draft` とする
  存在しないテナントは `not_found`、`pending_owner`またはアーカイブ済みのテナントは `failed_precondition` を返す
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
  外部トークンのscopeは`tenant.join`とし、Service Gatewayが主体を維持した内部JWTへ変換する
  Tenant Managementは対象が期限内の`pending_owner`であることと、所有権取得トークンのハッシュが一致することを検証する
  認証済みユーザーのオーナー所属作成（要求の表示名を持たせる）、`owned`への遷移、サブドメイン名の確定、所有権取得トークンの消費を同一DBのローカルトランザクションで確定する
  サブドメイン名が使われていて `already_exists` を返す場合は、所有権取得トークンを消費しない。呼び出し側は別の名前で再試行できる
  TenantRegisteredはこのトランザクションの成功時に成立する
  1ユーザーは複数テナントに所属しうるため、取得者が既に他テナントへ所属していても所有権を取得できる
- オンボーディング全体は「匿名の仮テナント作成 → Authの`POST /api/signup`でアカウント作成 → テナント未指定の認可で`registration`トークンを取得 → 所有権取得、オーナー所属、サブドメイン名とオーナーの表示名の確定 → tenantId指定の再認可で`tenant_access`取得」の流れとする（Auth（IdP）補足）
  課金・プロビジョニング導入時に再設計の余地を残す

### 招待

- 管理 UI から人をテナントに加える経路は招待とする（ADR-0118）。AddTenantMember はサービス間に限る
- CreateTenantInvitation は、`owned` かつアーカイブされていないテナントに、テナントロールと表示名を固定した招待を作る
  テナントロールは `ROLE_STAFF` だけとし、`ROLE_OWNER` と `ROLE_ADMIN` は `invalid_argument` を返す（ADR-0120）
  オーナーは、スタッフとして所属した後に ChangeTenantRole で昇格させる
  招待トークンは暗号論的に推測困難な一回限りの値とし、平文を応答で一度だけ返す
  永続化層にはトークンのハッシュだけを保存し、ログ、監査ログ、エラー本文へ平文を出さない
  招待トークンの有効期限は作成から 7 日とする
- ListTenantInvitations は、受諾も取り消しもされておらず期限内の招待を返し、招待トークンは返さない
- RevokeTenantInvitation で取り消した招待は受諾できない。受諾済み、取り消し済み、期限切れの招待の取り消しは `failed_precondition` を返す
- AcceptTenantInvitation は、IdP が認証済みユーザーへ発行した `registration` トークンと、CreateTenantInvitation が返した招待トークンの両方を要求する
  外部トークンのscopeは`tenant.join`とし、Service Gatewayが主体を維持した内部JWTへ変換する
  Tenant Management は、招待トークンのハッシュが一致し、招待が期限内で、受諾も取り消しもされていないことを検証する。対象のテナントは `owned` かつアーカイブされていないことを要する
  認証済みユーザーの所属作成（招待で固定したテナントロールと表示名を持たせる）と招待トークンの消費を同一DBのローカルトランザクションで確定する
  UserJoinedTenant はこのトランザクションの成功時に成立する
  受諾した時点で所属を確定し、オーナーの承認の段階は設けない。所属には受諾の時刻を持たせる
  認証済みユーザーがすでにそのテナントに所属している場合は `failed_precondition` を返し、招待トークンを消費しない
- 招待の受諾は「招待 URL を開く → サインアップまたはログイン → テナント未指定の認可で`registration`トークンを取得 → 招待の受諾と所属の確定 → tenantId指定の再認可で`tenant_access`取得」の流れとする（Auth（IdP）補足）
- 使用済みの招待トークンで受諾しようとした人には、Web がオーナーへ連絡するよう案内する
- 招待 URL はテナントの公開 ID と招待トークンから管理 UI が作る。Web は招待 URL で受け取った招待トークンをすぐにサーバ側のセッションへ移し、招待トークンを含まない URL へリダイレクトする。招待トークンを含むページには `Referrer-Policy: no-referrer` を付け、URL をログに残さない

### 表示名

- 所属は表示名を持つ。表示名はテナントの中だけで使う名前であり、IdP の UserInfo の profile は使わない（ADR-0118）
- 招待による所属は招待で固定した表示名を、所有権取得によるオーナーの所属は ClaimTenantOwnership の要求の表示名を、AddTenantMember による所属は要求の表示名を持つ
- 表示名は前後の空白を除いて扱う。空を許さず、一意である必要はない
- 表示名は 64 文字までとする。超える表示名は `invalid_argument` を返す
- 表示名を変える手段は実装フェーズで確定する
- 表示名だけでは受諾した人が本人かを確かめられないので、オーナーは昇格の前に本人へ別の経路で確かめる。受諾の時刻（`joined_at`）を手がかりにする

### 関係参照

- `ROLE_ADMIN` は enum の予約値であり、付与しようとした場合は `invalid_argument` を返す。実ロールにするかは将来判断する
- `owned` のテナントには常に1人以上のオーナーがいる（ADR-0120）
  ChangeTenantRole で最後のオーナーのテナントロールを変える要求と、RevokeRole で最後のオーナーのテナント所属を削除する要求は `failed_precondition` を返す
  残るオーナーの数は現在権限の確認と同じローカルトランザクションで確かめ、並行する変更が重なってもオーナーが0人にならないようにする
- relation model 制約の検証点: 同一テナントへの重複所属を作らない（AddTenantMember、AcceptTenantInvitation）、event∈tenant と event-role⇒tenant-role（GrantEventRole）
  違反は `failed_precondition` を返す
  1ユーザーが所属できるテナント数に上限は置かない
- イベントの存在確認は同一サービス内のイベントレコード参照で行い、存在しない ID へ所属を作らない（RPC を経ない）
- 所属変更時の Auth 側キャッシュパージ（RelationCachePurged）は Auth と関係参照の間の内部 I/F のため対象外
- アーカイブ済みテナント・イベントへの新規所属・ロール変更は `failed_precondition` を返し、既存所属はそのまま保持する
  復元（EventUnarchived／テナント復元）時に再割り当ては不要

### 管理系書き込みの現在権限確認

- scope はトークン発行時点の権限スナップショットであり、通常 RPC は外部トークンの TTL までその値を許容する
- `ArchiveTenant`、`ChangeTenantContract`、`CreateTenantInvitation`、`ChangeTenantRole`、`GrantEventRole`、`RevokeRole` は例外とし、内部 JWT の scope 検証に加えて、呼び出しユーザーの現在の membership と permission を同一 DB から再確認する
- 現在権限の確認と対象の書き込みは同じローカルトランザクションで行い、確認後の権限変更との競合で許可判定が古くならないようにする
- 現在の membership が存在しない場合、または現在のロールから要求 scope を発行できない場合は `permission_denied` を返す
- Service Gateway の introspection はトークンの active／revoked だけを確認する。現在権限の確認を introspection の結果で代替しない
- 上記6 RPC以外は現在権限を都度参照せず、既発行 scope の失効反映を外部トークンの TTL に委ねる


### エラー

| 事象 | Connect エラーコード |
|---|---|
| サブドメイン名の重複、公開 ID の衝突 | `already_exists` |
| 所有権取得トークンまたは招待トークンが不正、期限切れ、使用済み、取り消し済み | `unauthenticated` |
| relation model 制約違反（同一テナントへの重複所属、event∈tenant・event-role⇒tenant-role の違反）、最後のオーナーの降格・削除、`ROLE_ADMIN` 以外の予約違反はそれぞれ上記「関係参照」のとおり | `failed_precondition`（`ROLE_ADMIN` の付与は `invalid_argument`） |
| 存在しないテナント／イベント | `not_found` |
| `pending_owner`またはアーカイブ済みテナント／イベントへの許可外の書き込み、許容外の状態遷移 | `failed_precondition` |
| 匿名の仮作成がレート制限を超過 | `resource_exhausted` |
| 必須項目の欠落、サブドメイン名または表示名が規則に合わない | `invalid_argument` |
| 内部 JWT が無効、`token_use` 不一致 | `unauthenticated` |
| 内部 JWT の検証鍵を解決できない（JWKS の取得失敗、その再試行を控えている間） | `unavailable` |
| scope 不足、認証テナント以外のイベントの指定 | `permission_denied` |

- エラーメッセージに内部主キー・テナント名・ユーザー ID を含めない（service_gateway.md のエラー方針）
- 存在しない識別子と権限のない識別子は区別して返す。公開 ID は 64 ビットのランダムで列挙できないため、存在の推定は許容する
  サブドメイン名は推測できるため、ResolveTenantSubdomain によってテナントの有無は誰でも確かめられる。名前で開ける仕組みである以上これを受け入れ、応答を公開 ID だけに限る（ADR-0104）

### 参照系 RPC とテナント境界

サービス間の参照系 RPC は `GetEvent` だけであり、テナント境界を強制する。

- 内部 JWT の `tenant_id` クレームと対象イベントの所属テナントが一致しない場合は `permission_denied` を返し、クレームを持たない内部 JWT（マシン起点の `token_use=service`）では `unauthenticated` を返す
- 呼び出し元はグラフ編集で、管理 UI 起点のためテナント文脈を必ず持つ

### その他

- Realtime 配信ログの保持期間などテナント設定値の追加項目は実装フェーズで決定する（Realtime）
