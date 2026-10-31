package main

// core.go - amezo.py の共通部分: 秘密鍵、トリップ/ID/キャップ、認証キー、中継リンク

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---------- リクエスト/レスポンスの抽象 (CGI の exit の代わり) ----------
type headerPair struct{ name, value string }

type request struct {
	method      string
	queryString string
	body        []byte
	headers     []headerPair
	remoteAddr  string
	scriptName  string
}

type response struct {
	status  int
	headers []headerPair
	body    string
}

func (r *request) header(name string) string {
	for _, h := range r.headers {
		if strings.EqualFold(h.name, name) {
			return h.value
		}
	}
	return ""
}

var currentRequest *request

// bbsExit は Python 版の error()/redirect() の sys.exit 相当。panic で抜ける。
type bbsExit struct{ resp *response }

func bail(resp *response) { panic(bbsExit{resp}) }

func htmlHeaderResponse(body string, status int, extra []headerPair) *response {
	return &response{
		status:  status,
		headers: append([]headerPair{{"Content-Type", "text/html; charset=UTF-8"}}, extra...),
		body:    body,
	}
}

// pageError は Python 版 amezo.error(): 200 でエラー文を出して終わる。
func pageError(msg string) {
	bail(htmlHeaderResponse(
		fmt.Sprintf("<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\"></head><body><h2>%s</h2></body></html>",
			htmlEscape(msg)), 200, nil))
}

func redirectResponse(folder, indexName string) {
	target := htmlEscape(folder + "/" + indexName)
	bail(&response{
		status: 303,
		headers: []headerPair{
			{"Location", "../" + target},
			{"Content-Type", "text/html; charset=UTF-8"},
		},
		body: fmt.Sprintf("<html><head><meta http-equiv=\"refresh\" content=\"0;url=../%s\"></head><body>Redirecting to <a href=\"../%s\">%s</a>...</body></html>",
			target, target, target),
	})
}

// runHandler はハンドラを実行し、bbsExit をレスポンスに変換する。
func runHandler(fn func() *response) (resp *response) {
	defer func() {
		if e := recover(); e != nil {
			if be, ok := e.(bbsExit); ok {
				resp = be.resp
				return
			}
			panic(e)
		}
	}()
	return fn()
}

// ---------- 秘密鍵ファイル ----------
func readSecretKey(path string) []byte {
	s, ok := readFileString(path)
	if !ok {
		return nil
	}
	txt := trimSpace(s)
	if len(txt) != 64 {
		return nil
	}
	for _, c := range txt {
		if _, ok := hexVal(byte(c)); !ok {
			return nil
		}
	}
	return hexDecode(txt)
}

// loadSecret は秘密鍵 (32バイト) を読む。無ければ作る (Python 版 load_secret)。保存できなければ nil。
func loadSecret(path string) []byte {
	if key := readSecretKey(path); key != nil {
		return key
	}
	key := randomOctets(32)
	data := hexEncode(key) + "\n"
	tmp := fmt.Sprintf("%s.tmp%d", path, os.Getpid())
	writeExclusive := func(p string) bool {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return false
		}
		_, werr := f.WriteString(data)
		cerr := f.Close()
		return werr == nil && cerr == nil
	}
	if writeExclusive(tmp) {
		_ = os.Link(tmp, path)
		_ = os.Remove(tmp)
		if placed := readSecretKey(path); placed != nil {
			return placed
		}
	}
	if writeExclusive(path) {
		// 直接置けた
	} else {
		for i := 0; i < 20; i++ {
			time.Sleep(50 * time.Millisecond)
			if k := readSecretKey(path); k != nil {
				return k
			}
		}
	}
	if readSecretKey(path) == nil {
		// 壊れた鍵ファイルは置き換える
		writeFileString(path, data)
		chmodFile(path, 0o600)
	}
	return readSecretKey(path)
}

// ---------- クライアント IP ----------
func clientIP() string {
	addr := ""
	if currentRequest != nil {
		addr = currentRequest.remoteAddr
	}
	trusted := false
	for _, p := range trustedProxies {
		if p == addr {
			trusted = true
			break
		}
	}
	if !trusted || currentRequest == nil {
		return addr
	}
	if cf := currentRequest.header("CF-Connecting-IP"); cf != "" && ipValidP(cf) {
		return cf
	}
	parts := splitString(currentRequest.header("X-Forwarded-For"), ',')
	if len(parts) > 0 {
		last := trimSpace(parts[len(parts)-1])
		if last != "" && ipValidP(last) {
			return last
		}
	}
	return addr
}

// ---------- トリップ / ID / キャップ ----------
func tripKey() []byte {
	if k := loadSecret(appPath(tripSecretFile)); k != nil {
		return k
	}
	pageError("トリップ用の鍵を保存できません.")
	return nil
}

func idKey() []byte {
	if k := loadSecret(appPath(idSecretFile)); k != nil {
		return k
	}
	pageError("ID用の鍵を保存できません.")
	return nil
}

func keySecretKey() []byte {
	if k := loadSecret(appPath(keySecretFile)); k != nil {
		return k
	}
	pageError("認証キー用の鍵を保存できません.")
	return nil
}

func jumpSecretKey() []byte {
	if k := loadSecret(appPath(jumpSecretFile)); k != nil {
		return k
	}
	pageError("中継用の鍵を保存できません.")
	return nil
}

func cleanTripPassword(password string) string {
	var b []rune
	for _, c := range password {
		if c == ' ' || c == '\u3000' {
			continue
		}
		if c >= 0xFF01 && c <= 0xFF5E {
			c -= 0xFEE0
		}
		b = append(b, c)
	}
	if len(b) > tripPassMax {
		b = b[:tripPassMax]
	}
	return string(b)
}

func tripTransform(b64 string) string {
	r := strings.NewReplacer("+", ".", "/", "_")
	return r.Replace(b64)
}

func makeTrip(password string) string {
	pw := cleanTripPassword(password)
	if pw == "" {
		return ""
	}
	digest := hmacSHA256(tripKey(), utf8Bytes(pw))
	return tripTransform(base64Encode(digest)[:10])
}

func makeID(ip, board string, epoch int64) string {
	y, m, d, _, _, _, _ := epochToJST(epoch)
	msg := fmt.Sprintf("%s%s|%04d%02d%02d|%s", idCtx, ipUnit(ip), y, m, d, board)
	digest := hmacSHA256(idKey(), utf8Bytes(msg))
	return tripTransform(base64Encode(digest)[:idLen])
}

func removeNameSpaces(s string) string {
	return strings.Map(func(c rune) rune {
		if c == ' ' || c == '\u3000' || c == '\u200D' {
			return -1
		}
		return c
	}, s)
}

func isCap(nameEscaped, password string) bool {
	if password == "" {
		return false
	}
	lines, _ := readFileLines(appPath(capFile))
	for _, line := range lines {
		if line == "" || startsWithP("#", line) || !strings.Contains(line, ",") {
			continue
		}
		pos := strings.LastIndex(line, ",")
		capName, stored := line[:pos], line[pos+1:]
		if htmlEscape(removeNameSpaces(capName)) == nameEscaped && verifyHash(password, stored) {
			return true
		}
	}
	return false
}

func loadDelcrypt() string {
	s, ok := readFileString(appPath(delcryptFile))
	if !ok {
		return ""
	}
	return trimSpace(s)
}

// ---------- 板 ----------
func blistLines() []string {
	lines, _ := readFileLines(appPath("blist.txt"))
	return lines
}

func isBoard(folder string) bool {
	for _, line := range blistLines() {
		if line != "" && stringNthField(line, 0, ',') == folder {
			return true
		}
	}
	return false
}

// boardInfo は blist.txt から (背景色 タイトル)。
func boardInfo(folder string) (string, string) {
	for _, line := range blistLines() {
		if line == "" {
			continue
		}
		parts := splitString(line, ',')
		if parts[0] == folder {
			color, title := "", ""
			if len(parts) > 1 {
				color = parts[1]
			}
			if len(parts) > 2 {
				title = parts[2]
			}
			return color, title
		}
	}
	return "", ""
}

func boardFolders() []string {
	var out []string
	for _, line := range blistLines() {
		if name := stringNthField(line, 0, ','); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// ---------- 投稿制限 ----------
func postIntervalLeft(folder string) int64 {
	if postInterval <= 0 {
		return 0
	}
	s, ok := readFileString(appPath(folder, "lasttime.txt"))
	if !ok {
		return 0
	}
	t := trimSpace(s)
	if !allDigits(t) {
		return 0
	}
	last, _ := strconv.ParseInt(t, 10, 64)
	if left := int64(postInterval) - (epochNow() - last); left > 0 {
		return left
	}
	return 0
}

func touchLasttime(folder string) {
	if postInterval > 0 {
		writeFileString(appPath(folder, "lasttime.txt"), strconv.FormatInt(epochNow(), 10))
	}
}

// checkGlobalIP は同じ接続元の新規スレッドを globalWindow 秒止める (Python 版 check_global_ip)。
func checkGlobalIP(ip string) {
	who := hmacSHA256Hex(idKey(), utf8Bytes(rlCtx+ipUnit(ip)))[:24]
	path := appPath("global.mem")
	callWithFdLock(path, 0, 0o600, func(f *os.File) {
		now := epochNow()
		type entry struct {
			who string
			ts  int64
		}
		var table []entry
		lines, _ := readFileLines(path)
		for _, line := range lines {
			parts := splitStringNoEmpty(line, ' ')
			if len(parts) == 2 && allDigits(parts[1]) {
				ts, _ := strconv.ParseInt(parts[1], 10, 64)
				if now-ts < int64(globalWindow) {
					table = append(table, entry{parts[0], ts})
				}
			}
		}
		for _, e := range table {
			if e.who == who {
				pageError(fmt.Sprintf("あと %d 秒たってから投稿してください", int64(globalWindow)-(now-e.ts)))
			}
		}
		table = append(table, entry{who, now})
		var lines2 []string
		for _, e := range table {
			lines2 = append(lines2, fmt.Sprintf("%s %d\n", e.who, e.ts))
		}
		writeFileString(path, joinStrings(lines2, ""))
	})
}

// ---------- 過去ログ ----------
// archivedPath は o数字.html の実体パス (平文優先、無ければ .gz)。無ければ ""。
func archivedPath(folder, name string) string {
	p := appPath(folder, name)
	if fileRegularP(p) {
		return p
	}
	if gz := appPath(folder, name+".gz"); fileRegularP(gz) {
		return gz
	}
	return ""
}

func archivedExists(folder, res string) bool {
	return allDigits(res) && archivedPath(folder, "o"+res+".html") != ""
}

func threadMissing(folder, res, verb string) {
	if archivedExists(folder, res) {
		pageError(fmt.Sprintf("このスレッドは過去ログになったため%sできません.", verb))
	}
	pageError("スレッドがありません.")
}

// ---------- 中継リンク ----------
func isAlnumRune(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) }

func urlIsValid(url string) bool {
	if url == "" || len(utf8Bytes(url)) > urlMaxLen {
		return false
	}
	schemeLen := 0
	switch {
	case startsWithP("http://", url):
		schemeLen = 7
	case startsWithP("https://", url):
		schemeLen = 8
	default:
		return false
	}
	if schemeLen >= len(url) {
		return false
	}
	c0, _ := utf8.DecodeRuneInString(url[schemeLen:])
	if !(isAlnumRune(c0) || c0 == '[') {
		return false
	}
	for _, c := range url {
		if !isAlnumRune(c) && !strings.ContainsRune("-._~:/?#[]@!$*+,;=%()'&", c) {
			return false
		}
	}
	return true
}

func jumpSign(url string) string {
	return b64urlEncode(hmacSHA256(jumpSecretKey(), utf8Bytes(jumpCtx+url)))[:jumpSigLen]
}

func jumpVerify(url, sig string) bool {
	return urlIsValid(url) && constantTimeStringEqual(jumpSign(url), sig)
}

func jumpHref(url string) string {
	return fmt.Sprintf("../jump.cgi?u=%s&amp;s=%s", b64urlEncode(utf8Bytes(url)), jumpSign(url))
}

const linkifyChars = "-._~:/?#[]@!$*+,;=%()"

// linkifyUrls はエスケープ済み本文の URL をリンクにする (Python 版 linkify_urls)。
func linkifyUrls(escaped string, jump bool) string {
	var out strings.Builder
	pos, n := 0, len(escaped)
	for {
		start, schemeLen := -1, 0
		for i := pos; i < n; i++ {
			if startsWithP("https://", escaped[i:minInt(n, i+8)]) {
				start, schemeLen = i, 8
				break
			}
			if startsWithP("http://", escaped[i:minInt(n, i+7)]) {
				start, schemeLen = i, 7
				break
			}
		}
		_ = schemeLen
		if start < 0 {
			out.WriteString(escaped[pos:])
			break
		}
		out.WriteString(escaped[pos:start])
		// URL 本体を貪欲に取る (&amp; / &#39; も1単位)
		end := start
		for end < n {
			switch {
			case end+5 <= n && escaped[end:end+5] == "&amp;":
				end += 5
			case end+5 <= n && escaped[end:end+5] == "&#39;":
				end += 5
			default:
				c, size := utf8.DecodeRuneInString(escaped[end:])
				if isAlnumRune(c) || strings.ContainsRune(linkifyChars, c) {
					end += size
				} else {
					goto bodyDone
				}
			}
		}
	bodyDone:
		match := escaped[start:end]
		dsp, tail := match, ""
		// 末尾の不要文字を tail へ
		for len(dsp) > 0 {
			switch {
			case endsWithP("&amp;", dsp) || endsWithP("&#39;", dsp):
				tail = dsp[len(dsp)-5:] + tail
				dsp = dsp[:len(dsp)-5]
			case strings.ContainsRune(".,;:!?(", rune(dsp[len(dsp)-1])):
				tail = dsp[len(dsp)-1:] + tail
				dsp = dsp[:len(dsp)-1]
			case dsp[len(dsp)-1] == ')' && strings.Count(dsp, ")") > strings.Count(dsp, "("):
				tail = ")" + tail
				dsp = dsp[:len(dsp)-1]
			case dsp[len(dsp)-1] == ']' && strings.Count(dsp, "]") > strings.Count(dsp, "["):
				tail = "]" + tail
				dsp = dsp[:len(dsp)-1]
			default:
				goto tailDone
			}
		}
	tailDone:
		url := replaceAll(replaceAll(dsp, "&amp;", "&"), "&#39;", "'")
		if !urlIsValid(url) {
			out.WriteString(match)
		} else {
			href := htmlEscape(url)
			if jump {
				href = jumpHref(url)
			}
			fmt.Fprintf(&out, "<a href=\"%s\" target=\"_blank\" rel=\"noopener noreferrer nofollow\">%s</a>%s", href, dsp, tail)
		}
		pos = end
	}
	return out.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------- 返信フォーム ----------
func replyForm(res string, links string, locked bool) string {
	lockedHTML := ""
	if locked {
		lockedHTML = "<div class=\"row\">🔒 認証キー <input type=password name=\"key\" size=32 autocomplete=off required> このスレッドは認証キーがないと書き込めません</div>"
	}
	return fmt.Sprintf("<form method=post action=\"../amezo.cgi\" class=\"post\"><input type=hidden name=\"res\" value=\"%s\"><div class=\"row\">投稿者 <input type=text name=\"name\" size=20> メール <input type=text name=\"mail\" size=20 maxlength=50></div>%s<textarea name=\"comm\" rows=4 cols=70 maxlength=400></textarea><div class=\"row\"><input type=submit value=\"↑へのレスカキコ\"></div>%s</form>",
		res, lockedHTML, links)
}

// ---------- 認証キー ----------
func threadIsLocked(folder, res string) bool {
	return fileRegularP(appPath(folder, res+".lock"))
}

type keyRow struct {
	id      string
	label   string
	hash    string
	scope   string
	created int64
	status  string
}

// keyNormalize は入力キーを正規形 (大文字24文字) にする。形が違えば ""。
func keyNormalize(s string) string {
	var b []rune
	for _, ch := range s {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '2' && ch <= '7' {
			b = append(b, unicode.ToUpper(ch))
		}
	}
	if len(b) == 24 {
		return string(b)
	}
	return ""
}

func keyDisplay(canon string) string {
	var parts []string
	for i := 0; i+4 <= len(canon); i += 4 {
		parts = append(parts, canon[i:i+4])
	}
	return joinStrings(parts, "-")
}

func keyHash(canon string) string {
	return hmacSHA256Hex(keySecretKey(), utf8Bytes(keyCtx+canon))
}

func parseKeyRow(line string) *keyRow {
	p := splitString(line, '\t')
	if len(p) != 6 {
		return nil
	}
	created := int64(0)
	s := p[4]
	numEnd := 0
	if numEnd < len(s) && s[numEnd] == '-' {
		numEnd++
	}
	for numEnd < len(s) && s[numEnd] >= '0' && s[numEnd] <= '9' {
		numEnd++
	}
	if numEnd > 0 {
		if v, err := strconv.ParseInt(s[:numEnd], 10, 64); err == nil {
			created = v
		}
	}
	return &keyRow{id: p[0], label: p[1], hash: p[2], scope: p[3], created: created, status: p[5]}
}

func keysLoad(folder string) []*keyRow {
	var rows []*keyRow
	lines, _ := readFileLines(appPath(folder, keysFile))
	for _, line := range lines {
		if r := parseKeyRow(line); r != nil {
			rows = append(rows, r)
		}
	}
	return rows
}

func keyFindRow(folder, kid string) *keyRow {
	lines, _ := readFileLines(appPath(folder, keysFile))
	prefix := kid + "\t"
	for _, line := range lines {
		if startsWithP(prefix, line) {
			return parseKeyRow(line)
		}
	}
	return nil
}

func keyVerify(folder, res, inputKey string) bool {
	canon := keyNormalize(inputKey)
	if canon == "" {
		return false
	}
	row := keyFindRow(folder, canon[:4])
	return row != nil && row.status == "active" &&
		constantTimeStringEqual(row.hash, keyHash(canon)) &&
		(row.scope == "*" || row.scope == res)
}

// keysUpdate は keys.txt をロックして fn(全行) の結果で書き直す。失敗したら false。
func keysUpdate(folder string, fn func([]*keyRow) []*keyRow) bool {
	lockpath := appPath(folder, "keys.lock")
	ok := false
	busy := callWithFdLock(lockpath, 10, 0, func(f *os.File) {
		rows := fn(keysLoad(folder))
		path := appPath(folder, keysFile)
		tmp := fmt.Sprintf("%s.tmp%d", path, os.Getpid())
		var lines []string
		for _, r := range rows {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%s\n",
				r.id, r.label, r.hash, r.scope, r.created, r.status))
		}
		if writeFileString(tmp, joinStrings(lines, "")) {
			chmodFile(tmp, 0o600)
			if os.Rename(tmp, path) == nil {
				ok = true
				return
			}
		}
		_ = os.Remove(tmp)
	})
	return !busy && ok
}

// keyIssue は (ID, 表示用キー) を返す。失敗したら ("", "")。
func keyIssue(folder, label, scope string) (string, string) {
	label = trimSpace(strings.Map(func(c rune) rune {
		if c <= 0x1f || c == 0x7f {
			return ' '
		}
		return c
	}, label))
	if runes := []rune(label); len(runes) > 40 {
		label = string(runes[:40])
	}
	if label == "" {
		label = "(無題)"
	}
	if scope != "*" && !digitsP(scope, 1, 20) {
		return "", ""
	}
	var resultID, resultDisp string
	ok := keysUpdate(folder, func(rows []*keyRow) []*keyRow {
		used := map[string]bool{}
		for _, r := range rows {
			used[r.id] = true
		}
		canon := base32Encode(randomOctets(15))
		for used[canon[:4]] {
			canon = base32Encode(randomOctets(15))
		}
		resultID, resultDisp = canon[:4], keyDisplay(canon)
		return append(rows, &keyRow{
			id: canon[:4], label: label, hash: keyHash(canon), scope: scope,
			created: epochNow(), status: "active",
		})
	})
	if !ok {
		return "", ""
	}
	return resultID, resultDisp
}

func keyRevoke(folder, kid string) bool {
	hit := false
	ok := keysUpdate(folder, func(rows []*keyRow) []*keyRow {
		for _, r := range rows {
			if r.id == kid && r.status == "active" {
				r.status = "revoked"
				hit = true
			}
		}
		return rows
	})
	return ok && hit
}

func threadSetLock(folder, res string, lock bool) bool {
	path := appPath(folder, res+".html")
	if !fileRegularP(path) {
		return false
	}
	result := false
	busy := callWithFdLock(path, 10, 0, func(f *os.File) {
		data, ok := readFileString(path)
		if !ok {
			return
		}
		footer := len(data)
		if idx := strings.LastIndex(data, "\n<form"); idx >= 0 {
			footer = idx + 1
		}
		if !writeFileString(path, data[:footer]+replyForm(res, "", lock)+"</body></html>\n") {
			return
		}
		lockfile := appPath(folder, res+".lock")
		if lock {
			writeFileString(lockfile, "")
		} else if _, err := os.Stat(lockfile); err == nil {
			_ = os.Remove(lockfile)
		}
		result = true
	})
	return !busy && result
}

// ---------- スレッドの読み取り ----------
// readThreadTitle はスレッドファイル (または .gz) の1行目から <b>題名</b> を取る。読めなければ ""。
func readThreadTitle(path string) string {
	var first string
	if endsWithP(".gz", path) {
		tmp, ok := gunzipToString(path)
		if !ok {
			return ""
		}
		first = firstLine(tmp)
	} else {
		s, ok := readFileString(path)
		if !ok {
			return ""
		}
		first = firstLine(s)
	}
	if v, ok := extractTagContent(first, "b"); ok {
		return v
	}
	return ""
}

func firstLine(s string) string {
	if p := strings.IndexByte(s, '\n'); p >= 0 {
		return s[:p]
	}
	return s
}

// extractTagContent は最初の <tag>...</tag> の中身。無ければ ("", false)。
func extractTagContent(s, tag string) (string, bool) {
	open, close := "<"+tag+">", "</"+tag+">"
	a := strings.Index(s, open)
	if a < 0 {
		return "", false
	}
	b := strings.Index(s[a+len(open):], close)
	if b < 0 {
		return "", false
	}
	return s[a+len(open) : a+len(open)+b], true
}

// extractFirstB は正規表現 <b>([^<]*)</b> 相当: 最初の <b> から < まで。
func extractFirstB(s string) (string, bool) {
	a := strings.Index(s, "<b>")
	if a < 0 {
		return "", false
	}
	b := strings.Index(s[a+3:], "<")
	if b < 0 {
		return "", false
	}
	return s[a+3 : a+3+b], true
}

// gunzipToString は外部 gzip で展開して文字列にする。失敗したら ("", false)。
func gunzipToString(path string) (string, bool) {
	cmd := exec.Command("gzip", "-dc", path)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return utf8Decode(out), true
}

func countNewlinesInFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
