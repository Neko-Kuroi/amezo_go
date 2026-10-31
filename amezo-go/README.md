# amezo-go — あめぞう風掲示板 (Go 版)

Python 版 (amezo-python) / Common Lisp 版 (amezo-lisp) と**データ互換**のある、あめぞう風掲示板の Go 実装です。同じ掲示板フォルダを Python 版・Lisp 版と共有できます (トリップ・ID・認証キー・delpass のハッシュも互換)。

Go 版の特徴は **単一バイナリ** なこと。ビルド済みバイナリ (`amezo`) だけあれば、実行環境にランタイムやライブラリは要りません。

## 起動

```sh
sh run.sh --make-hash 好きなパスワード   # 初回のみ: 管理パスワード (delpass.txt) を作る
sh run.sh                                # http://127.0.0.1:8000/ で起動
```

`run.sh` は、バイナリが無い/古い場合にだけ `go build` します (Go 1.22 以降が必要)。ビルド済みバイナリが同梱されていれば、そのまま `./amezo` でも起動できます。

オプション:

```sh
sh run.sh --port 8080      # ポート変更
sh run.sh --verbose        # アクセスログを出す
sh run.sh --tunnel         # Cloudflare Tunnel で公開 (下記)
```

## Cloudflare Tunnel で公開する

```sh
sh run.sh --tunnel
```

- `cloudflared` が PATH にもアプリフォルダにも無ければ、公式リリースから自動ダウンロードします (amd64 / arm64)
- 起動すると公開 URL (`https://….trycloudflare.com/`) が表示されます。URL は起動ごとに変わります
- Ctrl+C で掲示板とトンネルが一緒に止まります
- 事前にダウンロードだけ済ませたい場合は `sh run.sh --setup-tunnel`

## 過去ログの圧縮 (archive)

古いスレッド (`o数字.html`) を gzip 圧縮して平文を消す、2段階のアーカイバです。

```sh
sh archive.sh --selftest main     # 環境チェック
sh archive.sh main --quiet=600    # 実行 (最終更新から600秒以上たったもの)
sh archive.sh --restore main o260101000000   # 平文に戻す
```

## CGI モード

外部の Web サーバから CGI として呼ぶこともできます。

```sh
./amezo --cgi amezo   # / dai / admin / jump も同じ
```

## 設定

`config.ini` (書式は `config.sample.ini` 参照) で投稿間隔・上限・トリップ等の既定値を上書きできます。`config.php` は読み込みません。

## テスト

```sh
go test .                        # 関数レベルの単体テスト (暗号ベクトル、トリップ/ID 等)
python3 tests/port_test.py       # 結合テスト (サーバを立てて一通り)
python3 tests/archive_test.py    # archive のテスト
python3 tests/interop_test.py    # Python 版との相互運用テスト
```

## ファイル構成

| ファイル | 内容 |
|---|---|
| `main.go` | 起動点 (サーバ / --make-hash / --cgi / archive) |
| `server.go` | HTTP サーバ、配信の許可リスト、cloudflared トンネル |
| `post.go` / `regen.go` | 投稿処理、板 index の再生成 |
| `dai.go` / `jump.go` / `admin.go` | 板一覧、中継ページ、管理画面 |
| `archive.go` | 過去ログの gzip 圧縮 |
| `core.go` / `config.go` / `crypto.go` / `util.go` | 共通部品、設定、暗号、土台 |

設計の考え方は `DESIGN.md` を参照してください。
