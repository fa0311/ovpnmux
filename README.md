# ovpnmux

`profiles/` に置いた `.ovpn` ごとに OpenVPN トンネルを 1 本ずつ張り、HTTP CONNECT プロキシの Basic 認証 username でどのトンネルから出るかを選ぶサーバーです。
Docker で動かす前提で、必要な権限は `NET_ADMIN` と `/dev/net/tun` だけです。

- `profiles/tokyo-1.ovpn` → username `tokyo-1` で繋ぐとそのトンネルから出る
- 落ちたトンネルは自動で張り直す
- トンネルごとに routing table と fwmark を割り当て、プロキシは `SO_MARK` を付けて dial する。
  listen ポートはトンネル本数に関係なく 1 つ

## 使い方

```sh
git clone https://github.com/fa0311/ovpnmux.git && cd ovpnmux
cp config.example.yaml config.yaml   # password を変える
cp /path/to/*.ovpn profiles/
docker compose up -d
curl -s localhost:9090/status
curl -x http://tokyo-1:PASSWORD@SERVER:1080 https://ifconfig.me   # profiles/tokyo-1.ovpn から出る
```

## プロファイル

`profiles/` 以下の `*.ovpn` 1 つにつきトンネルが 1 本張られます (サブディレクトリも可)。ファイル名 (拡張子なし) がそのまま Basic 認証の username です。ファイルを足したり消したりしたら `docker compose restart` してください。

- 証明書や鍵、`auth-user-pass` はインラインで書いてください。別ファイル参照は読めません
- `up` / `down` / `plugin` / `script-security` / `management` などプログラムを実行する指示を含むプロファイルは読み込まれません
- `dev` / `redirect-gateway` / `route` などルーティングに関わる指示は無視され、ovpnmux が自分で設定します

CONNECT だけに対応しています。ブラウザや curl は `https://` の URL では CONNECT を使いますが、`http://` の URL ではプロキシに `GET http://...` を送るので、そのままでは通りません (curl なら `-p` で CONNECT を強制できます)。

## 設定 (`config.yaml`)

`config.example.yaml` のコメントを見てください。

## 管理 API (`127.0.0.1:9090`)

- `GET /status` トンネル一覧 (プロファイル、状態、レイテンシ、接続数、バイト数、最後のエラー)
- `GET /healthz` ready なトンネルが 1 本以上あれば 200

## 仕組み

1. openvpn を `--ifconfig-noexec --route-noexec` で起動し、ホストのルーティングは触らせない
2. `--up` で呼ばれた `ovpnmux _up` が tun のアドレス・gateway・DNS を JSON で渡す
3. デーモンが `ip addr` / `ip route ... table N` / `ip rule add fwmark N lookup N` を設定する。table には `unreachable` のフォールバックがあり、トンネルが落ちても main に漏れない
4. プロキシは選んだトンネルの fwmark を `SO_MARK` に付けて dial する。DNS も同じ経路で引く
5. 定期的にヘルスチェックし、失敗が続くか openvpn が終了したら張り直す

## 開発

```sh
go test ./...
go build -o bin/ ./cmd/...
```

実トンネルの動作確認には `NET_ADMIN` と `/dev/net/tun` が要るので、Docker ホストで `docker compose up` して確認します。
`main` への push で GitHub Actions がテストを実行し、`ghcr.io/fa0311/ovpnmux:main` (amd64 / arm64) を公開します。

プロキシは自宅 LAN 向けです。インターネットに公開しないでください。
