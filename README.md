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

### connect-es の生成
connect-es の生成（`task proto:gen:es`）はリリース時に CI で行う  
ローカルで実行する場合は `clients/connect-es` の依存（`npm i`）を導入する必要がある

### TenantService の authz interceptor

`TenantService` は proto の policy annotation により、セルフサインアップには `tenant.register`、テナント操作には `tenant_access` と `tenant.write`、イベント操作には `events.read` / `events.write` を要求する。`GetEvent` は内部サービス向けの `AUTH_LEVEL_INTERNAL` である。
`internal/jwks` の JWKS validator を、`internal/infra/connect` の認可 verifier とテナント ID interceptor で共有する。

API Gateway が発行した内部 JWT を Authorization ヘッダーで受け付ける。サービス側では ES256 の署名、`kid`、`iss`、`aud`、`exp`／`nbf`、`token_use`、scope を検証する。

```text
Authorization: Bearer <API Gateway 発行の内部 JWT>
```

`RegisterTenant` は `token_use=registration`、`GetEvent` は `token_use=service`、その他の TenantService RPC は `token_use=tenant_access` を要求する。後者には `tenant_id` クレームが必須である。

```text
{"tenant_id":"<tenant UUIDv7>"}
```

JWKS は `INTERNAL_JWKS_URL` から取得する。未設定時は API Gateway コンテナのエンドポイント `http://gateway:8080/.well-known/jwks.json` を使う。取得した鍵は 5 分間キャッシュし、未知の `kid` を受信した場合は直ちに再取得する。

### テスト用内部 JWT の生成

`cmd/jwtgen` は ES256 の鍵ペアを生成し、内部 JWT と対応する公開 JWKS を JSON で出力する。

```bash
go run ./cmd/jwtgen -tenant-id test-tenant -scope events.read
```

`tenant_access` では `-tenant-id` が必須。`-token-use service` または `-token-use registration` も指定できる。
出力された `jwks` を API Gateway の JWKS スタブとして公開すると、出力された `token` を結合テストに利用できる。サービスのテストもこの CLI と同じ生成ロジックを使用する。

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
