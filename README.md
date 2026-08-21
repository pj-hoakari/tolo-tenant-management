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

### テナントのオンボーディング

テナントは未認証の仮登録と、認証済みユーザーによる所有権取得の二段階で作成する（`docs/tenant_management_spec.md` の「オンボーディング」）。

1. `StartTenantRegistration`（未認証）が `pending_owner` のテナントを作成し、所有権取得トークンの平文をこの応答でのみ返す。永続化するのは SHA-256 ハッシュだけで、トークンの有効期限は既定 1 時間（`application.DefaultOwnershipClaimTTL`）
2. `ClaimTenantOwnership`（`registration` トークン、scope `tenant.claim`）が、対象が期限内の `pending_owner` であることとトークンのハッシュ一致を検証し、内部 JWT の `sub` をオーナーとして所属させ、`owned` へ遷移させ、トークンを消費する。これらは 1 つの DB トランザクションで確定する
3. 以降は `tenantId` 指定で再認可した `tenant_access` でテナント配下を操作する

`pending_owner` のテナントは `ClaimTenantOwnership` 以外のテナント配下の RPC（`CreateEvent`、`ListEvents` など）を `failed_precondition` で拒否する。
期限切れの `pending_owner` は次の `StartTenantRegistration` の際に物理削除し、その名前を解放する。
トークンの不正・期限切れ・使用済みはいずれも `unauthenticated` で、理由は区別しない。

オーナー所属の書き込みは `application.MembershipWriter` ポートを通じて関係参照側（RelationAdminService の実装）が担う。
関係参照が未実装の間は `UnavailableMembershipWriter` を配線しており、`ClaimTenantOwnership` は `unavailable` を返してフェイルクローズする（オーナー不在の `owned` テナントを作らない）。

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
