package main

// server.go - 簡易 Web サーバー (Python 版 setup_expose.py のルーティング部分)
// 許可リスト方式の静的配信、不正パスの 400、過去ログ gz 配信、CGI の内部ディスパッチ。

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var verbose bool

var allowedCGI = []string{"amezo", "dai", "admin", "jump"}
var staticRootFiles = []string{"/", "/index.html", "/style.css", "/hide.js", "/ad.html"}

const maxPostBytesServer = 65536

// ---------- 起動時準備 ----------
func checkFlockWorks() {
	path := appPath(".flock_test")
	f1, err1 := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	var f2 *os.File
	var err2 error
	if err1 == nil {
		f2, err2 = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	}
	effective := false
	if err1 == nil && err2 == nil {
		if fdLock(f1, false) {
			effective = !fdLock(f2, true)
			fdUnlock(f1)
		}
	}
	if f1 != nil {
		f1.Close()
	}
	if f2 != nil {
		f2.Close()
	}
	_ = os.Remove(path)
	if !effective {
		fmt.Println(" ⚠️ このディレクトリでは flock が効いていないか、確認できませんでした。")
		fmt.Println(" 同時に投稿されるとファイルが壊れるおそれがあります。ローカルディスクに置いてください。")
	}
}

func setupBBSDirectories() {
	fmt.Println("📂 BBS用のディレクトリとパーミッションを設定しています...")
	for _, needed := range []string{"index.html", "style.css", "hide.js", "header.html", "blist.txt"} {
		if !fileRegularP(appPath(needed)) {
			fmt.Printf(" ⚠️ %s が見つかりません\n", needed)
		}
	}
	if !fileRegularP(appPath("delpass.txt")) {
		fmt.Println(" ℹ️ delpass.txt がありません。管理画面を使うなら:./run.sh --make-hash 合言葉")
	}
	checkFlockWorks()
	for _, board := range boardFolders() {
		ensureDir(appPath(board))
		index := appPath(board, "index.html")
		sz, _ := fileSizeOf(index)
		if !fileRegularP(index) || sz < 100 {
			writeFileString(index, fmt.Sprintf("<html><head><meta http-equiv=\"refresh\" content=\"0;url=../amezo.cgi?folder=%s\"></head><body>Redirecting to BBS...</body></html>\n", board))
			setFileMtime(index, epochNow()-3600)
			fmt.Printf(" - 初期リダイレクト用ファイルを配置: %s/index.html\n", board)
		}
	}
}

// ---------- パスの検査 (Python 版 is_malformed_path) ----------
func dotSegmentP(path string) bool {
	for _, part := range splitString(path, '/') {
		if part == "." {
			return true
		}
	}
	return false
}

func malformedPathP(rawPath string) bool {
	return rawPath == "" || !startsWithP("/", rawPath) ||
		strings.Contains(rawPath, "//") || strings.Contains(rawPath, "%") ||
		strings.Contains(rawPath, "\\") || strings.ContainsRune(rawPath, 0) ||
		strings.Contains(rawPath, "..") || dotSegmentP(rawPath)
}

func acceptsGzipP(header string) bool {
	for _, tok := range splitString(header, ',') {
		parts := splitString(tok, ';')
		for i := range parts {
			parts[i] = trimSpace(parts[i])
		}
		name := strings.ToLower(parts[0])
		if name == "gzip" || name == "x-gzip" {
			q := 1.0
			for _, p := range parts[1:] {
				if len(p) > 2 && strings.EqualFold(p[:2], "q=") {
					if v, err := strconv.ParseFloat(trimSpace(p[2:]), 64); err == nil {
						q = v
					}
				}
			}
			return q > 0
		}
	}
	return false
}

// ---------- HTTP の読み書き ----------
func writeHTTPResponse(w http.ResponseWriter, status int, headers []headerPair, body []byte, headOnly bool) {
	h := w.Header()
	hasLen := false
	for _, p := range headers {
		h.Add(p.name, p.value)
		if strings.EqualFold(p.name, "Content-Length") {
			hasLen = true
		}
	}
	if !hasLen {
		h.Set("Content-Length", strconv.Itoa(len(body)))
	}
	h.Set("Connection", "close")
	w.WriteHeader(status)
	if !headOnly {
		_, _ = w.Write(body)
	}
}

func respondError(w http.ResponseWriter, status int, message string, headOnly bool) {
	writeHTTPResponse(w, status, []headerPair{{"Content-Type", "text/plain; charset=UTF-8"}}, []byte(message), headOnly)
}

// ---------- ルーティング ----------
// boardFileAllowedP は (?:index\d*|kako|o?\d+)\.html 相当。
func boardFileAllowedP(name string) bool {
	if !endsWithP(".html", name) {
		return false
	}
	base := name[:len(name)-5]
	switch {
	case base == "kako":
		return true
	case startsWithP("index", base):
		return len(base) == 5 || digitsP(base[5:], 1, -1)
	case base != "" && allDigits(base):
		return true
	case len(base) > 1 && base[0] == 'o':
		return digitsP(base[1:], 1, -1)
	}
	return false
}

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func staticAllowedP(path string) bool {
	if inList(staticRootFiles, path) {
		return true
	}
	if len(path) <= 1 {
		return false
	}
	board, tail, found := splitOnce(path[1:], '/')
	if !isBoard(board) {
		return false
	}
	return !found || tail == "" || boardFileAllowedP(tail)
}

func staticFilePath(path string) string {
	if path == "/" || path == "/index.html" {
		return appPath("index.html")
	}
	if inList(staticRootFiles, path) {
		return appPath(path[1:])
	}
	board, tail, _ := splitOnce(path[1:], '/')
	if tail == "" {
		return appPath(board, "index.html")
	}
	return appPath(board, tail)
}

// cgiName は /xxx.cgi か /xxx.py なら xxx (許可されたものだけ)。それ以外は ""。
func cgiName(path string) string {
	if len(path) <= 1 {
		return ""
	}
	rest := path[1:]
	if strings.Contains(rest, "/") {
		return ""
	}
	base, ext, found := splitOnce(rest, '.')
	if !found {
		return ""
	}
	if (ext == "cgi" || ext == "py") && inList(allowedCGI, base) {
		return base
	}
	return ""
}

func archivedGzPath(rawPath string) string {
	parts := splitStringNoEmpty(rawPath, '/')
	if len(parts) != 2 || !isBoard(parts[0]) {
		return ""
	}
	name := parts[1]
	if len(name) > 6 && name[0] == 'o' && endsWithP(".html", name) && digitsP(name[1:len(name)-5], 1, -1) {
		plain := appPath(parts[0], name)
		gz := appPath(parts[0], name+".gz")
		if fileRegularP(gz) {
			if !fileRegularP(plain) {
				return gz
			}
			pm, _ := fileMtimeOf(plain)
			gm, _ := fileMtimeOf(gz)
			if pm == gm {
				return gz
			}
		}
	}
	return ""
}

func serveArchived(w http.ResponseWriter, gz string, accept string, headOnly bool) {
	common := []headerPair{
		{"Content-Type", "text/html; charset=UTF-8"},
		{"Vary", "Accept-Encoding"},
		{"X-Content-Type-Options", "nosniff"},
		{"Cache-Control", "public, max-age=3600"},
	}
	if acceptsGzipP(accept) {
		data, err := os.ReadFile(gz)
		if err != nil {
			respondError(w, 404, "Not Found", headOnly)
			return
		}
		writeHTTPResponse(w, 200, append(common, headerPair{"Content-Encoding", "gzip"}), data, headOnly)
		return
	}
	if plain, ok := gunzipToString(gz); ok {
		writeHTTPResponse(w, 200, common, []byte(plain), headOnly)
	} else {
		respondError(w, 404, "Not Found", headOnly)
	}
}

func dispatchCGI(name string, req *request, w http.ResponseWriter, headOnly bool) {
	resp := func() (r *response) {
		defer func() {
			if e := recover(); e != nil {
				if be, ok := e.(bbsExit); ok {
					r = be.resp
					return
				}
				fmt.Fprintf(os.Stderr, "amezo: %s の処理でエラー: %v\n", name, e)
				r = &response{status: 500, headers: []headerPair{{"Content-Type", "text/plain; charset=UTF-8"}}, body: "Internal Server Error"}
			}
		}()
		switch name {
		case "amezo":
			return handleAmezo(req)
		case "dai":
			return handleDai(req)
		case "admin":
			return handleAdmin(req)
		case "jump":
			return handleJump(req)
		}
		return nil
	}()
	if resp != nil {
		writeHTTPResponse(w, resp.status, resp.headers, []byte(resp.body), headOnly)
	}
}

func routeHandler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if e := recover(); e != nil {
			fmt.Fprintf(os.Stderr, "amezo: 接続処理でエラー: %v\n", e)
		}
	}()
	target := r.RequestURI
	rawPath, query := target, ""
	if q := strings.IndexByte(target, '?'); q >= 0 {
		rawPath, query = target[:q], target[q+1:]
	}
	headOnly := r.Method == "HEAD"
	if verbose {
		fmt.Printf(" %s %s\n", r.Method, target)
	}
	// 本体サイズの検査はルーティングより先 (Python 版と同じ)
	if r.ContentLength > maxPostBytesServer {
		respondError(w, 413, "Payload Too Large", headOnly)
		return
	}
	if malformedPathP(rawPath) {
		respondError(w, 400, "Bad Request", headOnly)
		return
	}
	// 過去ログ gz
	if r.Method == "GET" || r.Method == "HEAD" {
		if gz := archivedGzPath(rawPath); gz != "" {
			serveArchived(w, gz, r.Header.Get("Accept-Encoding"), headOnly)
			return
		}
	}
	if name := cgiName(rawPath); name != "" {
		if r.Method == "GET" || r.Method == "POST" || r.Method == "HEAD" {
			method := r.Method
			if headOnly {
				method = "GET"
			}
			var body []byte
			if r.Body != nil {
				body, _ = io.ReadAll(r.Body)
			}
			remote := "127.0.0.1"
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
				remote = host
			}
			var headers []headerPair
			for k, vs := range r.Header {
				for _, v := range vs {
					headers = append(headers, headerPair{k, v})
				}
			}
			req := &request{
				method:      method,
				queryString: query,
				body:        body,
				headers:     headers,
				remoteAddr:  remote,
				scriptName:  rawPath,
			}
			dispatchCGI(name, req, w, headOnly)
		}
		// 許可メソッド以外は Lisp 版と同じく応答しない (接続を閉じる)
		return
	}
	switch {
	case r.Method == "GET" || r.Method == "HEAD":
		if !staticAllowedP(rawPath) {
			respondError(w, 404, "Not Found", headOnly)
			return
		}
		path := staticFilePath(rawPath)
		if !fileRegularP(path) {
			respondError(w, 404, "Not Found", headOnly)
			return
		}
		ctype := "text/html"
		switch {
		case endsWithP(".css", path):
			ctype = "text/css"
		case endsWithP(".js", path):
			ctype = "text/javascript"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			respondError(w, 404, "Not Found", headOnly)
			return
		}
		writeHTTPResponse(w, 200, []headerPair{{"Content-Type", ctype}}, data, headOnly)
	case r.Method == "POST":
		respondError(w, 501, "Can only POST to CGI scripts", headOnly)
	default:
		respondError(w, 501, "Not Implemented", headOnly)
	}
}

// ---------- サーバ本体 ----------
var serverSocket *http.Server

func startServer(port int) error {
	serverSocket = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: http.HandlerFunc(routeHandler),
	}
	fmt.Printf("🚀 Webサーバーを起動しています (ポート %d)...\n", port)
	go func() {
		_ = serverSocket.ListenAndServe()
	}()
	return nil
}

// httpStatus は自己チェック用の単純な GET。ステータスコードを返す (失敗は 0)。
func httpStatus(port int, path string) int {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	if err != nil {
		return 0
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n", path)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0
	}
	parts := splitStringNoEmpty(strings.TrimRight(line, "\r\n"), ' ')
	if len(parts) < 2 {
		return 0
	}
	code, _ := strconv.Atoi(parts[1])
	return code
}

func selfCheck(port int) bool {
	fmt.Println("🔎 起動チェック:")
	good := true
	checks := []struct {
		path string
		want int
	}{
		{"/dai.cgi", 200}, {"/jump.cgi", 200}, {"/style.css", 200}, {"/hide.js", 200},
		{"//amezo.py", 400}, {"/setup_expose.py", 404}, {"/amezo.go", 404},
		{"/trip_secret.txt", 404}, {"/id_secret.txt", 404}, {"/delpass.txt", 404},
		{"/cap.txt", 404}, {"/blist.txt", 404}, {"/config.ini", 404},
		{"/key_secret.txt", 404}, {"/jump_secret.txt", 404}, {"/ng.txt", 404},
		{"/server.go", 404}, {"//archive.py", 400},
	}
	for _, c := range checks {
		got := httpStatus(port, c.path)
		mark := "✅"
		note := ""
		if got != c.want {
			good = false
			mark = "❌"
			note = fmt.Sprintf(" (期待: %d)", c.want)
		}
		fmt.Printf(" %s %s -> %d%s\n", mark, c.path, got, note)
	}
	if !good {
		fmt.Println(" ⚠️ 期待と違う結果があります。公開する前に server.go の配信判定を確認してください。")
	}
	return good
}

// ---------- cloudflared トンネル (Python 版 setup_expose.py の start_tunnel 相当) ----------
func cloudflaredOnPath() string {
	p, err := exec.LookPath("cloudflared")
	if err != nil {
		return ""
	}
	return p
}

func cloudflaredLocalPath() string { return appPath("cloudflared") }

// cloudflaredValid はダウンロードしたファイルが本物の実行ファイルらしいか確かめる
// (HTTP エラーの本文をバイナリと誤認しないため。Lisp 版の既知の問題への対策)。
func cloudflaredValid(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() < 1000000 {
		return false
	}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return false
	}
	return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
}

// downloadCloudflared は公式リリースから cloudflared をダウンロードして実行可能にする。
func downloadCloudflared(dest string) bool {
	mach := ""
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		mach = trimSpace(string(out))
	}
	asset := "cloudflared-linux-amd64"
	if strings.Contains(mach, "aarch64") || strings.Contains(mach, "arm") {
		asset = "cloudflared-linux-arm64"
	}
	url := "https://github.com/cloudflare/cloudflared/releases/latest/download/" + asset
	fmt.Printf("cloudflared をダウンロード中... (%s)\n", url)
	cmd := exec.Command("curl", "-fsSL", url, "-o", dest)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(dest)
		return false
	}
	chmodFile(dest, 0o755)
	if !cloudflaredValid(dest) {
		_ = os.Remove(dest)
		return false
	}
	return true
}

// ensureCloudflared は cloudflared の実行ファイル名 (またはパス) を返す。用意できなければ ""。
// Python 版と同じく、PATH に無ければアプリフォルダへダウンロードする。
func ensureCloudflared() string {
	if p := cloudflaredOnPath(); p != "" {
		return p
	}
	local := cloudflaredLocalPath()
	if fileRegularP(local) {
		if cloudflaredValid(local) {
			return local
		}
		_ = os.Remove(local) // 壊れた (エラーページ等) ファイルは消して取り直す
	}
	if downloadCloudflared(local) {
		return local
	}
	return ""
}

// extractTunnelURL は cloudflared の出力行から公開URLを取り出す。無ければ ""。
// エラー行に出る api.trycloudflare.com は公開URLではないので拾わない。
func extractTunnelURL(line string) string {
	if strings.Contains(line, "failed") {
		return ""
	}
	start := strings.Index(line, "https://")
	if start < 0 {
		return ""
	}
	marker := ".trycloudflare.com"
	m := strings.Index(line[start:], marker)
	if m < 0 {
		return ""
	}
	m += start
	if line[start+8:m] == "api" {
		return ""
	}
	return line[start : m+len(marker)]
}

type tunnelProc struct {
	cmd *exec.Cmd
}

func (t *tunnelProc) kill() {
	if t != nil && t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Signal(syscall.SIGTERM)
		_ = t.cmd.Wait()
	}
}

// startTunnel は cloudflared tunnel を起動し、公開URLが取れるまで最大20秒待つ。
func startTunnel(port int) *tunnelProc {
	fmt.Println("☁️ Cloudflare Tunnel をセットアップしています...")
	bin := ensureCloudflared()
	if bin == "" {
		fmt.Println("❌ cloudflared を用意できませんでした (ダウンロードに失敗)。トンネルは張れません。")
		return nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		fmt.Println("❌ cloudflared を起動できませんでした。")
		return nil
	}
	cmd := exec.Command(bin, "tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", port))
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		fmt.Println("❌ cloudflared を起動できませんでした。")
		return nil
	}
	pw.Close()
	urlCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			fmt.Println(line)
			if u := extractTunnelURL(line); u != "" && !sent {
				urlCh <- u
				sent = true
			}
		}
	}()
	fmt.Println("⏳ トンネルの公開URLを取得しています...")
	select {
	case u := <-urlCh:
		fmt.Printf("\n🎉: %s/\n\n", u)
	case <-time.After(20 * time.Second):
		fmt.Println("❌ 公開URLの取得に失敗しました。トンネルプロセスの出力を確認してください。")
	}
	return &tunnelProc{cmd: cmd}
}

// setupTunnelCommand は `--setup-tunnel`: cloudflared を用意して版数を表示する。
func setupTunnelCommand() int {
	bin := ensureCloudflared()
	if bin == "" {
		fmt.Fprintln(os.Stderr, "❌ cloudflared を用意できませんでした。")
		return 1
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌ cloudflared を実行できませんでした。")
		return 1
	}
	line := firstLine(trimSpace(string(out)))
	fmt.Printf("✅ cloudflared の用意ができました: %s (%s)\n", line, bin)
	return 0
}
