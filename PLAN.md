# vpngate-proxy 設計プラン

作成日: 2026-10-15 (同日中に 3 回改訂: 自宅 Docker 前提 / Go 採用 / VPN Gate 非依存コアに分離)

## ゴール

**コアは「OpenVPN プロファイル群 → SOCKS5 / HTTP プロキシ」の汎用デーモン。VPN Gate のことは一切知らない。**

- `profiles/*.ovpn` にあるプロファイルを使って複数本の OpenVPN トンネルを同時に張り、
  1 ポートの SOCKS5 / HTTP CONNECT プロキシとして LAN に提供する
- 接続ごとに「国指定 / ラウンドロビン / 最小レイテンシ / スティッキー」で出口トンネルを選ぶ
- 落ちたトンネルは自動で別プロファイルに張り替える
- 状態確認・手動操作用の管理 API を持つ
- **VPN Gate の CSV → .ovpn 変換は別の小さなツール (feeder)** が担当し、プロファイルディレクトリに
  ファイルを書くだけ。デーモンとの結合点は「ディレクトリ内の .ovpn ファイル」のみ。
  Mullvad / Proton などの .ovpn を置いても同じように動く
- 登大遊氏運営の `public-vpn-NNN.opengw.net` は DDNS 名で静的に書けるので、feeder 無しでも運用できる

## 結合度についての整理

| 層 | VPN Gate 依存 | 中身 |
|---|---|---|
| デーモン (`vpngate-proxy serve`) | **無し** | .ovpn を読む、openvpn を起動、ポリシールーティング、SOCKS5/HTTP、選択、ヘルスチェック、admin |
| プロファイルの付加情報 | 無し | `.ovpn` 先頭のコメント (`# tags: country=JP name=public-vpn-251`) または同名 `.json`。無ければ接続後に出口 IP を GeoIP で引いて自動付与 |
| feeder (`vpngate-feeder` 別バイナリ or `tools/` のスクリプト) | **有り** | API の CSV を取得 → フィルタ → `remote` を DDNS 名に書き換え → `profiles/vpngate/*.ovpn` に書く。古いものは消す |
| 同梱テンプレート `profiles/examples/public-vpn-*.ovpn` | 弱 (DDNS 名のみ) | CA 証明書は全台共通なので 1 テンプレートを複製して `remote` だけ変える |

CSV フォーマットが変わっても壊れるのは feeder だけで、デーモンは無傷。feeder は 100 行程度の独立ツールにする。

## 調査結果 (2026-10-15 時点)

### VPN Gate API

- `http://www.vpngate.net/api/iphone/` は CSV を返す (98 件、1.3 MB、全件 OpenVPN 設定 base64 付き)
- 形式: 1 行目 `*vpn_servers`、2 行目 `#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,NumVpnSessions,Uptime,TotalUsers,TotalTraffic,LogType,Operator,Message,OpenVPN_ConfigData_Base64`、末尾に `*` 行
- OpenVPN 設定の中身 (98 件すべて共通の傾向)
  - `dev tun` / `client` / `proto tcp` 82 件・`udp` 16 件 / ポートは 443・995 が主、他はランダム
  - `cipher AES-128-CBC` / `data-ciphers AES-128-CBC` / `auth SHA1`
  - `<ca>` `<cert>` `<key>` がインライン。ユーザー名・パスワードは不要
  - `remote` は IP 直書き (DDNS 名ではない)
- 国別件数: JP 42, KR 32, RU 8, TH 8, US 3, 他 (MY, BR, AU, VN, MM) 1 件ずつ

### 登大遊氏運営サーバー (`public-vpn-NNN.opengw.net`) の安定性

- API と公式サイトの双方に同じ 18 台が載っている。全て `219.100.37.0/24` (SoftEther Telecommunication Research Institute / 筑波大)
- `public-vpn-NNN.opengw.net` の DNS 解決結果は CSV の IP と全て一致 (5 台確認)。DDNS 名を `remote` に書けば IP が変わっても追従できる
- 18 台とも **同一の CA 証明書** を使っている → `.ovpn` は 1 テンプレートで済み、`remote` 行だけ差し替えればよい
- ポートは全台 TCP 443、うち 7 台は UDP 1195 も開いている。L2TP/IPsec と SSTP も有り
- Uptime は全台 12 日で揃っている (一斉再起動されている)。累計ユーザー数は 130 万〜2,690 万で、同じ名前が年単位で生きている
- 「NNN と IP の対応が将来も変わらない」ことは 1 回の観測では確認できない。公式サイトも「リストの IP が間違っていることがある」「一覧は一部のみ」と注記
- ボランティア運営の `vpnNNNNNNNNN.opengw.net` も名前自体はインストール単位で固定だが、背後は家庭回線の動的 IP で稼働率が低い。こちらは feeder 前提

### 実際に handshake して分かったこと (この sandbox で openvpn 2.6.19 を使い `--dev null --dev-type tun` で接続)

- TLS 1.3 で接続でき、データチャネルは `AES-128-CBC` / `SHA1` でネゴシエートされる (DCO は非対応なので無効化される)
- **サーバーが push してくる内容** (登氏サーバーとボランティアサーバーで同じ形式):
  ```
  PUSH_REPLY,ping 3,ping-restart 10,
    ifconfig 10.246.24.185 10.246.24.186,        # p2p (net30 風): local と local+1
    dhcp-option DNS 10.246.254.254,dhcp-option DNS 8.8.8.8,
    route-gateway 10.246.24.186,redirect-gateway def1
  ```
  - 払い出し範囲はサーバーごとに違う (`10.246.0.0/16`, `10.211.0.0/16` など)。同じ範囲の可能性もあるので /32 + ポリシールーティングで吸収する
  - `redirect-gateway def1` が来るので `--route-noexec` (または `--pull-filter ignore "redirect-gateway"`) 必須
  - `ping 3 / ping-restart 10` とかなり短い。再接続判定はサーバー任せで十分
- **`--dev null` だけだと SoftEther は L2 扱いにして ifconfig を push しない** (`dev-type` で L3/L2 を判定している)。
  TUN 無しでハンドシェイクだけ試す時は `--dev null --dev-type tun` を使う。これは単体テスト / CI でも使える
- SoftEther は IPC (仮想 HUB への接続 + DHCP) が終わるまで PUSH_REPLY を返さず、終わったら 1 回だけ返す。
  クライアント側は普通の OpenVPN で何も特別なことは要らない

### 実行環境

- **本番: 自宅サーバーの Docker** (docker compose で 1 サービス)
- この sandbox: `/dev/net/tun` なし、`CAP_NET_ADMIN` なし、`docker` なし。`openvpn` と `iproute2` は apt で入れた
- → ここでできるのは: ユニットテスト、`--dev null --dev-type tun` によるハンドシェイクのテスト、ルーティング設定生成のテスト。
  **実トンネルの結合テストとイメージのビルドは自宅サーバー側**。コードは git 経由で渡す。
  自宅サーバーに SSH できるなら PoC と結合テストをこちらから回せる

### Docker で使える権限と設計への影響

| やりたいこと | 必要な権限 | 備考 |
|---|---|---|
| tun デバイスを開く (openvpn) | `--device /dev/net/tun` + `CAP_NET_ADMIN` | gluetun 等の VPN コンテナと同じ |
| ルーティングテーブル / `ip rule` の操作 | `CAP_NET_ADMIN` | コンテナ自身の netns 内なら自由 |
| `SO_MARK` 付きソケット | `CAP_NET_ADMIN` (新しめのカーネルは `NET_RAW` でも可) | |
| `rp_filter` 等の sysctl | compose の `sysctls:` で起動時に指定 | 実行中の `/proc/sys` は read-only |
| **トンネルごとに netns を作る** (`unshare(CLONE_NEWNET)` / `setns`) | **`CAP_SYS_ADMIN`** + seccomp/AppArmor 緩和 | ほぼ host root 相当。相手はボランティアのサーバーで信用できないので避ける |

→ netns 分離案は不採用。**1 コンテナ・1 netns の中で fwmark ポリシールーティングによりトンネルを選ぶ**。

## アーキテクチャ

```
docker compose: vpngate-proxy (cap_add NET_ADMIN, /dev/net/tun)  ← コンテナ自身の netns
┌──────────────────────────────────────────────────────────────────────────────┐
│ vpngate-proxy serve (Go, 1 プロセス)                                          │
│  ├ profiles       : profiles/**/*.ovpn を読み、fsnotify で変更を追う            │
│  ├ tunnel manager : openvpn 子プロセス x N を監視・張り替え                       │
│  │                  tunN ごとに routing table N + `ip rule fwmark N → table N`  │
│  ├ proxy (:1080)  : 接続ごとに出口を選び、SO_MARK=N を付けて dial               │
│  └ admin (:9090)  : /status /reload /metrics /healthz                         │
│                                                                              │
│  tun1 (table 1: default via gw dev tun1 / fallback unreachable) ── openvpn#1 ─┐│
│  tun2 (table 2: ...)                                           ── openvpn#2 ─┤│
│  eth0 (main table: default via docker bridge) ◄──────────────────────────────┘│
└──────────────────────────────────────────────────────────────────────────────┘
     ▲ profiles/ (volume)                       │ openvpn の制御/データ通信は eth0 → 自宅ルーター → VPN サーバー
     │ .ovpn を置くだけ
  vpngate-feeder (別コンテナ or cron。無くても動く)
```

### 要点

1. **openvpn は `--ifconfig-noexec --route-noexec --pull-filter ignore "redirect-gateway"`** で起動し、
   main のルーティングには一切触らせない。up 通知を受けたデーモンが tunN に以下を設定する:
   - `ip addr add $ifconfig_local peer $ifconfig_remote/32 dev tunN` (p2p) または `/32 noprefixroute` (subnet)
   - `ip route add default via $route_vpn_gateway dev tunN onlink table N`
   - `ip route add unreachable default metric 1000 table N` (トンネルが落ちたら main に抜けず失敗させる = リーク防止)
   - `ip rule add fwmark N lookup N`
2. **プロキシは per-tunnel の SOCKS サーバーを立てず、ポートも 1 つしか使わない。** 接続を受けたら出口トンネル N を選び、
   `net.Dialer.Control` で `SO_MARK=N` を付けて dial する。カーネルが table N を引いて tunN から出し、
   送信元 IP も tunN のアドレスになる
3. **DNS** は同じ SO_MARK 付きで push された DNS (無ければ 1.1.1.1 / 8.8.8.8) に dial する独自 resolver で引く。
   Docker 内蔵 DNS (127.0.0.11) は通さない。IPv6 は使わない
4. **sysctl**: `net.ipv4.conf.all.rp_filter=0` と `net.ipv4.conf.default.rp_filter=0` を compose の `sysctls:` で指定
5. **出口の選び方**: SOCKS5 / HTTP の username を選択キーにする (`jp`, `kr`, `any`, `tunnel:3`, `name:public-vpn-251`)。
   password は設定した共通シークレット。未指定時は設定のデフォルト戦略。
   username を使えないクライアント向けに、任意で **国別の追加 listen ポート** (`:1081 → JP` など) も設定できるようにする

### 代替案 (不採用理由つき)

| 案 | 内容 | 判断 |
|---|---|---|
| netns 分離 | トンネルごとに netns を作り tun を移動、proxy は setns して dial | 隔離は最強だが Docker では `CAP_SYS_ADMIN` が要る。VPS に直置きするなら有力 |
| compose で N+1 コンテナ | tunnel コンテナ (openvpn + 小さな socks) を `--scale` で N 個、frontend が国で振り分け | 権限は NET_ADMIN だけで済み Docker が隔離してくれる。ポリシールーティングが push 設定と相性悪かった場合の **フォールバック** |
| 既存ツール合成 | openvpn + microsocks + glider (mixed listener, rr/ha/lha, ヘルスチェック内蔵) を bash で束ねる | 最速で動くが国選択・自動張り替え・候補管理をシェルで書くことになり脆い。**PoC (マイルストーン 0) ではこの構成を使う** |
| 完全ユーザー空間 | minivpn + gVisor netstack。root/TUN 不要 | OpenVPN 実装が研究用途・rekey 未実装。将来の選択肢 |

## Go スタック (モダン寄り、依存は惜しまない)

| 用途 | 採用 |
|---|---|
| Go | 1.25 系 (`go.mod` の `toolchain` 指定)。`log/slog`、`net/netip`、`context`、range-over-func、`iter` を普通に使う |
| CLI | `spf13/cobra` (`serve` / `list` / `up` / `_up` 内部用) |
| 設定 | `knadh/koanf` (YAML + 環境変数上書き) |
| netlink / ルーティング | `vishvananda/netlink` (`ip` コマンドに依存しない) |
| SO_MARK dialer | 標準 `net.Dialer.Control` + `golang.org/x/sys/unix` |
| SOCKS5 / HTTP | 自前実装 (RFC 1928 CONNECT + HTTP CONNECT を 1 ポートで判別)。`things-go/go-socks5` は dialer 差し替えが弱いので参考に留める |
| プロファイル監視 | `fsnotify/fsnotify` |
| 並行処理 | `golang.org/x/sync/errgroup`、`singleflight` |
| メトリクス | `prometheus/client_golang` |
| admin API | `net/http` + `go-chi/chi` v5 |
| GeoIP | `oschwald/geoip2-golang` + GeoLite2 (任意) か、出口 IP を `ip-api.com` 等で引く (デフォルト) |
| テスト | `testing` + `stretchr/testify`、`--dev null --dev-type tun` で実サーバーとの handshake テスト (ネットワーク有りの時だけ) |
| 品質 | `golangci-lint` v2、`gofumpt`、`govulncheck`、GitHub Actions |
| 配布 | multi-stage Dockerfile (`golang:1.25` → `debian:bookworm-slim` + `openvpn iproute2 ca-certificates`)、`linux/amd64` + `linux/arm64`、`goreleaser` は任意 |

## コンポーネント詳細

### 1. profiles (`internal/profile`)

- `profiles/**/*.ovpn` を読む。`Profile {id(パスから), name, tags{country,...}, remote(host,port,proto), ovpn 本文, source(dir)}`
- 付加情報: 先頭コメント `# tags: country=JP name=public-vpn-251 score=3137099` を解釈。無ければ空
- fsnotify で追加・削除・変更を検知し、tunnel manager に差分を通知 (使用中のプロファイルが消えたらそのトンネルを drain)
- `.ovpn` の危険なディレクティブ (`up`, `down`, `plugin`, `script-security`, `route-up`, `tls-verify`, `management` 等) は **拒否**して読み込まない
  (feeder が書くファイルは外部由来なので)

### 2. tunnel manager (`internal/tunnel`)

- `Tunnel {id N, dev tunN, mark N, table N, openvpn *exec.Cmd, profile, state(connecting/ready/degraded/draining/dead), stats}`
- 起動手順
  1. プロファイルに以下を付加した設定を一時ディレクトリに書く:
     `dev tunN` / `dev-type tun` / `ifconfig-noexec` / `route-noexec` / `pull-filter ignore "redirect-gateway"` /
     `up <自分自身> _up` / `script-security 2` / `connect-timeout 15` / `connect-retry-max 1` / `auth-nocache` / `verb 3`
  2. openvpn を子プロセスとして起動し stdout を slog に流す
  3. `_up` サブコマンド (環境変数 `dev ifconfig_local ifconfig_remote ifconfig_netmask route_vpn_gateway tun_mtu foreign_option_*`)
     が unix socket でデーモンに JSON を送る
  4. デーモンが netlink でアドレス、table N、unreachable fallback、`ip rule` を設定。push された DNS を Tunnel に記録
  5. ヘルスチェックへ
- ヘルスチェック: SO_MARK=N の dialer で `http://www.gstatic.com/generate_204` を GET、出口 IP と国を取得 (国タグが無ければここで付与)。
  以後 30 秒ごと。連続 3 回失敗 / openvpn 終了 / 一定時間ゼロトラフィックで dead にし、別プロファイルで張り直す
- プール制御: 目標本数 (全体・国別)、同時接続試行数の上限、起動時は候補を並列に試して早く埋める
- 自前スコア: プロファイルごとの成功率・実測レイテンシを `data/history.json` に永続化。失敗したものは指数バックオフでクールダウン
- 後始末: SIGTERM → openvpn 終了 → rule / table N を削除。起動時に残骸の rule / table を掃除

### 3. proxy (`internal/proxy`)

- SOCKS5 (RFC 1928; まず CONNECT のみ。UDP ASSOCIATE は後回し) と HTTP CONNECT / plain HTTP forward を
  同一ポートで自動判別 (先頭 1 バイトが 0x05 なら SOCKS5)
- 認証: username = 選択キー、password = 共通シークレット (設定で必須化できる)。bind 先は LAN に限定
- Selector: 戦略 (round-robin / least-latency / sticky by client IP + TTL) と制約 (国、tunnel id、name)。
  該当トンネルが無ければ SOCKS5 reply 0x01 / HTTP 503
- Dialer: `net.Dialer{Control: setsockopt(SO_MARK, N)}` で `DialContext`。resolver も同じ Control。IPv4 のみ
- 統計: トンネルごとの接続数・バイト数・エラー数

### 4. admin (`internal/admin`)

- `127.0.0.1:9090`
- `GET /status` (トンネル一覧: 国、プロファイル名、出口 IP、レイテンシ、稼働時間、接続数)
- `POST /reload` (profiles 再読込)、`POST /tunnels/{id}/reconnect`、`GET /metrics`、`GET /healthz`

### 5. feeder (`cmd/vpngate-feeder`、VPN Gate 依存はここだけ)

- API の CSV を取得 (メイン + ミラー URL、失敗時は前回結果を保持)、`*` 行と `#` ヘッダを処理
- フィルタ: 国 allow/deny、Score / Speed / Ping のしきい値、NumVpnSessions 上限、proto 優先
- `remote` の IP を `HostName.opengw.net` に書き換え (オプション)、先頭に `# tags:` コメントを付けて
  `profiles/vpngate/<HostName>.ovpn` に書く。リストから消えたものは削除
- 実行間隔 10〜15 分。デーモンとは別コンテナ (同じ volume を共有) か、ホストの cron

### 6. 設定 (`config.yaml`)

```yaml
listen: 0.0.0.0:1080           # compose 側で LAN IP に限定して公開
admin_listen: 0.0.0.0:9090
auth:
  required: true
  password: "change-me"
profiles_dir: /profiles
pool:
  size: 6
  per_country: { JP: 3, KR: 2 }
selection:
  default: sticky              # rr | lha | sticky
  sticky_ttl: 10m
extra_listeners:               # 任意: username を使えないクライアント向け
  - { listen: 0.0.0.0:1081, select: "jp" }
routing:
  table_base: 100              # tunN → table 100+N, fwmark 100+N
  fallback_dns: [1.1.1.1, 8.8.8.8]
openvpn_bin: /usr/sbin/openvpn
data_dir: /var/lib/vpngate-proxy
```

## Docker デプロイ (自宅サーバー)

```yaml
# docker-compose.yml
services:
  proxy:
    build: .
    cap_add: [NET_ADMIN]
    devices: ["/dev/net/tun:/dev/net/tun"]
    sysctls:
      net.ipv4.conf.all.rp_filter: "0"
      net.ipv4.conf.default.rp_filter: "0"
      net.ipv6.conf.all.disable_ipv6: "1"
    ports:
      - "192.168.1.10:1080:1080"   # LAN の自宅サーバー IP に限定して公開
      - "127.0.0.1:9090:9090"      # admin はホストのローカルのみ
    volumes:
      - ./config.yaml:/etc/vpngate-proxy/config.yaml:ro
      - profiles:/profiles
      - data:/var/lib/vpngate-proxy
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:9090/healthz"]
      interval: 30s
  feeder:                          # 任意。無ければ profiles/ に手で .ovpn を置く
    build: .
    command: ["vpngate-feeder", "--out", "/profiles/vpngate", "--interval", "10m", "--countries", "JP,KR,US"]
    volumes: ["profiles:/profiles"]
    restart: unless-stopped
volumes:
  profiles: {}
  data: {}
```

- イメージは `linux/amd64` と `linux/arm64` の両方をビルドできるようにする
- 自宅ルーター側は何も開けなくてよい (openvpn は outbound のみ)
- `privileged: true` は使わない。`NET_ADMIN` + `/dev/net/tun` + `sysctls` だけで動くことを設計の制約にする

## リポジトリ構成 (予定)

```
cmd/vpngate-proxy/             # serve / list / up / _up
cmd/vpngate-feeder/            # VPN Gate CSV → profiles/*.ovpn (ここだけ VPN Gate 依存)
internal/profile/              # .ovpn 読み込み、tags、fsnotify、危険ディレクティブ拒否
internal/tunnel/               # openvpn プロセス、_up 通知、ヘルスチェック、プール、スコア
internal/routing/              # netlink: アドレス付与、table/rule、SO_MARK dialer/resolver
internal/proxy/                # socks5, http, selector
internal/admin/                # status API, metrics
internal/vpngate/              # CSV パース (feeder からのみ使う)
profiles/examples/             # public-vpn-NNN.opengw.net 用テンプレート
scripts/poc/                   # マイルストーン 0 の bash PoC (docker run で実行)
Dockerfile  docker-compose.yml  config.example.yaml  .golangci.yml  .github/workflows/ci.yml
```

## マイルストーン

0. **自宅 Docker で PoC (bash)** — `docker run --cap-add NET_ADMIN --device /dev/net/tun --sysctl ... ubuntu` の中で
   openvpn 2 本を `--route-noexec` で張り、`ip rule` / table / `SO_MARK` (iptables の owner マッチか `curl --interface` で代用) で
   それぞれから `curl ifconfig.me` が別の出口 IP を返すことを確認。技術的な肝はここなので最初に潰す
1. **profile + feeder + `vpngate-proxy list`** — .ovpn 読み込み、tags、危険ディレクティブ拒否、CSV → .ovpn。sandbox でテスト可能
2. **tunnel manager + routing + `vpngate-proxy up --country JP`** — 1 本張って SO_MARK dialer で疎通確認まで (自宅 Docker)
3. **proxy + admin** — mixed SOCKS5/HTTP、username 選択、sticky、`/status`
4. **運用品質** — 自動張り替え、バックオフ、永続スコア、Dockerfile (multi-arch)、compose、healthcheck、CI
5. **任意** — UDP ASSOCIATE、国別追加ポート、Web UI、ユーザー空間モード

## リスクと注意

- VPN Gate は筑波大の学術プロジェクトで、サーバーはボランティア運営、接続ログを取得すると明記している。
  API 取得間隔は 10 分以上、同時トンネル本数は控えめに (10 本以下)
- 82/98 件が `proto tcp` なので TCP-over-TCP によるスループット劣化が起きやすい。udp を優先する
- OpenVPN 2.6 で `cipher AES-128-CBC` は非推奨警告が出るが、`data-ciphers` が設定済みなので接続できる (handshake で確認済み)
- 認証なしで公開するとオープンプロキシになる。compose の `ports:` は LAN IP に限定し、認証をデフォルト必須にする
- ポリシールーティングの落とし穴: rp_filter、トンネル断時のリーク (unreachable fallback で防ぐ)、
  サーバーが push する払い出し IP の重複 (/32 で回避)。PoC で実測して潰す
- 相手サーバーは信用できない。openvpn を動かすコンテナには `NET_ADMIN` 以上を渡さず、.ovpn の危険ディレクティブは拒否する
- sandbox では結合テストもイメージビルドもできないので、ロジックはユニットテストで固め、openvpn/ルーティング部分は自宅 Docker で検証する
