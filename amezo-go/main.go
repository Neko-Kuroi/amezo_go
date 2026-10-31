package main

// main.go - 起動点: サーバ / --make-hash / --cgi / archive サブコマンド

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func printHashAndExit(password string) int {
	fmt.Println(makeHash(password))
	return 0
}

func writeDelpass(password string) int {
	if password == "" {
		fmt.Fprintln(os.Stderr, "❌ 合言葉が空です。")
		return 1
	}
	writeFileString(appPath("delpass.txt"), makeHash(password)+"\n")
	chmodFile(appPath("delpass.txt"), 0o600)
	fmt.Println("✅ delpass.txt を作りました。管理画面は /admin.cgi です。")
	return 0
}

func cgiEnvRequest(scriptBase string, body []byte) *request {
	var headers []headerPair
	for _, pair := range []headerPair{
		{"Referer", "HTTP_REFERER"}, {"Origin", "HTTP_ORIGIN"}, {"Host", "HTTP_HOST"},
		{"User-Agent", "HTTP_USER_AGENT"}, {"CF-Connecting-IP", "HTTP_CF_CONNECTING_IP"},
		{"X-Forwarded-For", "HTTP_X_FORWARDED_FOR"}, {"Accept-Encoding", "HTTP_ACCEPT_ENCODING"},
	} {
		if v, ok := os.LookupEnv(pair.value); ok {
			headers = append(headers, headerPair{pair.name, v})
		}
	}
	return &request{
		method:      getenvDefault("REQUEST_METHOD", "GET"),
		queryString: getenvDefault("QUERY_STRING", ""),
		body:        body,
		headers:     headers,
		remoteAddr:  getenvDefault("REMOTE_ADDR", ""),
		scriptName:  "/" + scriptBase + ".cgi",
	}
}

// runCGI は外部の CGI サーバから呼ばれた場合のモード。標準入力から本体を読み、標準出力へ返す。
func runCGI(scriptBase string) int {
	n := safeInteger(getenvDefault("CONTENT_LENGTH", "0"), 0)
	if n < 0 {
		n = 0
	}
	var body []byte
	if n > 0 {
		body = make([]byte, n)
		_, _ = io.ReadFull(os.Stdin, body)
	}
	req := cgiEnvRequest(scriptBase, body)
	currentRequest = req
	resp := func() (r *response) {
		defer func() {
			if e := recover(); e != nil {
				if be, ok := e.(bbsExit); ok {
					r = be.resp
					return
				}
				r = &response{status: 500, body: fmt.Sprintf("Error: %v", e)}
			}
		}()
		switch scriptBase {
		case "amezo":
			return handleAmezo(req)
		case "dai":
			return handleDai(req)
		case "admin":
			return handleAdmin(req)
		case "jump":
			return handleJump(req)
		}
		return &response{status: 404, body: "Not Found"}
	}()
	var head strings.Builder
	fmt.Fprintf(&head, "Status: %d\r\n", resp.status)
	for _, h := range resp.headers {
		fmt.Fprintf(&head, "%s: %s\r\n", h.name, h.value)
	}
	head.WriteString("\r\n")
	_, _ = os.Stdout.WriteString(head.String())
	_, _ = os.Stdout.WriteString(resp.body)
	return 0
}

// serveMain はサーバを起動して Ctrl+C まで動かす。
func serveMain(port int, tunnel bool) int {
	setupBBSDirectories()
	if err := startServer(port); err != nil {
		fmt.Fprintf(os.Stderr, "❌ サーバーを起動できませんでした: %v\n", err)
		return 1
	}
	for i := 0; i < 50; i++ {
		if httpStatus(port, "/style.css") == 200 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Printf("✅ Webサーバーがポート %d で正常に応答しました。\n", port)
	selfCheck(port)
	var tp *tunnelProc
	if tunnel {
		tp = startTunnel(port)
	} else {
		fmt.Printf("\n🏠 ローカルで起動しました: http://127.0.0.1:%d/\n", port)
	}
	fmt.Println("\n⏳ サーバー稼働中... 停止するには Ctrl+C を押してください。")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\n🛑 終了します。")
	if tp != nil {
		tp.kill()
	}
	if serverSocket != nil {
		_ = serverSocket.Close()
	}
	return 0
}

func main() {
	// アプリのルートは実行ファイルのあるフォルダ (Lisp 版のスクリプトの場所と同じ考え方)
	if exe, err := os.Executable(); err == nil {
		appDir = filepath.Dir(exe) + "/"
	} else if cwd, err := os.Getwd(); err == nil {
		appDir = cwd + "/"
	}
	args := os.Args[1:]
	loadConfig()
	switch {
	case len(args) > 0 && (args[0] == "archive" || args[0] == "--archive"):
		os.Exit(archiveMain(args[1:]))
	case len(args) > 0 && args[0] == "--make-hash":
		if len(args) > 1 {
			os.Exit(printHashAndExit(args[1]))
		}
		fmt.Fprintln(os.Stderr, "Usage: amezo --make-hash PASSWORD")
		os.Exit(1)
	case len(args) > 0 && args[0] == "--make-delpass":
		pw := ""
		if len(args) > 1 {
			pw = args[1]
		}
		os.Exit(writeDelpass(pw))
	case len(args) > 0 && args[0] == "--setup-tunnel":
		os.Exit(setupTunnelCommand())
	case len(args) > 0 && args[0] == "--cgi":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		os.Exit(runCGI(name))
	default:
		port, tunnel := 8000, false
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--port":
				if i+1 < len(args) {
					port = safeInteger(args[i+1], 8000)
					i++
				}
			case "--tunnel":
				tunnel = true
			case "--no-tunnel":
				// 既定がトンネルなしなので何もしない
			case "--verbose":
				verbose = true
			default:
				fmt.Fprintf(os.Stderr, "不明な引数: %s\n", args[i])
			}
		}
		os.Exit(serveMain(port, tunnel))
	}
}
