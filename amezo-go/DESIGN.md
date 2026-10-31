# DESIGN — Go 版の設計判断

Python 版を仕様の正本とし、Common Lisp 版 (amezo-lisp) の設計判断を引き継いだうえで、Go 向けに決めたこと。

## 互換性 (最優先)

- データ形式は Python 版と完全互換。同じ掲示板フォルダを Python 版 / Lisp 版 / Go 版で共有できる (`tests/interop_test.py` で Python 版との相互運用を確認)。
- ハッシュ・署名もバイト互換: PBKDF2-HMAC-SHA256 (delpass)、トリップ = base64(HMAC-SHA256) 先頭10字 (`+`→`.`、`/`→`_`)、ID 同方式8字、jump 署名 = base64url HMAC 先頭22字、認証キー = base32 24字 + HMAC hex ハッシュ。

## Go 向けの判断

1. **依存は標準ライブラリのみ**。SHA-256 / HMAC / base64 / base32 / gzip 連携は標準でそろう。標準に無い PBKDF2 だけ、HMAC から自前実装した (`crypto.go`)。外部モジュール (x/crypto 等) は引かない。
2. **単一バイナリ配布**。`CGO_ENABLED=0` で静的にビルドすれば、バイナリ1つでどこでも動く。実行環境に Go は不要 (ビルド時のみ)。
3. **ファイルロックは `syscall.Flock`** (Linux)。Python の `fcntl.flock` と同じ flock(2) なので、版をまたいでも排他が効く。
4. **HTTP は net/http、ただしパス判定は自前**。`http.ServeMux` のパス浄化に頼らず、生の `RequestURI` を Python 版 `is_malformed_path` と同じ規則で検査する (`//`・`..`・`%`・`\`・`.` セグメントで 400)。配信は許可リスト方式。
5. **リクエスト/レスポンスの抽象 + panic による脱出**。Lisp 版の構造と同じで、ハンドラはリクエスト構造体→レスポンス構造体。`pageError` 等は `bbsExit` を panic してディスパッチ側で回収する (Python 版の `sys.exit` 相当)。
6. **日付は JST 固定** (`time.FixedZone`、OS の TZ に頼らない)。曜日は Python の `weekday()` (月曜=0) に合わせて変換。
7. **gzip の作成・展開は外部 `gzip` コマンド** (Lisp 版と同じ)。中身の照合は自前の SHA-256 で行う。

## Lisp 版からの意図的な差分

- **cloudflared のダウンロードを堅牢化**。Lisp 版では HTTP エラーの本文をバイナリと誤認して保存してしまう問題があったため、Go 版は `curl -fsSL` (HTTP エラーで失敗扱い) + サイズ/ELF マジック検証を行い、壊れたファイルが残っていたら消して取り直す。
- 自己チェックの探査パスを Go のファイル名に合わせた (`/server.go` 等が 404 であること)。

## Lisp 版から引き継いだ割り切り

- 巨大スレッドの「末尾だけ読む最適化」は入れていない (全文読みで一貫性優先)。
- フォームの不正バイトは UTF-8 置換文字として扱う (CP932 フォールバックなし)。
- トンネルは `--tunnel` 明示時のみ (Python 版 setup_expose.py の「既定でトンネル」とは違う)。
