package main

// post.go - amezo.py のメイン処理 (投稿の受付とレス表示)

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ---------- フォーム解析 ----------
type formPair struct{ k, v string }

func formGet(form []formPair, key string) string {
	for _, p := range form {
		if p.k == key {
			return p.v
		}
	}
	return ""
}

func formHas(form []formPair, key string) bool {
	for _, p := range form {
		if p.k == key {
			return true
		}
	}
	return false
}

// parseParams は QUERY_STRING をリストにする (値は url-decode 済み、キーはそのまま)。重複キーは最初のもの。
func parseParams(s string) []formPair {
	var out []formPair
	if s == "" {
		return out
	}
	for _, pair := range splitString(s, '&') {
		if pair == "" {
			continue
		}
		k, v, _ := splitOnce(pair, '=')
		out = append(out, formPair{k, urlDecode(v)})
	}
	return out
}

// parseFormBody は POST 本体を (リスト, trip用パスワード) にする。Python 版 main の前処理と同じ:
// 見えない文字の除去、name の最初の # で分割、HTML エスケープ、改行の正規化。
func parseFormBody(octs []byte) ([]formPair, string) {
	var form []formPair
	tripPw := ""
	if octs == nil {
		return form, tripPw
	}
	for _, pair := range bytes.Split(octs, []byte{'&'}) {
		eq := bytes.IndexByte(pair, '=')
		if eq < 0 {
			continue
		}
		key := utf8Decode(pair[:eq])
		value := urlDecodeBytes(pair[eq+1:])
		value = stripInvisible(value)
		if key == "name" {
			if p := strings.IndexByte(value, '#'); p >= 0 {
				tripPw = strings.Map(func(c rune) rune {
					if c <= 0x1f {
						return -1
					}
					return c
				}, value[p+1:])
				value = value[:p]
			}
		}
		value = htmlEscape(value)
		value = replaceAll(replaceAll(value, "\r\n", "\r"), "\n", "\r")
		if key == "comm" {
			value = replaceAll(value, "\r", "<br>")
		}
		value = strings.Map(func(c rune) rune {
			if c <= 0x1f {
				return -1
			}
			return c
		}, value)
		form = append(form, formPair{key, value})
	}
	return form, tripPw
}

func countOccurrences(needle, s string) int { return strings.Count(s, needle) }

// collapseGt は正規表現 &gt;(&gt;)+ -> &gt; 相当。
func collapseGt(s string) string {
	var out strings.Builder
	i, n := 0, len(s)
	for i < n {
		if i+4 <= n && s[i:i+4] == "&gt;" {
			out.WriteString("&gt;")
			for i+4 <= n && s[i:i+4] == "&gt;" {
				i += 4
			}
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

func removeNG(word, s string) string { return replaceAll(s, htmlEscape(word), "") }

// parseLeadingInt は位置 start から parse-integer (junk-allowed) 相当に数を読む。
func parseLeadingInt(s string, start int) (int, bool) {
	i := start
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digits {
		return 0, false
	}
	n, err := strconv.Atoi(s[start:i])
	if err != nil {
		// 巨大な数はそのまま大きい値として扱う
		if s[start] == '-' {
			return -1 << 30, true
		}
		return 1 << 30, true
	}
	return n, true
}

// ---------- レス表示 ----------
func resView(folder string, form []formPair) {
	res := formGet(form, "res")
	if !allDigits(res) {
		pageError("不正なスレッド指定です")
	}
	fromIdx := safeInteger(formGet(form, "from"), 1)
	if fromIdx < 1 {
		fromIdx = 1
	}
	filePath := appPath(folder, res+".html")
	if !pathExists(filePath) {
		threadMissing(folder, res, "表示")
	}
	lines, _ := readFileLines(filePath)
	folTag := fmt.Sprintf("<input type=\"hidden\" name=\"folder\" value=\"%s\">", htmlEscape(folder))
	fixj := func(line string) string {
		return replaceAll(line, "href=\"../jump.cgi?", "href=\"jump.cgi?")
	}
	formLine := func(line string) string {
		return replaceAll(replaceAll(line, "action=\"../", "action=\""), "</form>", folTag+"</form>")
	}
	fixbase := func(line string) string {
		return fixj(replaceAll(replaceAll(line, "href=\"../style.css\"", "href=\"style.css\""),
			"src=\"../hide.js\"", "src=\"hide.js\""))
	}
	var out strings.Builder
	idx := 0
	// <dl> か <dt> を含む行まで (置換して) 出す
	for idx < len(lines) {
		line := lines[idx]
		out.WriteString(fixbase(line))
		out.WriteString("\n")
		idx++
		if strings.Contains(line, "<dl>") || strings.Contains(line, "<dt>") {
			break
		}
	}
	// from 以上の最初のレスを探す
	matched := false
	footer := ""
	for idx < len(lines) {
		line := lines[idx]
		if startsWithP("<form", line) {
			footer = line
		}
		if dt := strings.Index(line, "<dt>"); dt >= 0 {
			if num, ok := parseLeadingInt(line, dt+4); ok && num >= fromIdx {
				out.WriteString(fixj(line))
				out.WriteString("\n")
				matched = true
				idx++
				break
			}
		}
		idx++
	}
	if !matched && footer != "" {
		out.WriteString(formLine(footer))
		out.WriteString("\n")
	}
	for idx < len(lines) {
		line := lines[idx]
		if strings.Contains(line, "</form>") {
			out.WriteString(formLine(line))
		} else {
			out.WriteString(fixj(line))
		}
		out.WriteString("\n")
		idx++
	}
	bail(htmlHeaderResponse(out.String(), 200, nil))
}

// parsePrevPost は前のレス行から (名前, 本文) を取る。取れなければ ok=false。
func parsePrevPost(line string) (string, string, bool) {
	b1 := strings.Index(line, "投稿者：<font")
	if b1 < 0 {
		return "", "", false
	}
	bs := strings.Index(line[b1:], "><b>")
	if bs < 0 {
		return "", "", false
	}
	bs += b1
	be := strings.Index(line[bs:], " </b>")
	if be < 0 {
		return "", "", false
	}
	be += bs
	dd := strings.Index(line[be:], "<dd>")
	if dd < 0 {
		return "", "", false
	}
	dd += be
	de := strings.Index(line[dd:], " </dl>")
	if de < 0 {
		return "", "", false
	}
	de += dd
	return line[bs+4 : be], line[dd+4 : de], true
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---------- メイン ----------
func handleAmezo(req *request) *response {
	return runHandler(func() *response {
		amezoMain(req)
		return nil
	})
}

func amezoMain(req *request) {
	currentRequest = req
	body := req.body
	if len(body) > maxPostBytes {
		pageError("投稿内容が大きすぎます")
	}
	queryParams := parseParams(req.queryString)
	form, tripPw := parseFormBody(body)
	folder := formGet(form, "folder")
	if folder == "" {
		folder = formGet(queryParams, "folder")
	}
	if folder == "" {
		referer := replaceAll(urlDecode(req.header("Referer")), "://www.", "://")
		if p := strings.Index(referer, "://"); p >= 0 {
			if slash := strings.IndexByte(referer[p+3:], '/'); slash >= 0 {
				slash += p + 3
				if slash2 := strings.IndexByte(referer[slash+1:], '/'); slash2 >= 0 {
					folder = referer[slash+1 : slash+1+slash2]
				}
			}
		}
	}
	if folder == "" {
		pageError("板名が判定できません。")
	}
	if strings.Contains(folder, "/") || strings.Contains(folder, "..") || !isBoard(folder) {
		pageError("板名が不正です")
	}
	name := formGet(form, "name")
	mail := formGet(form, "mail")
	comm := formGet(form, "comm")
	subj := formGet(form, "subj")
	res := formGet(form, "res")
	if res != "" && !allDigits(res) {
		pageError("不正なスレッド指定です")
	}
	if formHas(form, "sub") && formGet(form, "sub") != "" {
		resView(folder, form)
	}
	// ページ指定
	pageRaw := formGet(form, "page")
	if !formHas(form, "page") {
		pageRaw = formGet(queryParams, "page")
	}
	nextRaw := formGet(form, "next")
	if !formHas(form, "next") {
		nextRaw = formGet(queryParams, "next")
	}
	page := 1
	switch {
	case digitsP(pageRaw, 1, 6):
		page, _ = strconv.Atoi(pageRaw)
	case digitsP(nextRaw, 1, 6):
		n, _ := strconv.Atoi(nextRaw)
		if n < 1 {
			n = 1
		}
		page = 1 + (n-1)/perPage
	}
	page = pageClamp(folder, page)
	reloadFlag := false
	if name == subj && name == comm {
		reloadFlag = true
	}
	if name == "" && subj == "" && comm == "" {
		reloadFlag = true
	}
	if countOccurrences("<br>", comm) > commLinesMax {
		pageError("改行が多すぎます")
	}
	if htmlCharsLen(replaceAll(comm, "<br>", "\n")) > commMax {
		pageError("コメントが長すぎます")
	}
	if htmlCharsLen(mail) > mailMax {
		pageError("メールが長すぎます")
	}
	subj = strings.Map(func(c rune) rune {
		if c == ' ' || c == '\u3000' || c == '\uFF3F' {
			return -1
		}
		return c
	}, subj)
	if htmlCharsLen(subj) > subjMax {
		pageError("題名が長すぎます")
	}
	name = replaceAll(name, tripMark, "◇")
	name = replaceAll(name, capMark, "☆")
	name = removeNameSpaces(name)
	name = ccut(name, nameMax)
	comm = collapseGt(comm)
	// NG ワード
	if pathExists(appPath("ng.txt")) {
		ngLines, _ := readFileLines(appPath("ng.txt"))
		for _, line := range ngLines {
			if line == "" {
				continue
			}
			parts := splitString(line, ',')
			ng := parts[0]
			opt := ""
			if len(parts) > 1 {
				opt = parts[1]
			}
			if ng == "" {
				continue
			}
			switch opt {
			case "n":
				if strings.Contains(name, htmlEscape(ng)) {
					reloadFlag = true
				}
			case "c":
				if strings.Contains(comm, htmlEscape(ng)) {
					reloadFlag = true
				}
			default:
				name = removeNG(ng, name)
				comm = removeNG(ng, comm)
				mail = removeNG(ng, mail)
				subj = removeNG(ng, subj)
			}
		}
	}
	if page == 1 && !reloadFlag {
		capOk := isCap(name, tripPw)
		if !capOk {
			name = replaceAll(name, htmlEscape(adminName), "＃"+adminName)
		}
		if res != "" {
			subj = ""
		}
		if name == "" {
			pageError("投稿者を記入してください")
		}
		if comm == "" {
			pageError("コメントが入力されていません")
		}
		if res == "" && subj == "" {
			pageError("題名が入力されていません")
		}
		if left := postIntervalLeft(folder); left > 0 {
			pageError(fmt.Sprintf("連投を検知しました。あと %d 秒ほど待ってから投稿してください", left))
		}
		ip := clientIP()
		if subj != "" {
			checkGlobalIP(ip)
		}
		comm = linkifyUrls(comm, jumpEnabled)
		trip := ""
		if capOk {
			name += capMark
		} else {
			trip = makeTrip(tripPw)
			if trip != "" {
				name = name + tripMark + trip
			}
		}
		if mail != "" {
			name = fmt.Sprintf("<a href=\"mailto:%s \">%s </a>", mail, name)
		}
		now := epochNow()
		postID := makeID(ip, folder, now)
		dateStr := formatJstDate(now)
		if res == "" {
			res = formatResNumber(now)
		}
		postWrite(folder, res, subj, name, comm, trip, postID, dateStr, form)
	}
	indexName, _ := regenIndex(folder, page, reloadFlag, true)
	redirectResponse(folder, indexName)
}

// postWrite はスレッドファイルへの書き込み (新規作成 / 返信)。ロックの中で行う。
func postWrite(folder, res, subj, name, comm, trip, postID, dateStr string, form []formPair) {
	newThread := subj != ""
	filePath := appPath(folder, res+".html")
	if newThread {
		created := false
		for try := 0; try <= 30; try++ {
			filePath = appPath(folder, res+".html")
			if !pathExists(appPath(folder, "x"+res+".html")) && !archivedExists(folder, res) {
				if f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
					f.Close()
					created = true
				}
			}
			if created {
				break
			}
			if try >= 30 {
				pageError("スレッドを作成できません.")
			}
			n, _ := strconv.Atoi(res)
			res = strconv.Itoa(n + 1)
		}
		writeFileString(filePath, "")
	}
	if !pathExists(filePath) {
		threadMissing(folder, res, "返信")
	}
	done := false
	nlinkZero := false
	callWithFdLock(filePath, 10, 0, func(f *os.File) {
		// ロック待ちの間に archive が名前を消していたら (nlink=0) 書かない
		if !newThread {
			if fi, err := f.Stat(); err == nil {
				if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink == 0 {
					nlinkZero = true
					return
				}
			}
		}
		done = writePostUnderLock(folder, res, subj, name, comm, trip, postID, dateStr, form, filePath, newThread)
	})
	if nlinkZero {
		threadMissing(folder, res, "返信")
	}
	if !done {
		pageError("スレッドがありません.")
	}
}

func writePostUnderLock(folder, res, subj, name, comm, trip, postID, dateStr string, form []formPair, filePath string, newThread bool) bool {
	locked := !newThread && threadIsLocked(folder, res)
	if locked {
		given := formGet(form, "key")
		if given == "" {
			pageError("このスレッドは認証キーが必要です。ページを再読み込みして、認証キーの欄に入力してください。")
		}
		if !keyVerify(folder, res, given) {
			pageError("認証キーが正しくありません。")
		}
	}
	lines, _ := readFileLines(filePath)
	cnt, posLines := 0, 0
	prev := ""
	for _, line := range lines {
		dt := strings.Index(line, "<dt>")
		if dt < 0 {
			break
		}
		num, ok := parseLeadingInt(line, dt+4)
		if !ok {
			break
		}
		cnt, prev, posLines = num, line, posLines+1
	}
	cnt++
	if pname, pcomm, ok := parsePrevPost(prev); ok && name == pname && comm == pcomm {
		pageError("二重カキコです.")
	}
	subj1, subj2 := "", ""
	if newThread {
		subj1 = fmt.Sprintf("<html><head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>%s </title><link rel=\"stylesheet\" href=\"../style.css\">%s</head><body>", subj, scriptTag)
		subj2 = fmt.Sprintf("<font size=+2 color=red><b>%s </b></font>", subj)
	}
	meta := fmt.Sprintf("<font size=-1 class=\"meta\" data-id=\"%s\"", postID)
	if trip != "" {
		meta += fmt.Sprintf(" data-trip=\"%s\"", trip)
	}
	meta += ">"
	newLine := fmt.Sprintf("%s<dl><dt>%d%s投稿者：<font color=forestgreen><b>%s </b></font>%s　投稿日：%s ID:%s</font><br><dd>%s </dl>",
		subj1, cnt, subj2, name, meta, dateStr, postID, comm)
	var content strings.Builder
	for _, l := range lines[:posLines] {
		content.WriteString(l)
		content.WriteString("\n")
	}
	content.WriteString(newLine)
	content.WriteString("\n")
	content.WriteString(replyForm(res, "", locked))
	content.WriteString("</body></html>\n")
	writeFileString(filePath, content.String())
	touchLasttime(folder)
	return true
}
