# tolo-tenant-management

テナントとイベントの識別子の正本（`tolo.tenant.v1.TenantService`）と、所属とロールの真実の源である関係参照（`tolo.relation.v1.RelationAdminService`）を、1 つのプロセスで Service Gateway の背後に配信するサービスである。
仕様の正本は `docs/tenant_management_spec.md` をはじめとする `docs` 配下の文書に置く。

## 開発

### セットアップ

mise を使用して開発環境をセットアップする。

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

もしくは次を実行すると、同じ構成をデタッチモード（`-d`）で起動する。

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

もしくは次を実行すると、同じ構成をデタッチモード（`-d`）で起動する。

```bash
task up:build:o11y
```

Jaeger UI は `http://localhost:16686` で待ち受ける。

### connect-es の生成

connect-es の生成（`task proto:gen:es`）はリリース時に CI で行う。
ローカルで実行する場合は `clients/connect-es` の依存（`npm i`）を導入する必要がある。

### 環境変数

サーバー（`cmd/server`）が読む環境変数は次のとおりである。

| 環境変数 | 既定値 | 意味 |
|---|---|---|
| `DATABASE_URL` | なし（必須） | PostgreSQL の接続先。未設定ならサーバーは起動しない |
| `SERVER_ADDR` | `:8080` | HTTP サーバーの待ち受けアドレス |
| `LOG_LEVEL` | `info` | ログに出力する最小レベル。`debug`／`info`／`warn`／`error`／`critical` を取り、未知の値ならサーバーは起動しない |
| `INTERNAL_JWKS_URL` | `http://gateway:8080/.well-known/jwks.json` | 内部 JWT の署名鍵を取得する JWKS エンドポイント |
| `INTERNAL_JWT_ISSUER` | `service-gateway` | 内部 JWT に要求する `iss`。Service Gateway の発行者識別子に合わせる |
| `INTERNAL_JWT_AUDIENCE` | `tolo-tenant-management` | 内部 JWT に要求する `aud` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | なし | OTLP/HTTP のエクスポート先 |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | なし | トレース専用の OTLP エクスポート先 |
| `OTEL_SERVICE_NAME` | `tolo-tenant-management` | トレースが報告する `service.name` |
| `GOOGLE_CLOUD_PROJECT` | なし（未設定可） | 設定するとログの `logging.googleapis.com/trace` を `projects/<project>/traces/<trace_id>` 形式にし、Cloud Logging でトレースと相関させる |

OTLP のエクスポート先がどちらも未設定の場合、トレースは no-op のまま起動する。
そのほかの OTLP 用の環境変数（ヘッダー、TLS、タイムアウトなど）はエクスポータ自身が解釈する。
`DATABASE_URL` は `task migrate:*` でも参照し、未設定の場合は `compose.yml` の PostgreSQL を指す既定値を使う。

### ログ

ログは標準出力へ 1 行 1 件の JSON で書き出し、Cloud Logging がそのまま解釈する構造化フォーマット（`severity`、`message`、`time`）に合わせている。
トレースが有効なリクエストでは、その文脈からトレース ID とスパン ID を自動で読み取り、`logging.googleapis.com/trace` などのフィールドとして各レコードに付与する。
このハンドラは `internal/logging` が提供し、最小レベルは `LOG_LEVEL`、トレースの相関先プロジェクトは `GOOGLE_CLOUD_PROJECT` で指定する。
`message` や `severity` のような予約キーと同じ名前の属性は、値が上書きされないように `attr_` 接頭辞付きで出力する。
`net/http` と OpenTelemetry が内部で出すログも同じハンドラに流すので、サーバーのログはこの 1 系統にまとまる。

### テスト用内部 JWT の生成

`cmd/jwtgen` は ES256 の鍵ペアを生成し、内部 JWT と対応する公開 JWKS を JSON で出力する。

```bash
go run ./cmd/jwtgen -tenant-public-id 0123456789abcdef -scope events.read
```

`-token-use` は `tenant_access`（既定）、`registration`、`service` を取る。
`tenant_access` では `-tenant-public-id`（ランダムな 16 文字 hex）と `-scope` が必須、`registration` では `-scope` が必須である。
`service` は既定でマシン起点（`scope`、`src_jti`、`tenant_id` を持たない）として生成し、`-origin-sub <user_id>` を指定するとユーザー起点（`scope`、`src_jti`、`origin_sub` を持ち、`-tenant-public-id` で `tenant_id` を付与できる）になる。
いずれの場合も `txn` には UUIDv7 を自動で付与する。
そのほかのフラグは `-ttl`（既定 2 分）、`-kid`（既定 `test-key`）、`-issuer`／`-audience`（既定は `INTERNAL_JWT_ISSUER`／`INTERNAL_JWT_AUDIENCE` の既定値と同じ）である。

```bash
# マシン起点の service トークン
go run ./cmd/jwtgen -token-use service
# ユーザー起点の service トークン（テナント文脈付き）
go run ./cmd/jwtgen -token-use service -origin-sub user-1 -scope events.read -tenant-public-id 0123456789abcdef
```

出力された `jwks` を Service Gateway の JWKS スタブとして公開すると、出力された `token` を結合テストに利用できる。
サービスのテストもこの CLI と同じ生成ロジックを使用する。

## 認証と認可

### 内部 JWT の検証

Service Gateway が発行した内部 JWT を Authorization ヘッダーで受け付ける。
サービス側では ES256 の署名、`kid`、`iss`、`aud`、`exp`／`nbf`、`token_use`、scope を検証する。
`internal/jwks` の JWKS validator を、`internal/infra/connect` の認可 verifier とテナント ID interceptor で共有する。

```text
Authorization: Bearer <Service Gateway 発行の内部 JWT>
```

JWKS の取得先は `INTERNAL_JWKS_URL`、`iss`／`aud` の期待値は `INTERNAL_JWT_ISSUER` と `INTERNAL_JWT_AUDIENCE` で設定する（既定値は「環境変数」）。
取得した鍵は 5 分間キャッシュし、未知の `kid` を受信した場合は直ちに再取得する。

クレーム構造の正本は `docs/internal_jwt.md` である。
すべての内部 JWT に `sub`、`client_id`、`jti`、`iat`／`nbf`／`exp`、`token_use`、`txn` を要求し、`token_use` ごとに次を追加で要求する。
`txn` と `origin_sub` は監査とトレース用で、認可判定には使わない。

| `token_use` | 追加で要求するクレーム |
|---|---|
| `tenant_access` | `scope`、`src_jti`、`tenant_id` |
| `event_access` | `scope`、`src_jti`、`tenant_id`、`event_id` |
| `registration` | `scope`、`src_jti`（`tenant_id`／`event_id` を持たない） |
| `service`（ユーザー起点） | `scope`、`src_jti`、`origin_sub`（`tenant_id`／`event_id` は文脈トークンから引き写された場合のみ） |
| `service`（マシン起点） | なし（`scope`、`src_jti`、`origin_sub`、`tenant_id`、`event_id` のいずれも持たない） |

### 識別子とテナントの突合

proto の `tenant_id`／`event_id` はいずれも公開 ID（ランダムな 16 文字 hex）で、内部主キー（UUIDv7）は外に出ない。
`tenant_access` の内部 JWT には `tenant_id` クレームが必須で、その値も同じ公開 ID である。

```text
{"tenant_id":"<tenant public ID: 16-character hex>"}
```

テナント配下を対象にする RPC（`CreateEvent`、`ListEvents` など）はリクエストの `tenant_id` で対象を指定し、サービスはクレームの `tenant_id` と突合する。
不一致は `permission_denied`、`tenant_id` の欠落は `invalid_argument` を返す。
イベントを対象にする RPC（`AssignEventType`、`TransitionEventStatus`）は、読み込んだイベントの所属テナントをクレームと突合する。

サービス間の参照系 RPC は境界の扱いが異なる。
`GetEvent` はテナント文脈を持つ `service` トークン（ユーザー起点で `tenant_id` クレームを持つもの）を要求し、クレームがなければ `unauthenticated`、イベントの所属テナントと不一致なら `permission_denied` を返す。
`GetObservationSettings` はテナント文脈のない `service` トークン（マシン起点）も受け付け、クレームがある場合だけ突合する。

### RPC ごとの認可

どちらのサービスも proto の policy annotation（`authz.v1.auth_policy`）で RPC ごとの公開面と要求 scope を宣言する。
生成された authz verifier は `internal/infra/connect` の `AuthorizeCall` を共有し、procedure ごとに定めた `token_use` と宣言された scope を検証する。

| RPC | サービス | 公開面 | 要求する `token_use` | 要求 scope |
|---|---|---|---|---|
| StartTenantRegistration | `TenantService` | 未認証（`AUTH_LEVEL_PUBLIC`） | なし | なし |
| ClaimTenantOwnership | `TenantService` | 認証済み | `registration` | `tenant.claim` |
| ChangeTenantContract、ArchiveTenant | `TenantService` | 認証済み | `tenant_access` | `tenant.write` |
| CreateEvent、AssignEventType、TransitionEventStatus、UpdateObservationSettings | `TenantService` | 認証済み | `tenant_access` | `events.write` |
| ListEvents | `TenantService` | 認証済み | `tenant_access` | `events.read` |
| GetEvent、GetObservationSettings | `TenantService` | 内部（`AUTH_LEVEL_INTERNAL`） | `service` | なし |
| AddTenantMember、ChangeTenantRole、GrantEventRole、RevokeRole | `RelationAdminService` | 認証済み | `tenant_access` | `tenant.write` |
| ListMemberships | `RelationAdminService` | 認証済み | `tenant_access` | `tenant.read` |

### 管理系書き込みの現在権限確認

管理系の書き込み 6 RPC（`ArchiveTenant`、`ChangeTenantContract`、`AddTenantMember`、`ChangeTenantRole`、`GrantEventRole`、`RevokeRole`）は、JWT の scope 検証に加えて、呼び出し元自身の現在の所属とロールを書き込みと同じ DB トランザクション内で読み直す（`docs/tenant_management_spec.md` の「管理系書き込みの現在権限確認」）。
`scope` はトークン発行時点で与えられた権限しか表さないため、剥奪や降格はトークンの期限切れを待たずにこの再確認で反映する。
確認時に呼び出し元の所属行を `FOR SHARE` でロックするため、確認から書き込みの確定までの間に剥奪や降格が割り込むことはない。
関係参照側の 4 RPC は、同じトランザクションの冒頭でテナント単位のアドバイザリロック（`pg_advisory_xact_lock`）を取得して同一テナントの所属書き込みを直列化するため、複数の管理者が互いを同時に剥奪、降格してもデッドロックしない。
所属が存在しない場合、または現在のロールが `tenant.write` を発行できない場合は `permission_denied` を返す。
テナント側の `ArchiveTenant` と `ChangeTenantContract` はこの確認を `application.CurrentPermissionChecker` ポートを通じて行い、関係参照側の `Authorizer`（`internal/relation/application/authorizer.go`）がこれを実装する。
`ListMemberships` をはじめとする参照系 RPC は再確認せず、トークンの scope のみに依存する。

### エラー

内部エラーは `internal` と固定メッセージ `internal error` だけを返し、原因はサーバー側のログにのみ記録する。
そのログは `message` が `internal error`、`error` 属性が原因という構造化レコードで、トレースが有効なときはトレースのフィールドも付く（「ログ」を参照）。
クライアント都合の中断と締め切り超過は `canceled`／`deadline_exceeded` として返し、サーバー側のログには記録しない。
エラーメッセージには内部主キー（UUIDv7）、テナント名、ユーザー ID を含めない。
`connectrpc.CodeInternal` の直接使用は golangci-lint の `forbidigo` で禁止しており、許可するのは `InternalError` の中だけである。

## テナントとイベント

### オンボーディング

テナントは未認証の仮登録と、認証済みユーザーによる所有権取得の二段階で作成する（`docs/tenant_management_spec.md` の「オンボーディング」）。

1. `StartTenantRegistration`（未認証）が `pending_owner` のテナントを作成し、所有権取得トークンの平文をこの応答でのみ返す。
   永続化するのは SHA-256 ハッシュだけで、トークンの有効期限は既定 1 時間（`application.DefaultOwnershipClaimTTL`）である。
2. `ClaimTenantOwnership`（`registration` トークン、scope `tenant.claim`）が、対象が期限内の `pending_owner` であることとトークンのハッシュ一致を検証し、内部 JWT の `sub` をオーナーとして所属させ、`owned` へ遷移させ、トークンを消費する。
   これらは 1 つの DB トランザクションで確定する。
3. 以降は `tenantId` 指定で再認可した `tenant_access` でテナント配下を操作する。

`pending_owner` のテナントは `ClaimTenantOwnership` 以外のテナント配下の RPC（`CreateEvent`、`ListEvents` など）を `failed_precondition` で拒否する。
期限切れの `pending_owner` は次の `StartTenantRegistration` の際に物理削除し、その名前を解放する。
トークンの不正、期限切れ、使用済みはいずれも `unauthenticated` で、理由は区別しない。

### 契約変更とアーカイブ

`ChangeTenantContract` と `ArchiveTenant` はいずれも `tenant_access` の内部 JWT と scope `tenant.write` を要求する。
`ChangeTenantContract` は契約プランを変更し、`contract_plan` の欠落は `invalid_argument` で拒否する。
`ArchiveTenant` は論理削除で、テナントは識別子と名前（名前は解放しない）、イベント（状態は変えない）、所属をそのまま保持する。
アーカイブ後はそのテナント配下の書き込み RPC を `failed_precondition` で拒否し、参照（`GetEvent`、`ListEvents`、`ListMemberships`）はそのまま利用できる。

どちらの RPC も、JWT の scope 検証に加えて呼び出し元の現在権限を確認する（「管理系書き込みの現在権限確認」）。
`pending_owner` のテナントへの操作は `failed_precondition`、存在しないテナントは `not_found` で拒否する。

### イベントの状態遷移と一覧

`TransitionEventStatus` が許容する状態遷移は次の 8 つだけで、自状態への遷移を含むそれ以外はすべて `failed_precondition` で拒否する（`docs/tenant_management_spec.md` の「イベント」）。

| from → to | 意味 |
|---|---|
| draft → open | イベントを公開する |
| draft → archived | 作成した draft を破棄する |
| open → locked | 受付を締め切る |
| locked → open | 締め切りを解除する |
| locked → closed | イベントを終了する |
| closed → open | 終了したイベントを再開する |
| closed → archived | 終了したイベントを論理削除する |
| archived → closed | 論理削除を取り消す |

draft はそのままアーカイブでき、これは作成した draft を破棄する経路である。
アーカイブからの復元は `archived → closed` のみで、draft へは戻らない。

`ListEvents` はテナント配下のイベントを作成順（内部主キーの UUIDv7 順）に返す。
アーカイブ済みイベントは既定では返さず、`include_archived` を指定したときだけ含める。
返却件数の上限は 1000 件（`repository.MaxListEvents`）で、ページングは設けない。

### 観測設定値

イベントの観測設定値は `history_window_days` のみを持つ（`docs/tenant_management_spec.md` の「その他」）。
既定は 30 で、1 以上の値でなければならない。
値は `events` テーブルの列として保持し、イベントの作成時に既定値が入る。

`GetObservationSettings` はサービス間の参照系 RPC で、応答は観測設定値だけを含み、イベント名、状態、所属テナントは返さない。
アーカイブ済みのイベントについても応答し、宙づりの参照を生まない。
テナント境界は強制せず、`tenant_id` クレームを持つ場合だけ突合する（「識別子とテナントの突合」）。

`UpdateObservationSettings` は `tenant_access` と scope `events.write` を要求し、対象イベントの所属テナントをクレームと突合する。
アーカイブ済みのイベント、およびアーカイブ済みテナントのイベントは `failed_precondition` で拒否する。
`history_window_days` が 1 未満の場合は `invalid_argument` を返す。

## 関係参照（所属とロール）

### パッケージ境界とポート

所属とロールの真実の源は `internal/relation` 配下に置き、テナント側（`internal/domain`、`internal/application`）とはパッケージを分ける。
テナント側は `internal/relation` を直接参照せず、自身が宣言する `application.MembershipWriter` と `application.CurrentPermissionChecker` の 2 つのポートを通じてのみ関係参照側に到達する。
`MembershipWriter` は `ClaimTenantOwnership` のオーナー所属の書き込みに、`CurrentPermissionChecker` は管理系書き込みの現在権限確認に使う。
前者は `internal/relation/infra/db` の所属リポジトリが、後者は `internal/relation/application` の `Authorizer` が実装する。
所属リポジトリはテナント側と同じ接続プールと context 上のトランザクションを共有するため、オーナー所属と `owned` への遷移は同時に確定する。
関係参照側は、公開 ID の解決と書き込み可否を決めるテナントの状態（`pending_owner`、アーカイブ済み）の判定のために、テナント側のリポジトリを読み取り専用で参照する。
読み取りに加えて、`internal/infra/db` のトランザクション実行器と `internal/infra/connect` の検証器の中核および interceptor もテナント側のものを再利用する。

### スキーマとロール

- スキーマ: `tenant_memberships`（テナント×ユーザーで一意）と `event_roles`。`event∈tenant` は `events (id, tenant_id)` への複合外部キー、`event-role⇒tenant-role` は `tenant_memberships` への外部キーで担保し、所属の削除はイベントロールへ連鎖する
- ロール: `owner`／`staff`。`admin` は予約値で付与できない
- リポジトリは内部 ID で所属を扱い、公開 ID は読み出し時に結合して付与する

### RelationAdminService

`tolo.relation.v1.RelationAdminService`（`proto/tolo/relation/v1/relation.proto`）は TenantService と同じプロセスで配信し、同じ検証器とテナント ID interceptor を共有する（`internal/relation/infra/connect`）。
すべての RPC が `tenant_access` を要求し、リクエストの `tenant_id`（`GrantEventRole` とイベント指定の `RevokeRole` はイベントの所属テナント）をクレームと突合する。

| RPC | 要求 scope | 主な応答コード |
|---|---|---|
| AddTenantMember、ChangeTenantRole、GrantEventRole、RevokeRole | `tenant.write` | 重複した所属、所属のないイベントロール、アーカイブ済みまたは `pending_owner` のテナントとイベントは `failed_precondition`、`ROLE_ADMIN` は `invalid_argument`、存在しない所属、テナント、イベントは `not_found` |
| ListMemberships | `tenant.read` | `tenant_id` 指定はそのテナントの全所属、`user_id` 指定は認証テナント内のそのユーザーの所属のみ（他テナントの所属は返さない）。アーカイブ済みテナントも参照できる |

書き込み 4 RPC は、JWT の scope 検証に加えて呼び出し元の現在権限を確認する（「管理系書き込みの現在権限確認」）。

## 仕様文書

- `docs/tenant_management_spec.md`: 本サービスの入出力仕様。TenantService と RelationAdminService の RPC 一覧と振る舞いの正本
- `docs/tenant_context.md`: テナントコンテキストのドメインイベントと RPC の対応
- `docs/tenant_domain.md`: テナントコンテキストの責務とドメインモデル
- `docs/auth_context.md`: 認証・認可コンテキストのドメインイベントと入出力の対応
- `docs/auth_domain.md`: 認証・認可コンテキストの責務とドメインモデル。関係参照が所属とロールを読み書き所有する
- `docs/internal_jwt.md`: 内部 JWT の構造。`token_use`、scope、公開面の 3 軸とクレームの正本
- `docs/service_gateway.md`: Service Gateway の入出力仕様。外部資格情報の検証と内部 JWT への変換
- `docs/service_map.md`: サービス間の関係と主要なやり取りの図

## proto アーティファクトの利用

`.proto` は [ORAS](https://oras.land) で OCI アーティファクト化され、GitHub Container Registry に公開される。
アーティファクト名は `ghcr.io/<owner>/<repo>-proto` である。

### 取得（pull）

[ORAS CLI](https://oras.land/docs/installation) が必要である。

```bash
# 出力先ディレクトリに proto を展開（ディレクトリ構造が復元される）
oras pull ghcr.io/pj-hoakari/tolo-tenant-management-proto:latest -o proto

# 例: proto/tolo/tenant/v1/tenant.proto として展開される
```

取得した `.proto` は `buf` や `protoc` の入力としてそのまま利用できる。
