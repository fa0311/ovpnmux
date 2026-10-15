# ovpnmux

複数の OpenVPN トンネルを同時に張り、1 つの SOCKS5 / HTTP プロキシとして出し分ける常駐サーバーです。
Docker で動かす前提で、必要な権限は `NET_ADMIN` と `/dev/net/tun` だけです。

- `profiles/` に置いた `.ovpn` を候補として、設定した本数のトンネルを常に張っておく
- 接続ごとに username で出口を選ぶ (国、トンネル番号、プロファイル名)
- トンネルごとに routing table と fwmark を割り当て、プロキシは `SO_MARK` を付けて dial する。
  listen ポートはトンネル本数に関係なく 1 つ
- 落ちたトンネルは別のプロファイルで自動的に張り直す

## 使い方 (docker compose)

```sh
git clone https://github.com/fa0311/ovpnmux.git && cd ovpnmux
cp .env.example .env              # LAN_IP, OVPNMUX_PASSWORD
cp config.example.yaml config.yaml
cp /path/to/*.ovpn profiles/      # 使いたいプロファイルを置く
docker compose up -d
curl -s localhost:9090/status     # トンネルの状態
```

## 出口の選び方

SOCKS5 / HTTP の **username** で出口を選びます。password は `auth.password` です。

| username | 出口 |
|---|---|
| `jp`, `us`, ... | その国のトンネル (`# tags: country=XX` か、接続後に調べた出口 IP の国) |
| `any` または空 | どれでも |
| `tunnel:2` | トンネル 2 |
| `name:tokyo-1` | そのプロファイルを使っているトンネル |

```sh
curl -x socks5h://jp:PASSWORD@192.168.1.10:1080 https://ifconfig.me
curl -x http://any:PASSWORD@192.168.1.10:1080 https://ifconfig.me
```

同じクライアント IP と username の組は `selection.sticky_ttl` の間、同じトンネルに固定されます (`selection.default: rr` で無効)。
username を送れないクライアント向けには `extra_listeners` で出口固定のポートを足せます。

## プロファイル

`profiles/` 以下の `*.ovpn` がすべて候補になります (サブディレクトリも可)。先頭にタグを書くと国指定で選べ、`score` が大きいものから優先して使われます。

```
# tags: country=JP name=tokyo-1 score=100
client
dev tun
proto udp
remote vpn.example.com 1194
<ca>
...
</ca>
```

- 証明書や鍵、`auth-user-pass` はインラインで書いてください。別ファイル参照は読めません
- `up` / `down` / `plugin` / `script-security` / `management` などプログラムを実行する指示を含むプロファイルは読み込まれません
- `dev` / `redirect-gateway` / `route` などルーティングに関わる指示は無視され、ovpnmux が自分で設定します

## 設定 (`config.yaml`)

| キー | 意味 |
|---|---|
| `pool.size` | 同時に張るトンネル本数 |
| `pool.per_country` | 国ごとに確保する本数 (例 `{ JP: 2 }`) |
| `selection.default` | `sticky` か `rr` |
| `extra_listeners` | 出口固定の追加ポート |
| `health.interval` | ヘルスチェック間隔。3 回連続で失敗すると張り替え |
| `health.cooldown` | 失敗したプロファイルを再利用しない時間 |
| `health.geo_url` | 出口 IP と国を調べる URL。空にすると無効 |

## 管理 API (`127.0.0.1:9090`)

- `GET /status` トンネル一覧 (状態、国、出口 IP、レイテンシ、接続数、バイト数)
- `GET /healthz` ready なトンネルが 1 本以上あれば 200
- `POST /tunnels/{id}/reconnect` そのトンネルを切って別のプロファイルに張り替える

## 仕組み

1. openvpn を `--ifconfig-noexec --route-noexec` で起動し、ホストのルーティングは触らせない
2. `--up` で呼ばれた `ovpnmux _up` が tun のアドレス・gateway・DNS を JSON で渡す
3. デーモンが `ip addr` / `ip route ... table N` / `ip rule add fwmark N lookup N` を設定する。table には `unreachable` のフォールバックがあり、トンネルが落ちても main に漏れない
4. プロキシは選んだトンネルの fwmark を `SO_MARK` に付けて dial する。DNS も同じ経路で引く
5. 定期的にヘルスチェックし、失敗が続くか openvpn が終了したら別のプロファイルで張り直す

## 開発

```sh
go test ./...
go build -o bin/ ./cmd/...
./bin/ovpnmux list -dir profiles
```

実トンネルの動作確認には `NET_ADMIN` と `/dev/net/tun` が要るので、Docker ホストで `docker compose up` して確認します。
`main` への push で GitHub Actions がテストを実行し、`ghcr.io/fa0311/ovpnmux:main` (amd64 / arm64) を公開します。

## 注意

- プロキシを認証なしで公開しないでください。compose の `ports:` は LAN の IP に限定しています
- 出口 IP と国の確認に `ip-api.com` を使います (`health.geo_url` を空にすれば無効)
