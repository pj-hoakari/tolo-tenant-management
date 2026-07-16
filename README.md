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

### greet service の authz interceptor

`Greet` は proto の policy annotation により `AUTH_LEVEL_AUTHENTICATED` と `greeting.read` スコープを要求する  
`internal/server` では、生成された `NewGreetServiceHandlerWithAuthz` に開発用 verifier を渡す　　

ローカルでは次の Authorization ヘッダーで呼び出せる　　

```text
Authorization: Bearer example-greet-token
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

# 例: proto/greet/v1/greet.proto として展開される
```

取得した `.proto` は `buf` や `protoc` の入力としてそのまま利用できる
