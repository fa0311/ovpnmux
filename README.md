# ovpnmux

複数の OpenVPN トンネルを同時に張り、1 つの SOCKS5 / HTTP プロキシとして LAN に提供する常駐サーバーです。
自宅サーバーの Docker で動かす前提で、必要な権限は `NET_ADMIN` と `/dev/net/tun` だけです。

- 本体 (`ovpnmux`) は `profiles/**/*.ovpn` を読むだけで、VPN Gate のことは知りません。
  Mullvad や Proton の `.ovpn` を置いても同じように動きます
- `vpngate-feeder` は VPN Gate の公開 API を 10 分ごとに読み、`.ovpn` に変換して profiles に置く別プロセスです。無くても動きます
- `profiles/examples/` には筑波大 (登大遊氏) 運営の `public-vpn-NNN.opengw.net` 用プロファイルが入っています。DDNS 名なので静的に使えます
- トンネルごとに routing table と fwmark を割り当て、プロキシは接続ごとに `SO_MARK` を付けて dial します。
  listen ポートはトンネル本数に関係なく 1 つです

## 使い方 (docker compose)

```sh
cp .env.example .env            # LAN_IP, OVPNMUX_PASSWORD を書く
cp config.example.yaml config.yaml
docker compose up -d
curl -s localhost:9090/status   # トンネルの状態
```

初回起動時に `profiles/examples` が `profiles` volume にコピーされ、feeder が `profiles/vpngate/` に VPN Gate のサーバーを書き足します。

## 出口の選び方

SOCKS5 / HTTP の **username** で出口を選びます。password は `auth.password` です。

| username | 出口 |
|---|---|
| `jp`, `kr`, ... | その国のトンネル (`# tags: country=XX` か、接続後に調べた出口 IP の国) |
| `any` または空 | どれでも |
| `tunnel:2` | トンネル 2 |
| `name:public-vpn-251` | そのプロファイルを使っているトンネル |

```sh
curl -x socks5h://jp:PASSWORD@192.168.1.10:1080 https://ifconfig.me
curl -x http://kr:PASSWORD@192.168.1.10:1080 https://ifconfig.me
```

同じクライアント IP と key の組は `selection.sticky_ttl` の間、同じトンネルに固定されます (`selection.default: rr` で無効)。
username を送れないクライアント向けには `extra_listeners` で国固定のポートを足せます。

## 自前の .ovpn を使う

`profiles` volume に置くだけです。先頭にタグを書くと国指定で選べます。

```
# tags: country=JP name=my-server
client
dev tun
remote vpn.example.com 1194
...
```

`up` / `down` / `plugin` / `script-security` / `management` などプログラムを実行する指示を含むプロファイルは読み込まれません。

## 管理 API (`127.0.0.1:9090`)

- `GET /status` トンネル一覧 (状態、国、出口 IP、レイテンシ、接続数、バイト数)
- `GET /healthz` ready なトンネルが 1 本以上あれば 200
- `POST /tunnels/{id}/reconnect` そのトンネルを切って別のプロファイルに張り替える

## 仕組み

1. openvpn を `--ifconfig-noexec --route-noexec` で起動し、ホストのルーティングは触らせない
2. `--up` で呼ばれた `ovpnmux _up` が tun のアドレス・gateway・DNS を JSON で渡す
3. デーモンが `ip addr` / `ip route ... table N` / `ip rule add fwmark N lookup N` を設定する。table には `unreachable` のフォールバックがあり、トンネルが落ちても main に漏れない
4. プロキシは選んだトンネルの fwmark を `SO_MARK` に付けて dial する。DNS も同じ経路で引く
5. 30 秒ごとにヘルスチェックし、3 回連続で失敗するか openvpn が終了したら別のプロファイルで張り直す

## 開発

```sh
go test ./...
go build -o bin/ ./cmd/...
./bin/vpngate-feeder -out /tmp/profiles -interval 0 -countries JP   # VPN Gate から .ovpn を作る
./bin/ovpnmux list -dir /tmp/profiles
```

実トンネルの動作確認には `NET_ADMIN` と `/dev/net/tun` が要るので、Docker ホストで `docker compose up` して確認します。
`main` ブランチへの push で GitHub Actions がテストを実行し、`ghcr.io/fa0311/ovpnmux:main` (amd64 / arm64) を公開します。

## 注意

- VPN Gate は筑波大の学術プロジェクトで、サーバーはボランティア運営、接続ログを取ると明記されています。API の取得間隔は 10 分以上、トンネル本数は控えめに
- プロキシを認証なしで公開しないでください。compose の `ports:` は LAN の IP に限定しています
- 出口 IP と国の確認に `ip-api.com` を使います (`health.geo_url` を空にすれば無効)
