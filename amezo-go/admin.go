package main

// admin.go - 管理画面 (Python 版 admin.py)

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	adminMaxBody     = 65536
	adminMaxFails    = 5
	adminLockSec     = 900
	adminFailFile    = "admin_fail.mem"
	adminListLimit   = 100
	adminBodyPreview = 60
	adminStyle       = `<style>
body { font-family: sans-serif; margin: 12px; }
table { border-collapse: collapse; }
th, td { border: 1px solid #999; padding: 4px 8px; vertical-align: top; }
td.body { max-width: 32em; word-break: break-all; }
form.inline { display: inline; }
.msg { padding: 6px 10px; border: 1px solid #c33; margin: 8px 0; }
.msg.ok { border-color: #393; }
.keybox { border: 2px solid #c60; padding: 10px 14px; margin: 10px 0; background: #fff8e8; }
.keybox code { font-size: 1.3em; letter-spacing: 0.05em; user-select: all; }
button { padding: 4px 12px; }
</style>`
)

func adminPage(title, body string) {
	bail(&response{
		status: 200,
		headers: []headerPair{
			{"Content-Type", "text/html; charset=UTF-8"},
			{"Cache-Control", "no-store"},
			{"X-Robots-Tag", "noindex"},
		},
		body: fmt.Sprintf("<!DOCTYPE html>\n<html lang=\"ja\"><head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><meta name=\"robots\" content=\"noindex\"><title>%s</title>%s</head><body>\n<h2>%s</h2>\n%s\n</body></html>\n",
			htmlEscape(title), adminStyle, htmlEscape(title), body),
	})
}

func adminMsg(message string, ok bool) string {
	if message == "" {
		return ""
	}
	cls := "msg"
	if ok {
		cls = "msg ok"
	}
	return fmt.Sprintf("<div class=\"%s\">%s</div>", cls, htmlEscape(message))
}

func adminHidden(pw string, fields ...headerPair) string {
	var out strings.Builder
	fmt.Fprintf(&out, "<input type=\"hidden\" name=\"adminpass\" value=\"%s\">", htmlEscape(pw))
	for _, f := range fields {
		fmt.Fprintf(&out, "<input type=\"hidden\" name=\"%s\" value=\"%s\">", htmlEscape(f.name), htmlEscape(f.value))
	}
	return out.String()
}

func adminBtn(pw, label, confirm, act, folder, res, id string) string {
	var fields []headerPair
	if act != "" {
		fields = append(fields, headerPair{"act", act})
	}
	if folder != "" {
		fields = append(fields, headerPair{"folder", folder})
	}
	if res != "" {
		fields = append(fields, headerPair{"res", res})
	}
	if id != "" {
		fields = append(fields, headerPair{"id", id})
	}
	onclick := ""
	if confirm != "" {
		onclick = fmt.Sprintf(" onclick=\"return confirm('%s')\"", htmlEscape(confirm))
	}
	return fmt.Sprintf("<form method=\"post\" action=\"admin.cgi\" class=\"inline\">%s<button type=\"submit\"%s>%s</button></form>",
		adminHidden(pw, fields...), onclick, htmlEscape(label))
}

func adminNav(pw, folder string) string {
	s := adminBtn(pw, "板一覧", "", "boards", "", "", "")
	if folder != "" {
		s += " " + adminBtn(pw, fmt.Sprintf("「%s」のスレッド一覧", folder), "", "threads", folder, "", "")
	}
	return "<p>" + s + "</p>"
}

func adminFatal(pw, message string) {
	adminPage("エラー", adminMsg(message, false)+adminNav(pw, ""))
}

func adminLoginPage(message string) {
	adminPage("管理画面",
		adminMsg(message, false)+"<form method=\"post\" action=\"admin.cgi\"><input type=\"hidden\" name=\"act\" value=\"boards\">管理用パスワード <input type=\"password\" name=\"adminpass\" autocomplete=\"current-password\"> <button type=\"submit\">ログイン</button></form>")
}

// ---------- 認証 (失敗回数の制限つき) ----------
type adminFailEntry struct {
	count int
	ts    int64
}

// adminFailStore は admin_fail.mem をロックして updateFn を実行し書き戻す。開けなければ ok=false。
func adminFailStore(updateFn func(fails map[string]*adminFailEntry, now int64) bool) (bool, bool) {
	path := appPath(adminFailFile)
	result := false
	ran := false
	busy := callWithFdLock(path, 0, 0o600, func(f *os.File) {
		now := epochNow()
		fails := map[string]*adminFailEntry{}
		lines, _ := readFileLines(path)
		for _, line := range lines {
			p := splitStringNoEmpty(line, ' ')
			if len(p) == 3 && allDigits(p[1]) && allDigits(p[2]) {
				ts, _ := strconv.ParseInt(p[2], 10, 64)
				if now-ts < adminLockSec {
					cnt, _ := strconv.Atoi(p[1])
					fails[p[0]] = &adminFailEntry{cnt, ts}
				}
			}
		}
		result = updateFn(fails, now)
		ran = true
		var out strings.Builder
		for k, v := range fails {
			fmt.Fprintf(&out, "%s %d %d\n", k, v.count, v.ts)
		}
		writeFileString(path, out.String())
	})
	if busy || !ran {
		return false, false
	}
	return result, true
}

func adminAuthenticate(pw string) bool {
	ip := clientIP()
	unit := ip
	if unit == "" {
		unit = "unknown"
	}
	who := hmacSHA256Hex(idKey(), utf8Bytes("adm:"+ipUnit(unit)))[:24]
	blocked, storeOK := adminFailStore(func(fails map[string]*adminFailEntry, now int64) bool {
		if e, ok := fails[who]; ok {
			return e.count >= adminMaxFails
		}
		return false
	})
	if !storeOK {
		adminPage("管理画面", adminMsg("状態ファイル (admin_fail.mem) を開けないため、受け付けられません。", false))
	}
	if blocked {
		adminPage("管理画面", adminMsg("失敗が続いたため、しばらく受け付けません。時間をおいてやり直してください。", false))
	}
	delcrypt := loadDelcrypt()
	if delcrypt == "" {
		adminPage("管理画面", adminMsg("管理用パスワード (delpass.txt) が設定されていません。", false))
	}
	ok := pw != "" && verifyHash(pw, delcrypt)
	adminFailStore(func(fails map[string]*adminFailEntry, now int64) bool {
		if ok {
			delete(fails, who)
		} else if e, exists := fails[who]; exists {
			e.count++
			e.ts = now
		} else {
			fails[who] = &adminFailEntry{1, now}
		}
		return ok
	})
	return ok
}

// ---------- 板・スレッドの読み取り ----------
type adminBoard struct{ name, plain string }

func adminReadBoards() []adminBoard {
	var boards []adminBoard
	for _, line := range blistLines() {
		p := splitString(line, ',')
		name := p[0]
		if name == "" || startsWithP(".", name) || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			continue
		}
		title := ""
		if len(p) > 2 {
			title = p[2]
		}
		title = replaceAll(title, "#c#", ",")
		plain := ""
		if alt := strings.Index(title, "alt=\""); alt >= 0 {
			if b := strings.IndexByte(title[alt+5:], '"'); b >= 0 {
				plain = title[alt+5 : alt+5+b]
			}
		} else {
			plain = stripTags(title)
		}
		boards = append(boards, adminBoard{name, plain})
	}
	return boards
}

type adminThread struct {
	mtime int64
	num   string
	path  string
}

// adminListThreads は (表示中, 非表示中) を返す。各要素は新しい順。
func adminListThreads(folder string) ([]adminThread, []adminThread) {
	var active, hidden []adminThread
	for _, n := range listDirNames(appPath(folder)) {
		x, num := false, ""
		if len(n) > 6 && startsWithP("x", n) && endsWithP(".html", n) && digitsP(n[1:len(n)-5], 1, -1) {
			x, num = true, n[1:len(n)-5]
		} else if threadFileNameP(n) {
			num = n[:len(n)-5]
		}
		if num == "" {
			continue
		}
		mt, ok := fileMtimeOf(appPath(folder, n))
		if !ok {
			continue
		}
		if x {
			hidden = append(hidden, adminThread{mt, num, appPath(folder, n)})
		} else {
			active = append(active, adminThread{mt, num, appPath(folder, n)})
		}
	}
	byMtimeDesc := func(s []adminThread) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].mtime > s[j].mtime })
	}
	byMtimeDesc(active)
	byMtimeDesc(hidden)
	return active, hidden
}

func threadTitleAndCount(path string) (string, int) {
	title, count := "", 0
	lines, _ := readFileLines(path)
	for i, line := range lines {
		if i == 0 {
			if b, ok := extractFirstB(line); ok {
				title = trimSpace(htmlUnescape(b))
			}
		}
		if dt := strings.Index(line, "<dt>"); dt >= 0 {
			if _, ok := parseLeadingInt(line, dt+4); ok {
				count++
			}
		}
	}
	return title, count
}

type adminRes struct {
	num  int
	name string
	date string
	id   string
	body string
}

func adminParseThread(path string) (string, []adminRes) {
	title := ""
	var rows []adminRes
	lines, _ := readFileLines(path)
	for i, line := range lines {
		if i == 0 {
			if b, ok := extractFirstB(line); ok {
				title = trimSpace(htmlUnescape(b))
			}
		}
		dt := strings.Index(line, "<dt>")
		if dt < 0 {
			continue
		}
		num, ok := parseLeadingInt(line, dt+4)
		if !ok {
			continue
		}
		name := ""
		if nm := strings.Index(line, "投稿者：<font"); nm >= 0 {
			if bs := strings.Index(line[nm:], "><b>"); bs >= 0 {
				bs += nm
				if be := strings.Index(line[bs:], " </b>"); be >= 0 {
					name = trimSpace(stripTags(htmlUnescape(line[bs+4 : bs+be])))
				}
			}
		}
		date := ""
		if dm := strings.Index(line, "投稿日："); dm >= 0 {
			if e := strings.Index(line[dm:], "</font>"); e >= 0 {
				date = trimSpace(line[dm+4 : dm+e])
			}
		}
		if p := strings.Index(date, " ID:"); p >= 0 {
			date = trimSpace(date[:p])
		}
		postID := ""
		if im := strings.Index(line, "data-id=\""); im >= 0 {
			if e := strings.IndexByte(line[im+9:], '"'); e >= 0 {
				postID = line[im+9 : im+9+e]
			}
		}
		body := ""
		if bm := strings.Index(line, "<dd>"); bm >= 0 {
			if e := strings.Index(line[bm:], " </dl>"); e >= 0 {
				body = line[bm+4 : bm+e]
			}
		}
		body = htmlUnescape(stripTags(replaceAll(body, "<br>", " ")))
		rows = append(rows, adminRes{num, name, date, postID, body})
	}
	return title, rows
}

// ---------- 画面 ----------
func adminShowBoards(pw string) {
	var rows strings.Builder
	for _, b := range adminReadBoards() {
		fmt.Fprintf(&rows, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>",
			htmlEscape(b.name), htmlEscape(b.plain),
			adminBtn(pw, "開く", "", "threads", b.name, "", ""))
	}
	body := "<p>板がありません。</p>"
	if rows.Len() > 0 {
		body = fmt.Sprintf("<table><tr><th>板</th><th>名称</th><th></th></tr>%s</table>", rows.String())
	}
	adminPage("板の一覧", body)
}

func adminShowThreads(pw, folder, message string, ok bool) {
	active, hidden := adminListThreads(folder)
	body := adminMsg(message, ok) + adminNav(pw, "")
	var rows strings.Builder
	limit := minInt(len(active), adminListLimit)
	for _, th := range active[:limit] {
		title, count := threadTitleAndCount(th.path)
		if title == "" {
			title = "(題名なし)"
		}
		lockMark := ""
		if threadIsLocked(folder, th.num) {
			lockMark = "🔒"
		}
		fmt.Fprintf(&rows, "<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s %s %s</td></tr>",
			htmlEscape(title), count, formatAdminTime(th.mtime), lockMark,
			adminBtn(pw, "レスを見る", "", "replies", folder, th.num, ""),
			adminBtn(pw, "鍵の管理", "", "keys", folder, th.num, ""),
			adminBtn(pw, "非表示", "このスレッドを非表示にします。よろしいですか?", "hide", folder, th.num, ""))
	}
	if rows.Len() > 0 {
		body += fmt.Sprintf("<table><tr><th>題名</th><th>レス数</th><th>更新</th><th>鍵</th><th></th></tr>%s</table>", rows.String())
		if len(active) > adminListLimit {
			body += fmt.Sprintf("<p>新しい順に %d 件だけ表示しています (全 %d 件)。</p>", adminListLimit, len(active))
		}
	} else {
		body += "<p>表示中のスレッドはありません。</p>"
	}
	var hrows strings.Builder
	hlimit := minInt(len(hidden), adminListLimit)
	for _, th := range hidden[:hlimit] {
		title, count := threadTitleAndCount(th.path)
		if title == "" {
			title = "(題名なし)"
		}
		fmt.Fprintf(&hrows, "<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>",
			htmlEscape(title), count, formatAdminTime(th.mtime),
			adminBtn(pw, "復元", "", "restore", folder, th.num, ""))
	}
	if hrows.Len() > 0 {
		body += fmt.Sprintf("<h3>非表示中のスレッド</h3><table><tr><th>題名</th><th>レス数</th><th>更新</th><th></th></tr>%s</table>", hrows.String())
	}
	adminPage(fmt.Sprintf("「%s」のスレッド", folder), body)
}

func adminShowReplies(pw, folder, res, message string, ok bool) {
	path := appPath(folder, res+".html")
	if !pathExists(path) {
		adminFatal(pw, "スレッドがありません。(非表示中の場合は、スレッド一覧から復元してください)")
	}
	title, rows := adminParseThread(path)
	var trs strings.Builder
	for _, r := range rows {
		cb := "—"
		if r.num != 1 {
			cb = fmt.Sprintf("<input type=\"checkbox\" name=\"del\" value=\"%d\">", r.num)
		}
		runes := []rune(r.body)
		short := string(runes[:minInt(len(runes), adminBodyPreview)])
		if len(runes) > adminBodyPreview {
			short += "…"
		}
		fmt.Fprintf(&trs, "<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td class=\"body\">%s</td></tr>",
			cb, r.num, htmlEscape(r.name), htmlEscape(r.date), htmlEscape(r.id), htmlEscape(short))
	}
	if title == "" {
		title = "(題名なし)"
	}
	adminPage(fmt.Sprintf("%s (%s/%s)", title, folder, res),
		adminMsg(message, ok)+adminNav(pw, folder)+
			fmt.Sprintf("<form method=\"post\" action=\"admin.cgi\">%s<table><tr><th>削除</th><th>No</th><th>名前</th><th>日時</th><th>ID</th><th>本文</th></tr>%s</table><p><button type=\"submit\" onclick=\"return confirm('選択したレスを削除します。よろしいですか?')\">選択したレスを削除</button></p></form><p>1番は削除できません。スレッドごと消す場合は「非表示」を使ってください。</p><p>%s</p>",
				adminHidden(pw, headerPair{"act", "delete"}, headerPair{"folder", folder}, headerPair{"res", res}),
				trs.String(),
				adminBtn(pw, "このスレッドを非表示にする", "このスレッドを非表示にします。よろしいですか?", "hide", folder, res, "")))
}

func adminShowKeys(pw, folder, res, message string, ok bool, issued string) {
	path := appPath(folder, res+".html")
	if !pathExists(path) {
		adminFatal(pw, "スレッドがありません。")
	}
	title, _ := threadTitleAndCount(path)
	locked := threadIsLocked(folder, res)
	body := adminMsg(message, ok) + adminNav(pw, folder)
	if issued != "" {
		body += fmt.Sprintf("<div class=\"keybox\"><b>キーを発行しました。この画面を閉じると、二度と表示されません。</b><br>いまのうちに控えて、書き込んでよい人に渡してください。<br><br><code>%s</code></div>", htmlEscape(issued))
	}
	stateText := "鍵なし (誰でも書き込めます)"
	if locked {
		stateText = "🔒 鍵つき (認証キーがないと書き込めません)"
	}
	var buttons string
	if locked {
		buttons = adminBtn(pw, "鍵を外す", "このスレッドの鍵を外して、誰でも書き込めるようにします。よろしいですか?", "unlock", folder, res, "") +
			" " + adminBtn(pw, "書き込みフォームを作り直す", "", "lock", folder, res, "")
	} else {
		buttons = adminBtn(pw, "鍵をかける", "このスレッドに鍵をかけます。キーを持たない人は書き込めなくなります。よろしいですか?", "lock", folder, res, "")
	}
	body += fmt.Sprintf("<p>状態: <b>%s</b></p><p>%s</p><p>※ 鍵つきでも、読むのは誰でもできます。</p>", stateText, buttons)
	var rows strings.Builder
	for _, r := range keysLoad(folder) {
		if r.scope != "*" && r.scope != res {
			continue
		}
		active := r.status == "active"
		status := "失効"
		revokeBtn := ""
		if active {
			status = "有効"
			confirm := "このキーを失効します。よろしいですか?"
			if r.scope == "*" {
				confirm = "このキーは、この板の鍵つきスレッド全部で使えなくなります。よろしいですか?"
			}
			revokeBtn = adminBtn(pw, "失効", confirm, "revoke", folder, res, r.id)
		}
		scopeText := "このスレッドだけ"
		if r.scope == "*" {
			scopeText = "この板の鍵つきスレッドどれでも"
		}
		fmt.Fprintf(&rows, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
			htmlEscape(r.id), htmlEscape(r.label), scopeText, formatAdminTime(r.created), status, revokeBtn)
	}
	keysTable := "<p>このスレッドで使えるキーはまだありません。</p>"
	if rows.Len() > 0 {
		keysTable = fmt.Sprintf("<table><tr><th>ID</th><th>ラベル</th><th>範囲</th><th>発行日時</th><th>状態</th><th></th></tr>%s</table>", rows.String())
	}
	body += "<h3>キー</h3>" + keysTable
	body += fmt.Sprintf("<h3>キーを発行する</h3><form method=\"post\" action=\"admin.cgi\">%sラベル (誰に渡すか。40文字まで) <input type=\"text\" name=\"label\" size=\"24\"><br>範囲 <label><input type=\"radio\" name=\"scope\" value=\"thread\" checked> このスレッドだけ</label> <label><input type=\"radio\" name=\"scope\" value=\"board\"> この板の鍵つきスレッドどれでも</label><br><button type=\"submit\">発行</button></form><p>鍵をかける前にキーを発行しても、あとから発行してもかまいません。キーは発行したときに1度だけ表示されます (サーバーにはハッシュしか残りません)。</p>",
		adminHidden(pw, headerPair{"act", "issue"}, headerPair{"folder", folder}, headerPair{"res", res}))
	if title == "" {
		title = "(題名なし)"
	}
	adminPage(fmt.Sprintf("鍵の管理: %s (%s/%s)", title, folder, res), body)
}

// ---------- 操作 ----------
func adminDoSetLock(folder, res string, on bool) (bool, string) {
	if !pathExists(appPath(folder, res+".html")) {
		return false, "スレッドがありません。"
	}
	if !threadSetLock(folder, res, on) {
		return false, "鍵の印のファイルを更新できませんでした。"
	}
	if on {
		return true, fmt.Sprintf("スレッド %s に鍵をかけました。", res)
	}
	return true, fmt.Sprintf("スレッド %s の鍵を外しました。", res)
}

// adminParseDateInLine は投稿日文字列をエポック秒にする。取れなければ ok=false。
// Python 版 DATE_RE と同じく、各単位の直前の数字を読む。
func adminParseDateInLine(line string) (int64, bool) {
	numBefore := func(unit string) (int, bool) {
		pos := strings.Index(line, unit)
		if pos < 0 {
			return 0, false
		}
		start := pos
		for start > 0 && line[start-1] >= '0' && line[start-1] <= '9' {
			start--
		}
		if start == pos {
			return 0, false
		}
		n, err := strconv.Atoi(line[start:pos])
		if err != nil {
			return 0, false
		}
		return n, true
	}
	year, ok1 := numBefore("年")
	month, ok2 := numBefore("月")
	day, ok3 := numBefore("日")
	hour, ok4 := numBefore("時")
	min, ok5 := numBefore("分")
	sec, ok6 := numBefore("秒")
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return 0, false
	}
	return jstToEpoch(year, month, day, hour, min, sec), true
}

func adminDoDelete(folder, res string, nums []int) (bool, string) {
	filtered := nums[:0]
	for _, n := range nums {
		if n != 1 {
			filtered = append(filtered, n)
		}
	}
	nums = filtered
	if len(nums) == 0 {
		return false, "削除するレスが選ばれていません。(1番は削除できません)"
	}
	path := appPath(folder, res+".html")
	if !pathExists(path) {
		return false, "スレッドがありません。"
	}
	numSet := map[int]bool{}
	for _, n := range nums {
		numSet[n] = true
	}
	removed := 0
	var stamp int64 = -1
	origMtime, _ := fileMtimeOf(path)
	errFlag := false
	callWithFdLock(path, 10, 0, func(f *os.File) {
		lines, ok := readFileLines(path)
		if !ok {
			errFlag = true
			return
		}
		var keep []string
		for _, line := range lines {
			num := 0
			hasNum := false
			if dt := strings.Index(line, "<dt>"); dt >= 0 {
				if n, ok2 := parseLeadingInt(line, dt+4); ok2 {
					num, hasNum = n, true
				}
			}
			if hasNum && numSet[num] {
				removed++
			} else {
				if st, ok3 := adminParseDateInLine(line); ok3 {
					stamp = st
				}
				keep = append(keep, line)
			}
		}
		var buf strings.Builder
		for _, l := range keep {
			buf.WriteString(l)
			buf.WriteString("\n")
		}
		if !writeFileString(path, buf.String()) {
			errFlag = true
		}
	})
	if errFlag {
		return false, "削除中にファイルエラーが発生しました。"
	}
	ts := origMtime
	if stamp >= 0 {
		ts = stamp
	}
	if ts > 0 || stamp >= 0 || origMtime > 0 {
		setFileMtime(path, ts+1)
	}
	return true, fmt.Sprintf("%d件のレスを削除しました。", removed)
}

func adminDoHide(folder, res string) (bool, string) {
	src := appPath(folder, res+".html")
	dst := appPath(folder, "x"+res+".html")
	switch {
	case !pathExists(src):
		return false, "スレッドがありません。"
	case pathExists(dst):
		return false, "同じ番号の非表示スレッドが既にあるため、処理できません。"
	case renamePath(src, dst):
		chmodFile(dst, 0o600)
		return true, fmt.Sprintf("スレッド %s を非表示にしました。", res)
	default:
		return false, "スレッドの非表示化に失敗しました。"
	}
}

func adminDoRestore(folder, res string) (bool, string) {
	src := appPath(folder, "x"+res+".html")
	dst := appPath(folder, res+".html")
	switch {
	case !pathExists(src):
		return false, "非表示のスレッドがありません。"
	case pathExists(dst):
		return false, "同じ番号の表示中スレッドが既にあるため、復元できません。"
	case renamePath(src, dst):
		chmodFile(dst, 0o644)
		return true, fmt.Sprintf("スレッド %s を復元しました。", res)
	default:
		return false, "スレッドの復元に失敗しました。"
	}
}

func adminRegen(folder string) string {
	_, status := regenIndex(folder, 1, false, false)
	if status != "done" {
		return " (他の処理が再生成中のため、板の一覧の更新は省略されました)"
	}
	return ""
}

// ---------- メイン ----------
// parseAdminParams は parse_qs 相当: キー -> 値のリスト。
func parseAdminParams(octs []byte) map[string][]string {
	table := map[string][]string{}
	if octs == nil {
		return table
	}
	s := utf8Decode(octs)
	for _, pair := range splitString(s, '&') {
		k, v, _ := splitOnce(pair, '=')
		key := urlDecode(k)
		table[key] = append(table[key], urlDecode(v))
	}
	return table
}

func handleAdmin(req *request) *response {
	return runHandler(func() *response {
		adminMain(req)
		return nil
	})
}

func adminMain(req *request) {
	currentRequest = req
	method := req.method
	var params map[string][]string
	if method == "POST" {
		if len(req.body) > adminMaxBody {
			adminPage("エラー", adminMsg("送信内容が大きすぎます。", false))
		}
		params = parseAdminParams(req.body)
	}
	get1 := func(key string) string {
		if params != nil {
			if vs := params[key]; len(vs) > 0 {
				return vs[0]
			}
		}
		return ""
	}
	getAll := func(key string) []string {
		if params != nil {
			return params[key]
		}
		return nil
	}
	pw := get1("adminpass")
	if method != "POST" || pw == "" {
		adminLoginPage("")
	}
	if !adminAuthenticate(pw) {
		adminLoginPage("パスワードが違います。")
	}
	act := get1("act")
	if act == "" || act == "boards" {
		adminShowBoards(pw)
	}
	folder := get1("folder")
	if folder == "" || strings.Contains(folder, "/") || strings.Contains(folder, "\\") ||
		strings.Contains(folder, "..") || !isBoard(folder) {
		adminFatal(pw, "板名が不正です。")
	}
	if act == "threads" {
		adminShowThreads(pw, folder, "", true)
	}
	res := get1("res")
	if !digitsP(res, 1, 20) {
		adminFatal(pw, "スレッド番号が不正です。")
	}
	switch act {
	case "replies":
		adminShowReplies(pw, folder, res, "", true)
	case "keys":
		adminShowKeys(pw, folder, res, "", true, "")
	case "lock", "unlock":
		ok, message := adminDoSetLock(folder, res, act == "lock")
		if ok {
			message += adminRegen(folder)
		}
		adminShowKeys(pw, folder, res, message, ok, "")
	case "issue":
		scope := res
		if get1("scope") == "board" {
			scope = "*"
		}
		_, issued := keyIssue(folder, get1("label"), scope)
		if issued == "" {
			adminShowKeys(pw, folder, res, "キーを保存できませんでした。", false, "")
		} else {
			adminShowKeys(pw, folder, res, adminRegen(folder), true, issued)
		}
	case "revoke":
		keyID := get1("id")
		idOK := len(keyID) == 4
		if idOK {
			for _, c := range keyID {
				if !strings.ContainsRune(keyAlphabet, c) {
					idOK = false
					break
				}
			}
		}
		ok := idOK && keyRevoke(folder, keyID)
		message := "キーを失効できませんでした。"
		if ok {
			message = fmt.Sprintf("キー %s を失効しました。", keyID) + adminRegen(folder)
		}
		adminShowKeys(pw, folder, res, message, ok, "")
	case "delete":
		var nums []int
		for _, x := range getAll("del") {
			if digitsP(x, 1, 6) {
				n, _ := strconv.Atoi(x)
				nums = append(nums, n)
			}
		}
		ok, message := adminDoDelete(folder, res, nums)
		if ok {
			message += adminRegen(folder)
		}
		adminShowReplies(pw, folder, res, message, ok)
	case "hide":
		ok, message := adminDoHide(folder, res)
		if ok {
			message += adminRegen(folder)
		}
		adminShowThreads(pw, folder, message, ok)
	case "restore":
		ok, message := adminDoRestore(folder, res)
		if ok {
			message += adminRegen(folder)
		}
		adminShowThreads(pw, folder, message, ok)
	default:
		adminFatal(pw, "不明な操作です。")
	}
}
