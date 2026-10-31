#!/bin/sh
# 使い方: sh run.sh [--port 8000] [--tunnel] [--verbose]
#         sh run.sh --make-hash パスワード   (delpass.txt を作って終了)
#         sh run.sh --setup-tunnel           (cloudflared を用意して終了。--tunnel の事前準備)
cd "$(dirname "$0")" || exit 1
if [ ! -x ./amezo ] || [ -n "$(find . -maxdepth 1 -name '*.go' -newer ./amezo 2>/dev/null)" ]; then
  go build -o amezo . || exit 1
fi
if [ "$1" = "--make-hash" ]; then
  exec ./amezo --make-delpass "$2"
fi
exec ./amezo "$@"
