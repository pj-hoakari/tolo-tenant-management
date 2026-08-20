# API Gateway 入出力仕様

作成日: 2026-07-03
位置づけ: デプロイ単位。自身の proto を持たず、各サービスの proto を透過的に公開するプロキシ（Auth 以外の同期 RPC はすべて経由）
役割: 外部資格情報の検証と内部 JWT への変換（トークン変換点）、および宛先サービスへの転送
IdP 側への変更依頼: idp_change_list.md（tolo リポジトリ）

## 公開面と転送

- 公開面は各サービスの proto（Connect-RPC）をメソッド単位でそのまま公開する。リクエスト・レスポンスのペイロードは変換しない
- 変換対象は `authorization` メタデータのみ: 外部トークンを検証し、内部 JWT へ差し替えて宛先サービスを呼び出す
- ストリーミング RPC はストリーム開始時に検証する
- PubSub の publish／購読と Firestore 変更通知は経由しない（ブローカー仲介）
- マイクロサービスへ API Gateway を迂回して到達できないことをインフラ層（ネットワーク構成）で保証する

## 公開 proto と宛先サービスのマッピング

API Gateway は各サービスの proto をメソッド単位で透過公開するため、公開 proto と宛先 proto は同一（変換なし）
表の「公開区分」は API Gateway がどのクライアントへ公開するかを示す

- 公開: Web（BFF 経由）・スタッフアプリ・エッジ端末・ゲスト等のクライアントが呼べる
- 内部オンリー: サービス間呼び出し専用。API Gateway は token_use=service（後述「サービス間経路の検証と再発行」で発行）のみ許可し、クライアントへは公開しない（違反は permission_denied）

各 RPC の認可の正本は各サービス仕様（本表は導出）

| 宛先サービス | proto service | RPC | 公開区分 |
|---|---|---|---|
| Tenant Management | `tolo.tenant.v1.TenantService` | RegisterTenant | 公開（管理 UI。registration） |
| 〃 | 〃 | ChangeTenantContract、ArchiveTenant、CreateEvent、AssignEventType、TransitionEventStatus、ListEvents | 公開（管理 UI、ListEvents はスタッフアプリも） |
| 〃 | 〃 | GetEvent | 内部オンリー（Relation・Graph Authoring・Observation の参照整合） |
| Relation | `tolo.relation.v1.RelationAdminService` | ChangeTenantRole、GrantEventRole、RevokeRole、ListMemberships | 公開（管理 UI） |
| 〃 | 〃 | AddTenantMember | 公開（管理 UI）＋内部（Tenant Management の RegisterTenant 由来） |
| Graph Authoring | `tolo.graph.v1.GraphAuthoringService` | 全 RPC（CreateGraph〜PublishRevision） | 公開（管理・設計 UI） |
| 〃 | `tolo.graph.v1.GraphSupplyService` | 全 RPC（GetCurrentRevision、GetObservationPointMappings、GetDisplayNames、GetGatePoints、GetQrLocations） | 内部オンリー（Observation・Guest Service） |
| Observation | `tolo.observation.v1.MeasurementIngestService` | ReportMeasurements | 公開（エッジ端末。デバイストークンでパススルー）＋内部（Guest Service の QR 計上由来） |
| 〃 | `tolo.observation.v1.EdgeDeviceService` | RegisterEdgeDevice、ListEdgeDevices | 公開（管理・設計 UI） |
| 〃 | 〃 | Heartbeat | 公開（エッジ端末。デバイストークンでパススルー） |
| 〃 | 〃 | UpdateObservationPointConfig | 公開（スタッフアプリ） |
| 〃 | `tolo.observation.v1.ManualInterventionService` | 全 RPC（OperateGate、ToggleDangerFlag、RegisterScheduleEvent、ReportCongestion、CorrectQueue） | 公開（スタッフアプリ） |
| 〃 | `tolo.observation.v1.StatusQueryService` | GetEventOverview | 公開（スタッフアプリ） |
| 〃 | 〃 | GetGuestSnapshot | 内部オンリー（Guest Service の復旧 pull） |
| Flow Control | `tolo.flow.v1.FlowControlService` | Optimize | 内部オンリー（Observation のみ） |
| Line Control | `tolo.line.v1.LineControlService` | GuideQueues | 内部オンリー（Observation のみ） |
| Operation | `tolo.operation.v1.StaffCommunicationService` | SendStaffMessage、RevokeStaffMessage、ShareGuidanceInfo | 公開（スタッフアプリ） |
| 〃 | 〃 | ListStaffMessages | 公開（スタッフアプリ）＋内部（Guest Service の復旧 pull） |
| 〃 | `tolo.operation.v1.OperationControlService` | 全 RPC（DirectReassignment、ApplyReassignment、ListReassignments） | 公開（スタッフアプリ、ListReassignments は管理・設計 UI も） |
| 〃 | `tolo.operation.v1.DeliveryCoordinationService` | UpdateConnectionState | 公開（スタッフアプリ） |
| 〃 | 〃 | RequestProposalDelivery、RecordFeedbackValues | 内部オンリー（Observation） |
| 〃 | `tolo.operation.v1.DigestService` | GetHistoryDigest | 公開（スタッフアプリ） |
| Realtime | `tolo.realtime.v1.RealtimeFetchService` | FetchDeliveries | 公開（スタッフアプリ） |
| Notification | `tolo.notification.v1.NotificationService` | SendPush | 内部オンリー（Operation） |
| 〃 | `tolo.notification.v1.DeviceTokenService` | RegisterDeviceToken、UnregisterDeviceToken | 公開（スタッフアプリ） |
| Reference Aggregation | `tolo.refagg.v1.ReferenceAggregationService` | SubmitAnonymizedFeedback、GetReferenceValues | 内部オンリー（Operation 投入、Observation 取得） |
| Guest Service | —（proto なし。ゲスト向け HTTP） | ゲスト用ページ・コンポーネント API | 公開（ゲスト。未認証でパススルー） |

補足

- 同一 RPC が公開と内部の両方を持つもの（AddTenantMember、ReportMeasurements、ListStaffMessages）は、API Gateway が経路ごとに許可を判定する
  AddTenantMember・ListStaffMessages は公開側＝認可表の token_use、内部側＝service。ReportMeasurements の公開側はデバイストークンのパススルー（token_use 判定なし）で、内部側＝service
- PubSub 3トピックと Firestore 変更通知は API Gateway を経由しないため本表の対象外

## 経路の分類

| 経路 | 外部資格情報 | 動作 |
|---|---|---|
| ユーザー（テナント文脈） | `tenant_access`（IdP 発行） | 検証＋内部 JWT へ変換 |
| ユーザー（イベント文脈） | `event_access`（IdP 発行） | 検証＋内部 JWT へ変換 |
| セルフサインアップ | 登録専用の最小トークン（IdP 発行） | 検証＋内部 JWT へ変換 |
| サービス間の同期 RPC | ワークロード資格情報＋文脈トークン（後述「サービス間経路の検証と再発行」） | 辺ポリシー照合＋token_use=service の内部 JWT を再発行 |
| エッジ端末 | デバイストークン（観測が発行・検証） | 検証せずパススルー（対象は Observation の ReportMeasurements／Heartbeat のみ） |
| ゲスト | なし（未認証） | パススルー（対象は Guest Service のゲスト向け HTTP のみ） |

## 外部トークンの検証

- 署名は IdP の JWKS でローカル検証する。IdP の JWKS を参照するのは API Gateway のみ
- claim 検証: iss（IdP）、aud（API Gateway を指す登録 audience。例 `backend-api`）、exp／nbf（clock skew 許容 ±30 秒）、token_use
- メソッド単位の要求 token_use を認可表（後述）で強制する。scope の主判定は各サービスが行い（idp_design §16 踏襲）、API Gateway は scope を内部 JWT へ転記する
- 失効は introspection（RFC 7662。IdP の既存機能）で照会し、結果を jti 単位で短期キャッシュする（60 秒。失効反映遅れの上限）
- introspection 不達時は fail closed とする（トークンを有効扱いしない）

## サービス間経路の検証と再発行

サービス間の同期 RPC も API Gateway を経由する。この経路では外部トークンの代わりに、次の二つを検証して token_use=service の内部 JWT をホップごとに再発行する。
役割分離の原則: 呼び出し元の識別はワークロード資格情報が担い、内部 JWT はユーザー文脈の運搬と宛先束縛に使う（トークンの提示はワークロードの認証にならない）。呼び出し可否は辺ポリシーが決める。

### ワークロード資格情報の検証

- 形式は「audience 束縛のワークロード JWT（aud = API Gateway）を JWKS で検証」に固定し、デプロイ段階で発行者のみ差し替える
  Docker Compose 期: 各サービスが自身の鍵ペアで自己署名する短命 JWT（iss = サービス名）。API Gateway の静的 JWKS 構成（サービス名と公開鍵の対応表）で検証し、鍵は compose の secrets で配布する
  Google Cloud 期: Cloud Run のメタデータサーバが発行する Google 署名の ID トークン（Service Account 単位。GKE の場合は Workload Identity）。Google（またはクラスタ）の JWKS をキャッシュして検証し、Service Account をワークロード名へ対応付ける
- 「呼び出し元ワークロード identity の検証」は一点の抽象に切り出し、差し替えを局所化する。増えるのはキャッシュのみでステートレス性を保つ
- IdP はサービスの identity に関与しない（サービスは IdP のクライアントにならない）。全サービス共通の共有シークレットは用いない
- ワークロード JWT は内部 JWT とは別形式のシンプルな JWT とする（OAuth のクライアント認証（RFC 7523 private_key_jwt）と同じ位置づけ。提示者の証明のみを担い、認可情報を運ばない）
  必須クレーム: iss（サービス名。Google Cloud 期は発行者固有の値）、sub（サービス識別。iss と同値でよい）、aud（API Gateway）、iat／exp（短命。数分以下）、ヘッダ kid。任意: jti（リプレイ検知を強める場合）
  token_use・scope・tenant_id 等、内部 JWT のクレーム体系は持たない
- 内部 JWT との混同防止: iss と署名鍵を内部 JWT と別にし、検証パスを分離する（aud も Gateway 宛と宛先サービス宛で異なるため、取り違えは構造的にも弾かれる）
- 運搬チャネルの分離: 文脈トークンは `authorization` メタデータ、ワークロード JWT は別のメタデータキー `workload-authorization` で送る

### 文脈トークンの検証（ユーザー起点の場合）

- 文脈トークンは、呼び出し元サービスが処理中のリクエストで受領した入口内部 JWT であり、ユーザー文脈の証拠として提示される
- 提示者 = aud の突合: 文脈トークンを提示できるのは、その aud に指名されたワークロードだけである（ワークロード資格情報で識別した呼び出し元と突合する）。これにより漏えいトークンでの成りすましはワークロード自体の侵害に帰着する
- 自身の署名鍵で検証し、提示時点で有効（exp 内）であることを確認する

### 辺ポリシーの照合

- 辺ポリシーは、許可するサービス間呼び出しの静的な一覧（重要構成としてコードと同期管理し、実装用の宣言的設定と CI で突合する）
- 辺の形式: ユーザー起点は「(呼び出し元ワークロード, 文脈トークンの token_use と aud) → 宛先メソッド」、マシン起点（定期処理等。文脈トークンなし）は「呼び出し元ワークロード → 宛先メソッド」
- 辺が許可一覧になければ permission_denied。token_use=service の発行根拠はこの辺ポリシーの照合であり、ユーザーの権限からは導出しない（例: RegisterTenant 直後の AddTenantMember は、ユーザーが owner になる前のブートストラップ操作のため、辺「(tenant-management, registration, aud=tolo-tenant-management) → RelationAdminService.AddTenantMember」の許可が認可の全てである）

### 再発行する内部 JWT

- token_use=service、aud=宛先サービス、sub=呼び出し元サービスの client_id、src_jti=文脈トークンの jti、origin_sub=文脈トークンの sub（ユーザー起点の場合）、txn=透過
- TTL は独立の 120 秒とし、入口トークンの exp で cap しない（認可根拠が辺ポリシーにあり、ユーザー文脈は監査専用のため）

## 内部 JWT

外部トークンの検証成功後、以下の内部 JWT を発行する
scope は外部トークンの値を転記し、拡大も暗黙の縮小もしない（idp_design §11 の方針を踏襲）

### 共通必須クレーム

| claim | 内容 |
|---|---|
| iss | API Gateway の発行者識別子（IdP の iss と別値にし、取り違えを防ぐ） |
| sub | ユーザー系は user_id、サービス系は呼び出し元サービスの client_id |
| aud | 宛先マイクロサービスの論理識別子（宛先サービス単位。1 token 1 audience を踏襲し、サービス間のトークン転用を防ぐ） |
| token_use | 下表の4種別 |
| scope | 外部トークンの scope の転記 |
| client_id | 外部トークンを提示した client（BFF、スタッフアプリ、サービス等） |
| src_jti | 変換元トークンの jti（入口変換では外部トークン、サービス間再発行では文脈トークン。監査相関用） |
| iat／nbf／exp | TTL は 120 秒。入口変換の exp は「発行時刻＋120 秒」と「元トークンの exp」の小さい方。サービス間再発行は独立の 120 秒 |
| jti | 内部 JWT 自体の識別子（サービス側監査ログ用） |

### token_use 別の追加クレーム

| token_use | 由来する外部トークン | 追加必須 claim |
|---|---|---|
| tenant_access | `tenant_access` | tenant_id |
| event_access | `event_access` | tenant_id、event_id |
| service | （外部トークンなし）辺ポリシーに基づく再発行（前述「サービス間経路の検証と再発行」） | origin_sub／txn（ユーザー起点の場合。監査専用） |
| registration | 登録専用最小トークン | なし（tenant_id を持たない。scope は `tenant.register` のみで RegisterTenant 呼び出しに限定） |

- origin_sub: 処理の起点となったユーザーの user_id。監査専用であり、認可判定に使ってはならない
- txn: 入口変換時に採番し、同一処理チェーンの全ホップへ透過するトランザクション ID

### 含めないクレーム

| claim | 理由 |
|---|---|
| role／tenant_role／event_role | scope 発行判定にのみ使用し、JWT claim に載せない方針のため |
| resource | 外部トークン向けの表現。サービス側は tenant_id／event_id と RPC で判定できる |

### TTL と再利用

- TTL 120 秒。検証はサービス入口の一度きりで、後段のサービス間呼び出しは API Gateway が辺ポリシーに基づきホップごとに再発行するため、処理時間の長さは TTL を要求しない
- TTL が規定するのはリプレイの窓と、鍵漏えい時の残存トークンの寿命
- （src_jti、aud）単位で発行済み内部 JWT を exp まで再利用してよい（署名コストの削減）
- 内部 JWT は denylist を持たない。失効は TTL での自然失効に委ねる

### 署名鍵

- 方式: ES256、kid 必須。API Gateway 専用鍵とし、IdP の鍵と共有しない
- 保管: 秘密鍵は KMS／Secret Manager に置き、API Gateway のみがアクセスする
- 配布: API Gateway が内部向け JWKS エンドポイントで公開鍵を公開する。各サービスは JWKS をキャッシュし（TTL 5 分）、未知の kid を受けたら即時再取得する（再取得にはレート制限を付ける）
- 定期ローテーション（30 日周期。自動化）: (1) 新鍵を JWKS へ追加（署名は旧鍵のまま）→ (2) JWKS キャッシュ TTL＋余裕（15 分）経過後に新鍵で署名開始 → (3) 「内部 JWT TTL＋キャッシュ TTL」経過後に旧鍵を JWKS から削除
- 緊急ローテーション（漏えい時）: 新鍵で即時署名開始＋漏えい鍵を JWKS から即時削除。各サービスは未知 kid の再取得で自動追従し、被害の窓は「JWKS キャッシュ TTL＋内部 JWT TTL」で規定される

## メソッド認可表

各サービス仕様の認可欄からの導出（各仕様が正。食い違う場合は各仕様に従う）

### token_use = event_access を要求（Token Exchange を経たトークンが必要）

| サービス | RPC | scope |
|---|---|---|
| Graph Authoring | CreateGraph、AddPoint／UpdatePoint／RemovePoint、AddRoute／UpdateRoute／RemoveRoute、MapObservationPoint、AddQrLocation／UpdateQrLocation／RemoveQrLocation、PublishRevision | events.write |
| Graph Authoring | GetGraph | events.read |
| Observation | RegisterEdgeDevice、OperateGate、UpdateObservationPointConfig、ToggleDangerFlag、RegisterScheduleEvent、ReportCongestion、CorrectQueue | events.write |
| Observation | ListEdgeDevices、GetEventOverview | events.read |
| Operation | SendStaffMessage、RevokeStaffMessage、ShareGuidanceInfo、DirectReassignment、ApplyReassignment | events.write |
| Operation | ListStaffMessages、ListReassignments、GetHistoryDigest、UpdateConnectionState | events.read |
| Realtime | FetchDeliveries | events.read |

### token_use = tenant_access を要求

| サービス | RPC | scope |
|---|---|---|
| Tenant Management | ChangeTenantContract、ArchiveTenant | tenant.write |
| Tenant Management | CreateEvent、AssignEventType、TransitionEventStatus | events.write |
| Tenant Management | ListEvents | events.read |
| Relation | AddTenantMember、ChangeTenantRole、GrantEventRole、RevokeRole | tenant.write |
| Relation | ListMemberships | tenant.read |
| Notification | RegisterDeviceToken、UnregisterDeviceToken | tenant.read（自デバイス限定） |

### token_use = service を要求（サービス間）

Flow.Optimize、Line.GuideQueues、Tenant.GetEvent、Graph 供給系（GetCurrentRevision／GetObservationPointMappings／GetGatePoints／GetDisplayNames／GetQrLocations）、Observation の GetGuestSnapshot、Operation の RequestProposalDelivery／RecordFeedbackValues／ListStaffMessages（Guest Service の復旧時）、Notification.SendPush、Reference Aggregation の SubmitAnonymizedFeedback／GetReferenceValues、Relation.AddTenantMember（Tenant Management 由来）、Observation.ReportMeasurements（Guest Service 由来）

### token_use = registration を要求

Tenant.RegisterTenant（scope は `tenant.register`）

### パススルー

Observation の ReportMeasurements／Heartbeat（エッジ端末デバイストークン）、Guest Service のゲスト向け HTTP（未認証）

## サービス側の検証規約

- API Gateway の JWKS で署名を検証し、iss・aud・exp・nbf を確認する（clock skew 許容 ±30 秒）
- tenant_id／event_id claim とリクエスト対象の一致を確認する（保護境界の強制）
  claim もリクエストの識別子も公開 ID のため、値をそのまま突合する
- 主判定は scope とし、write 系は現在の membership／permission を DB で再確認する（idp_design §16 を踏襲）

## エラー方針

外部レスポンスに tenant／event／membership／失効の詳細を漏らさない（idp_design §18 と同方針）

| 事象 | Connect エラーコード |
|---|---|
| トークンなし、無効、期限切れ、失効済み、token_use 不一致 | unauthenticated |
| token_use は満たすが認可表で当該メソッドに許可されない | permission_denied |
| introspection 不達（fail closed） | unauthenticated |
| 宛先サービス不達 | unavailable（透過） |

## 監査ログ

- 記録項目: timestamp、request_id、メソッド（サービス＋RPC）、client_id、sub、token_use、src_jti、txn、発行した内部 JWT の jti、result、failure_reason、source_ip
- サービス間再発行では、呼び出し元ワークロード、照合した辺、origin_sub の有無も記録する
- 外部トークン本体・内部 JWT 本体をログへ出してはならない（idp_design §19.2 と同方針）
- request_id は宛先サービスへ伝播し、IdP 監査ログ（src_jti）・サービス側ログと突き合わせ可能にする

## 未確定事項

- 実装形態の製品選定（Envoy＋ext_authz、自前 Connect プロキシ等）
- 鍵保管（KMS／Secret Manager）の製品選定と内部 JWKS エンドポイントの公開範囲の実装詳細
- introspection キャッシュ TTL（60 秒）と失効反映遅れの許容値の最終確認
