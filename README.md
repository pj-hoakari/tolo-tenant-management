# tolo-tenant-management

## 開発

### セットアップ
miseを使用して開発環境をセットアップ

```bash
mise trust
mise install
task proto
```

### PostgreSQL を使った開発サーバー

Docker Compose で PostgreSQL、golang-migrate によるマイグレーション、および開発サーバーを起動できる。

```bash
docker compose up --build
```

もしくは
```bash
task up:build
```

サーバーは `http://localhost:8080`、PostgreSQL は `localhost:5432` で待ち受ける。  
アプリケーションは `DATABASE_URL` で接続先を設定する。  
ローカルでマイグレーションを実行する場合は、Compose で PostgreSQL を起動してから次を実行する（接続先は `DATABASE_URL` で上書きできる）。

```bash
# マイグレーションを適用
task migrate:up
# 新しいマイグレーションを作成（up/down のペアを生成）
task migrate:create -- <migration_name>
# 1 つ前にロールバック
task migrate:down
```

### トレースの確認（Jaeger）

監視スタックはオーバーライドファイル `compose.o11y.yml` を重ねたときだけ有効になる。
Jaeger が起動し、`server` に OTLP エクスポート用の環境変数（`OTEL_EXPORTER_OTLP_ENDPOINT` など）がセットされる。

```bash
docker compose -f compose.yml -f compose.o11y.yml up --build
```

もしくは
```bash
task up:build:o11y
```

Jaeger UI は `http://localhost:16686` 。

### connect-es の生成
connect-es の生成（`task proto:gen:es`）はリリース時に CI で行う  
ローカルで実行する場合は `clients/connect-es` の依存（`npm i`）を導入する必要がある

### TenantService の認可

`TenantService` は proto の policy annotation（`authz.v1.auth_policy`）で RPC ごとの公開面と要求 scope を宣言し、生成された authz interceptor が検証する。

| RPC | 公開面 | 要求する `token_use` | 要求 scope |
|---|---|---|---|
| StartTenantRegistration | 未認証（`AUTH_LEVEL_PUBLIC`） | なし | なし |
| ClaimTenantOwnership | 認証済み | `registration` | `tenant.claim` |
| ChangeTenantContract、ArchiveTenant | 認証済み | `tenant_access` | `tenant.write` |
| CreateEvent、AssignEventType、TransitionEventStatus、UpdateObservationSettings | 認証済み | `tenant_access` | `events.write` |
| ListEvents | 認証済み | `tenant_access` | `events.read` |
| GetEvent、GetObservationSettings | 内部（`AUTH_LEVEL_INTERNAL`） | `service` | なし |


Service Gateway が発行した内部 JWT を Authorization ヘッダーで受け付ける。サービス側では ES256 の署名、`kid`、`iss`、`aud`、`exp`／`nbf`、`token_use`、scope を検証する。
`internal/jwks` の JWKS validator を、`internal/infra/connect` の認可 verifier とテナント ID interceptor で共有する。

```text
Authorization: Bearer <Service Gateway 発行の内部 JWT>
```

#### 受理するクレーム

クレーム構造の正本は `docs/internal_jwt.md`。すべての内部 JWT に `sub`、`client_id`、`jti`、`iat`／`nbf`／`exp`、`token_use`、`txn` を要求し、`token_use` ごとに次を追加で要求する。
`txn` と `origin_sub` は監査・トレース用で、認可判定には使わない。

| `token_use` | 追加で要求するクレーム |
|---|---|
| `tenant_access` | `scope`、`src_jti`、`tenant_id` |
| `event_access` | `scope`、`src_jti`、`tenant_id`、`event_id` |
| `registration` | `scope`、`src_jti`（`tenant_id`／`event_id` を持たない） |
| `service`（ユーザー起点） | `scope`、`src_jti`、`origin_sub`（`tenant_id`／`event_id` は文脈トークンから引き写された場合のみ） |
| `service`（マシン起点） | なし（`scope`、`src_jti`、`origin_sub`、`tenant_id`、`event_id` のいずれも持たない） |

#### 識別子とテナントの突合

proto の `tenant_id`／`event_id` はいずれも公開 ID（ランダムな 16 文字 hex）で、内部主キー（UUIDv7）は外に出ない。
`tenant_access` の内部 JWT には `tenant_id` クレームが必須で、その値も同じ公開 ID である。

```text
{"tenant_id":"<tenant public ID: 16-character hex>"}
```

テナント配下を対象にする RPC（`CreateEvent`、`ListEvents` など）はリクエストの `tenant_id` で対象を指定し、サービスはクレームの `tenant_id` と突合する。
不一致は `permission_denied`、`tenant_id` の欠落は `invalid_argument` を返す。
イベントを対象にする RPC（`AssignEventType`、`TransitionEventStatus`）は、読み込んだイベントの所属テナントをクレームと突合する。

サービス間の参照系 RPC は境界の扱いが異なる。
`GetEvent` はテナント文脈を持つ `service` トークン（ユーザー起点。`tenant_id` クレームあり）を要求し、クレームがなければ `unauthenticated`、イベントの所属テナントと不一致なら `permission_denied` を返す。
`GetObservationSettings` はテナント文脈のない `service` トークン（マシン起点）も受け付け、クレームがある場合だけ突合する。

JWKS は `INTERNAL_JWKS_URL` から取得する。未設定時は Gateway コンテナのエンドポイント `http://gateway:8080/.well-known/jwks.json` を使う。取得した鍵は 5 分間キャッシュし、未知の `kid` を受信した場合は直ちに再取得する。
`iss`／`aud` の期待値は `INTERNAL_JWT_ISSUER`（既定 `service-gateway`。Service Gateway の発行者識別子に合わせる）と `INTERNAL_JWT_AUDIENCE`（既定 `tolo-tenant-management`）で設定する。

内部エラーは `internal` と固定メッセージ `internal error` だけを返し、原因はサーバー側のログ（`tenant-management: internal error: ...`）にのみ記録する。
エラーメッセージには内部主キー（UUIDv7）・テナント名・ユーザー ID を含めない。

### テナントのオンボーディング

テナントは未認証の仮登録と、認証済みユーザーによる所有権取得の二段階で作成する（`docs/tenant_management_spec.md` の「オンボーディング」）。

1. `StartTenantRegistration`（未認証）が `pending_owner` のテナントを作成し、所有権取得トークンの平文をこの応答でのみ返す。永続化するのは SHA-256 ハッシュだけで、トークンの有効期限は既定 1 時間（`application.DefaultOwnershipClaimTTL`）
2. `ClaimTenantOwnership`（`registration` トークン、scope `tenant.claim`）が、対象が期限内の `pending_owner` であることとトークンのハッシュ一致を検証し、内部 JWT の `sub` をオーナーとして所属させ、`owned` へ遷移させ、トークンを消費する。これらは 1 つの DB トランザクションで確定する
3. 以降は `tenantId` 指定で再認可した `tenant_access` でテナント配下を操作する

`pending_owner` のテナントは `ClaimTenantOwnership` 以外のテナント配下の RPC（`CreateEvent`、`ListEvents` など）を `failed_precondition` で拒否する。
期限切れの `pending_owner` は次の `StartTenantRegistration` の際に物理削除し、その名前を解放する。
トークンの不正・期限切れ・使用済みはいずれも `unauthenticated` で、理由は区別しない。

オーナー所属の書き込みは `application.MembershipWriter` ポートを通じて関係参照側（`internal/relation`）が担う。
`internal/relation/infra/db` の所属リポジトリがこのポートを実装し、テナント側と同じ接続プールと context 上のトランザクションを共有するため、オーナー所属と `owned` への遷移は同時に確定する。

### テナントの契約変更とアーカイブ

`ChangeTenantContract` と `ArchiveTenant` はいずれも `tenant_access` の内部 JWT と scope `tenant.write` を要求する。
`ChangeTenantContract` は契約プランを変更し、`contract_plan` の欠落は `invalid_argument` で拒否する。
`ArchiveTenant` は論理削除で、テナントは識別子と名前（名前は解放しない）、イベント（状態は変えない）、所属をそのまま保持する。
アーカイブ後はそのテナント配下の書き込み RPC を `failed_precondition` で拒否し、参照（`GetEvent`、`ListEvents`、`ListMemberships`）はそのまま利用できる。

どちらの RPC も、JWT の scope 検証に加えて、呼び出し元の現在の所属とロールを書き込みと同じ DB トランザクション内で読み直す（`docs/tenant_management_spec.md` の「管理系書き込みの現在権限確認」）。
再確認はテナント側の `application.CurrentPermissionChecker` ポートを通じて行い、関係参照側の `Authorizer`（`internal/relation/application/authorizer.go`）がこれを実装する。
所属が存在しない場合、または現在のロールがオーナーでなく `tenant.write` を発行できない場合は `permission_denied` を返す。
`pending_owner` のテナントへの操作は `failed_precondition`、存在しないテナントは `not_found` で拒否する。

### 関係参照（所属とロール）

所属とロールの真実の源は `internal/relation` 配下に置き、テナント側（`internal/domain`、`internal/application`）とはパッケージを分ける。
テナント側から関係参照側への参照は `MembershipWriter` と `CurrentPermissionChecker` の 2 つのポートに限り、逆方向の参照は作らない。

- スキーマ: `tenant_memberships`（テナント×ユーザーで一意）と `event_roles`。`event∈tenant` は `events (id, tenant_id)` への複合外部キー、`event-role⇒tenant-role` は `tenant_memberships` への外部キーで担保し、所属の削除はイベントロールへ連鎖する
- ロール: `owner`／`staff`。`admin` は予約値で付与できない
- リポジトリは内部 ID で所属を扱い、公開 ID は読み出し時に結合して付与する

#### RelationAdminService の認可

`tolo.relation.v1.RelationAdminService`（`proto/tolo/relation/v1/relation.proto`）は TenantService と同じプロセスで配信し、同じ検証器とテナント ID interceptor を共有する（`internal/relation/infra/connect`）。
すべての RPC が `tenant_access` を要求し、リクエストの `tenant_id`（`GrantEventRole` とイベント指定の `RevokeRole` はイベントの所属テナント）をクレームと突合する。

| RPC | 要求 scope | 主な応答コード |
|---|---|---|
| AddTenantMember、ChangeTenantRole、GrantEventRole、RevokeRole | `tenant.write` | 重複所属・所属のないイベントロール・アーカイブ済みまたは `pending_owner` のテナント／イベントは `failed_precondition`、`ROLE_ADMIN` は `invalid_argument`、存在しない所属・テナント・イベントは `not_found` |
| ListMemberships | `tenant.read` | `tenant_id` 指定はそのテナントの全所属、`user_id` 指定は認証テナント内のそのユーザーの所属のみ（他テナントの所属は返さない）。アーカイブ済みテナントも参照できる |

書き込み4 RPC は、JWT の scope 検証に加えて、呼び出し元自身の現在の所属とロールを書き込みと同じ DB トランザクション内で読み直す（`internal/relation/application/authorizer.go`）。
確認時に呼び出し元の所属行を `FOR SHARE` でロックするため、確認から書き込みの確定までの間に剥奪や降格が割り込むことはない。
同じトランザクションの冒頭でテナント単位のアドバイザリロック（`pg_advisory_xact_lock`）を取得し、同一テナントの所属書き込みを直列化するため、複数の管理者が互いを同時に剥奪・降格してもデッドロックしない。
所属が存在しない場合、または現在のロールが `tenant.write` を発行できない場合は `permission_denied` を返す。
ListMemberships は読み取りのため再確認せず、トークンの scope のみに依存する。

### テスト用内部 JWT の生成

`cmd/jwtgen` は ES256 の鍵ペアを生成し、内部 JWT と対応する公開 JWKS を JSON で出力する。

```bash
go run ./cmd/jwtgen -tenant-public-id 0123456789abcdef -scope events.read
```

`-token-use` は `tenant_access`（既定）、`registration`、`service` を取る。`tenant_access` では `-tenant-public-id`（ランダムな16文字hex）と `-scope` が必須、`registration` では `-scope` が必須。
`service` は既定でマシン起点（`scope`、`src_jti`、`tenant_id` を持たない）として生成し、`-origin-sub <user_id>` を指定するとユーザー起点（`scope`、`src_jti`、`origin_sub` を持ち、`-tenant-public-id` で `tenant_id` を付与できる）になる。
いずれの場合も `txn` には UUIDv7 を自動で付与する。

```bash
# マシン起点の service トークン
go run ./cmd/jwtgen -token-use service
# ユーザー起点の service トークン（テナント文脈付き）
go run ./cmd/jwtgen -token-use service -origin-sub user-1 -scope events.read -tenant-public-id 0123456789abcdef
```

出力された `jwks` を Service Gateway の JWKS スタブとして公開すると、出力された `token` を結合テストに利用できる。サービスのテストもこの CLI と同じ生成ロジックを使用する。

---

## proto アーティファクトの利用

`.proto` は [ORAS](https://oras.land) で OCI アーティファクト化され、GitHub Container Registry に公開される  
アーティファクト名: `ghcr.io/<owner>/<repo>/proto`

### 取得（pull）

[ORAS CLI](https://oras.land/docs/installation) が必要

```bash
# 出力先ディレクトリに proto を展開（ディレクトリ構造が復元される）
oras pull ghcr.io/pj-hoakari/tolo-tenant-management:latest -o proto

# 例: proto/tenant/v1/tenant.proto として展開される
```

取得した `.proto` は `buf` や `protoc` の入力としてそのまま利用できる
