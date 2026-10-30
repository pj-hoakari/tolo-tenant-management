# 認証・認可コンテキスト

## 責務

- ユーザーアカウントのセルフ登録（テナント紐づけ前のアカウント作成）
- ユーザー認証とテナント／イベント文脈のアクセストークン発行
- 関係参照（Relation）が所属・ロールを所有し、読み書きを担う
- テナント／イベント識別子はテナントが所有し、本コンテキストは参照整合を保つ

## ドメインモデル

| 語 | 内容 |
|---|---|
| ユーザー / User | IdP の認証主体<br>user_id（`sub`）は IdP がアカウントの作成時に発行する不変の識別子で、ログインに使う識別子とは別に持つ |
| 所属 / Membership | どのテナント／イベントにどのロールで属すか<br>Relation が読み書き所有 |
| `tenant_access` token | テナント文脈の JWT |
| `event_access` token | Token Exchange で発行するイベント文脈トークン |
| Token Exchange | tenant_access→event_access の一方向のみ<br>subject_token の発行先 client と要求 client の一致を検証し、public client も自ら交換する |
| scope | tenant.read／tenant.write／events.read と、権限クラスで分けた書き込み scope<br>events.manage（設計・構成）／events.operate（現場運用）／events.report（計測報告・稼働通知と、エッジ端末の観測点の参照） |
| 送信者拘束 / sender constraint | public client へ発行する外部トークンは、クライアントの鍵の所持証明（DPoP）で提示者を束縛する<br>鍵はクライアントの保護領域に取り出せない形で保持する<br>confidential client（BFF）は client 認証で代替し対象にしない |
| client scope 上限 / client scope cap | client ごとに許可する scope の上限。ロールから導いた scope との積を発行する |
| resource / audience | 対象イベント（URI）／バックエンド API 全体の論理 audience（例 backend-api。登録は1つ）<br>検証は Service Gateway と Edge Bridge Service が行う |
| テナント参加用の最小トークン / registration token | テナント文脈なしで、まだ所属していないテナントに入るためのトークン<br>scope は `tenant.join` のみで、仮テナントの所有権取得（ClaimTenantOwnership）と招待の受諾（AcceptTenantInvitation）に限定 |
| 招待 / tenant invitation | テナント、テナントロール（スタッフに限る）、表示名を固定し、一回限りの招待トークンで受諾させる参加の申し出<br>オーナーが作成、一覧、取り消しを行い、受諾した時点で所属が確定する |
| 表示名 / display name | 所属ごとに持つ、テナントの中だけで使う名前<br>招待またはオーナーの所有権取得で決まり、IdP の profile は使わない |
| 関係参照サービス / relation service | 所属とロールを所有し参照を返す |
| login challenge | IdP がログイン状態のない認可要求ごとに発行する一回限りの値<br>Web のログイン画面でのログインをその認可要求に結び付け、`prompt=login` はその challenge の認証でだけ満たす |

ロール→scope：オーナー＝全scope／スタッフ＝tenant.write を除く全scope／管理者（予約）＝オーナー相当
発行する scope はロールとクライアント種別の両方から決める（client scope 上限との積）
ロールは JWT claim に載せない

## ドメインイベント

| イベント | 英語 | 契機 |
|---|---|---|
| ユーザー登録された | UserRegistered | セルフサインアップのアカウント作成（テナント紐づけ前） |
| ログインした | UserLoggedIn | ログイン成功（Web のログイン画面からのログイン、サインアップの成功） |
| ログイン失敗した | LoginFailed | 認証失敗 |
| ログアウトした | UserLoggedOut | ログアウト |
| テナントアクセストークン発行された | TenantAccessTokenIssued | 認可コードフロー |
| テナント参加用トークン発行された | RegistrationTokenIssued | テナント未指定の認可コードフロー（仮テナントの所有権取得、招待の受諾） |
| イベントアクセストークン発行された | EventAccessTokenIssued | Token Exchange 成功 |
| トークン交換拒否された | TokenExchangeDenied | 遷移／audience／scope 違反、subject_token の発行先 client と要求 client の不一致、archived イベントへの交換、DPoP の鍵不一致 |
| トークン失効された | TokenRevoked | 失効要求 |
| 監査ログ記録された | AuditLogRecorded | ログイン／ログアウト／発行／交換／失効の成否 |
| 招待が作成された | TenantInvitationCreated | オーナーによる招待の作成 |
| 招待が取り消された | TenantInvitationRevoked | オーナーによる招待の取り消し |
| ユーザーがテナントに所属した | UserJoinedTenant | 招待の受諾、メンバー追加 |
| テナントロール変更された | TenantRoleChanged | テナントロール付与・変更 |
| イベントロール付与された | EventRoleGranted | 割り当て |
| ロール剥奪された | RoleRevoked | メンバー削除／ロール解除 |
| 関係参照キャッシュ更新された | RelationCacheUpdated | 参照成功 |
| 関係参照キャッシュパージされた | RelationCachePurged | 所属変更時 |

## 不変条件

- relation model 制約：1ユーザーは複数テナントに所属しうる／event∈tenant／event-role⇒tenant-role／tenant-role と event-role は独立
- Token Exchange は tenant→event の一方向のみ
- 発行する scope は client scope 上限とロール由来 scope の積を超えない
- ロールは scope の発行判定にのみ使う（claim に載せない）
- 識別子はテナントが所有し、存在しないIDへは所属を作らない
- イベント状態はテナントが所有し、本コンテキストは持たない
