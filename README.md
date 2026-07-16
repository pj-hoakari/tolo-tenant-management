# go-service-template

Connect (connect-go) ベースの Go マイクロサービス開発用テンプレートリポジトリ

- HTTP サーバ（ヘルスチェック `GET /healthz` + graceful shutdown）
- マルチステージ Dockerfile（distroless）とコンテナイメージ公開ワークフロー
- `buf` による proto の lint / コード生成（connect-go・connect-es）
- `.proto` を ORAS で OCI アーティファクト化し GitHub Container Registry へ公開するワークフロー
- connect-es クライアントの npm publish ワークフロー
- 生成物の drift-check（connect-go）ワークフロー
- `mise` による開発ツール（buf / go / task）のバージョン固定

---

## テンプレート利用時の初期設定

開発を始める前にプロジェクト向けに変更する必要がある

### 1. Go モジュールパス

テンプレートの `github.com/pj-hoakari/go-service-template` を新しいモジュールパスへ置換

| ファイル | 箇所 | 内容 |
| --- | --- | --- |
| `go.mod` | `module` 行 | モジュールパス |
| `cmd/server/main.go` | import | `.../internal/server` の参照 |
| `buf.gen.go.yaml` | `go_package_prefix` | 生成コードのパッケージ接頭辞 |

生成物（`gen/`）を除いて一括置換

```bash
OLD=github.com/pj-hoakari/go-service-template
NEW=github.com/<owner>/<repo>

# gen/ は再生成で追従
git grep -lz "$OLD" -- ':!gen' | xargs -0 sed -i "s#$OLD#$NEW#g"

go mod tidy
task proto:gen:go
```

### 2. connect-es クライアント（npm パッケージ）

`clients/connect-es` は GitHub Packages に publish される npm クライアント向け実装

| ファイル | 箇所 | 内容 |
| --- | --- | --- |
| `clients/connect-es/package.json` | `name` | `@<scope>/<repo>-client-es` |
| `clients/connect-es/package.json` | `description` | パッケージ説明 |
| `clients/connect-es/package.json` | `repository.url` | 新しいリポジトリ URL |
| `.github/workflows/publish-client-es.yml` | `scope` | `@<owner>` へ（GitHub Packages のスコープと一致） |

`package.json` の `name` を変更し、ロックファイルを同期

```bash
cd clients/connect-es && npm install
```

### 3. その他

- `cmd/server/main.go` のログ文字列 `go-service-template: ...`
- `mise.toml` の Go / buf バージョン
    buf の版を変える場合は `.github/workflows/proto-gen-check.yml` の `version:` も揃える

---

## 初期設定チェックリスト

- [ ] Go モジュールパスを `github.com/<owner>/<repo>` に置換（`go.mod` / `cmd/server/main.go` / `buf.gen.go.yaml`）
- [ ] `go mod tidy` を実行
- [ ] `task proto:gen:go` で connect-go を再生成し、`gen/` をコミット

- [ ] connect-es の `package.json`（`name` / `description` / `repository.url`）を更新
- [ ] `clients/connect-es` で `npm install` を実行し `package-lock.json` を同期
- [ ] `publish-client-es.yml` の `scope` を `@<owner>` に変更

- [ ] `main.go` のログ文字列
- [ ] README のテンプレート説明を書き換え

---

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
oras pull ghcr.io/pj-hoakari/go-service-template-proto:latest -o proto

# 例: proto/greet/v1/greet.proto として展開される
```

取得した `.proto` は `buf` や `protoc` の入力としてそのまま利用できる
