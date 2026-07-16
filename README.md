# tolo-tenant-management

## 開発

### セットアップ
miseを使用して開発環境をセットアップ

```bash
mise trust
mise install
task proto
```

### connect-es の生成
connect-es の生成（`task proto:gen:es`）はリリース時に CI で行う  
ローカルで実行する場合は `clients/connect-es` の依存（`npm i`）を導入する必要がある

### TenantService の authz interceptor

`TenantService` は proto の policy annotation により、セルフサインアップには `tenant.register`、テナント操作には `tenant_access` と `tenant.write`、イベント操作には `events.read` / `events.write` を要求する。`GetEvent` は内部サービス向けの `AUTH_LEVEL_INTERNAL` である。
`internal/server` では、生成された `NewTenantServiceHandlerWithAuthz` に開発用 verifier を渡す。

ローカルでは次の Authorization ヘッダーで呼び出せる。

```text
Authorization: Bearer example-tenant-token
```

この固定トークンと固定スコープはあくまで interceptor の利用例であり、実運用では OIDC/JWT などで検証した identity claims を verifier から参照するよう置き換える

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
