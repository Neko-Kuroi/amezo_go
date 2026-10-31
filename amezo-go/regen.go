package main

// regen.go - 板 index の再生成と過去ログ一覧 (Python 版 amezo.regen_index 周辺)

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ---------- 過去ログ一覧 (kako) ----------
func kakoListSize(folder string) int64 {
	if sz, ok := fileSizeOf(appPath(folder, kakoList)); ok {
		return sz
	}
	return -1
}

type kakoItem struct{ mt, name string }

func sortKakoItems(items []kakoItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].mt != items[j].mt {
			return items[i].mt < items[j].mt
		}
		return items[i].name < items[j].name
	})
}

// appendKakoList は退避したアイテムの題名を kako.txt に追記する。
func appendKakoList(folder string, items []kakoItem) bool {
	sorted := append([]kakoItem{}, items...)
	sortKakoItems(sorted)
	var buf strings.Builder
	for _, item := range sorted {
		p := archivedPath(folder, item.name)
		if p == "" {
			p = appPath(folder, item.name)
		}
		fmt.Fprintf(&buf, "%s%s%s\n", item.name, kakoSep, readThreadTitle(p))
	}
	ok := false
	busy := callWithFdLock(appPath(folder, kakoList), 5, 0, func(f *os.File) {
		ok = appendFileString(appPath(folder, kakoList), buf.String())
	})
	return !busy && ok
}

func rebuildKakoList(folder string, names []string) bool {
	var items []kakoItem
	for _, name := range names {
		p := archivedPath(folder, name)
		if p == "" {
			p = appPath(folder, name)
		}
		mt, _ := fileMtimeOf(p)
		items = append(items, kakoItem{fmt.Sprintf("%011d", mt), name})
	}
	sortKakoItems(items)
	var buf strings.Builder
	for _, item := range items {
		p := archivedPath(folder, item.name)
		if p == "" {
			p = appPath(folder, item.name)
		}
		fmt.Fprintf(&buf, "%s%s%s\n", item.name, kakoSep, readThreadTitle(p))
	}
	return writeFileAtomic(appPath(folder, kakoList), buf.String())
}

// reverseFileLines はファイルの行を新しい順 (逆順) に返す。
func reverseFileLines(path string) []string {
	lines, _ := readFileLines(path)
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

func writeKako(folder, bodyTag, title0, amezoNameV, suffix, title string) {
	listPath := appPath(folder, kakoList)
	if !fileRegularP(listPath) {
		return
	}
	var out strings.Builder
	fmt.Fprintf(&out, "<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<link rel=\"stylesheet\" href=\"../style.css\">\n<title>%s%s%s 過去ログ</title></head>\n%s%s\n<font size=+2>%s%s%s 過去ログ</font>\n<div class=\"row links\"><a href=\"./\">板へ戻る</a></div>\n<font size=2>\n",
		title0, amezoNameV, suffix, copyrightText, bodyTag, title, amezoNameV, suffix)
	i := 0
	for _, line := range reverseFileLines(listPath) {
		p := strings.Index(line, kakoSep)
		if p < 0 {
			continue
		}
		name, ttl := line[:p], line[p+len(kakoSep):]
		if len(name) > 5 && startsWithP("o", name) && endsWithP(".html", name) &&
			digitsP(name[1:len(name)-5], 1, -1) {
			i++
			fmt.Fprintf(&out, " %d<a href=\"%s\">%s</a>\n", i, name, ttl)
		}
	}
	out.WriteString("</font>\n<hr></body></html>\n")
	writeFileAtomic(appPath(folder, kakoFile), out.String())
}

// ---------- スレッド走査 ----------
func threadFileNameP(name string) bool {
	return len(name) > 5 && endsWithP(".html", name) && digitsP(name[:len(name)-5], 1, -1)
}

// oldFileName は o数字.html / o数字.html.gz のどちらかなら 'o数字.html' を返す。
func oldFileName(name string) string {
	base := name
	if endsWithP(".gz", base) {
		base = base[:len(base)-3]
	}
	if len(base) > 6 && startsWithP("o", base) && endsWithP(".html", base) &&
		digitsP(base[1:len(base)-5], 1, -1) {
		return base
	}
	return ""
}

type threadItem struct{ mt, name string }

// scanThreads は (スレッド [(時刻文字列, 名前)] 新しい順, 過去ログ名のリスト) を返す。
func scanThreads(folder string, wantOld bool) ([]threadItem, []string) {
	var files []threadItem
	var oldNames []string
	oldSeen := map[string]bool{}
	for _, name := range listDirNames(appPath(folder)) {
		if threadFileNameP(name) {
			mt, _ := fileMtimeOf(appPath(folder, name))
			files = append(files, threadItem{fmt.Sprintf("%011d", mt), name})
		} else if wantOld {
			if o := oldFileName(name); o != "" && !oldSeen[o] {
				oldSeen[o] = true
				oldNames = append(oldNames, o)
			}
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].mt != files[j].mt {
			return files[i].mt > files[j].mt
		}
		return files[i].name > files[j].name
	})
	return files, oldNames
}

func countThreads(folder string) int {
	n := 0
	for _, name := range listDirNames(appPath(folder)) {
		if threadFileNameP(name) {
			n++
		}
	}
	if maxThreads > 0 && n > maxThreads {
		return maxThreads
	}
	return n
}

func totalPages(n int) int {
	p := (n + perPage - 1) / perPage
	if p < 1 {
		return 1
	}
	return p
}

func pageClamp(folder string, page int) int {
	if page <= 1 {
		return 1
	}
	if t := totalPages(countThreads(folder)); page > t {
		return t
	}
	return page
}

func pagerHTML(folder string, page, total int) string {
	if total <= 1 {
		return ""
	}
	q := urlEncode(folder)
	href := func(p int) string {
		if p == 1 {
			return "./"
		}
		return fmt.Sprintf("../amezo.cgi?folder=%s&amp;page=%d", q, p)
	}
	var nums []string
	lastP := 0
	for p := 1; p <= total; p++ {
		if total <= 10 || p == 1 || p == total || absInt(p-page) <= 2 {
			if lastP > 0 && p-lastP > 1 {
				nums = append(nums, "…")
			}
			if p == page {
				nums = append(nums, fmt.Sprintf("<b>[%d]</b>", p))
			} else {
				nums = append(nums, fmt.Sprintf("<a href=\"%s\">%d</a>", href(p), p))
			}
			lastP = p
		}
	}
	var segments []string
	if page > 1 {
		segments = append(segments, fmt.Sprintf("<a href=\"%s\">← 前の%d件</a>", href(page-1), perPage))
	}
	segments = append(segments, joinStrings(nums, " "))
	if page < total {
		segments = append(segments, fmt.Sprintf("<a href=\"%s\">次の%d件 →</a>", href(page+1), perPage))
	}
	return fmt.Sprintf("<div class=\"row links pager\">%s</div>\n", joinStrings(segments, "　"))
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// readThreadExcerpt は (先頭行, 最後の7レスのリスト, レス行数, 読めたか) を返す。
func readThreadExcerpt(path string) (string, []string, int, bool) {
	lines, ok := readFileLines(path)
	if !ok || len(lines) == 0 {
		return "", nil, 0, false
	}
	var body []string
	for _, l := range lines[1:] {
		if startsWithP("<form", l) {
			break
		}
		body = append(body, l)
	}
	last := body
	if len(last) > 7 {
		last = last[len(last)-7:]
	}
	return lines[0], last, len(body), true
}

// stripBoardTags は板 index 用に、先頭行から html/head/body/meta/link/title/script を取り除く。
func stripBoardTags(line string) string {
	s := line
	for {
		a := strings.Index(s, "<title>")
		if a < 0 {
			break
		}
		if b := strings.Index(s[a:], "</title>"); b >= 0 {
			s = s[:a] + s[a+b+8:]
		} else {
			s = s[:a]
		}
	}
	for {
		a := strings.Index(s, "<script")
		if a < 0 {
			break
		}
		if b := strings.Index(s[a:], "</script>"); b >= 0 {
			s = s[:a] + s[a+b+9:]
		} else {
			s = s[:a]
		}
	}
	var out strings.Builder
	i, n := 0, len(s)
	for i < n {
		if s[i] == '<' {
			end := strings.IndexByte(s[i:], '>')
			tag := false
			if end >= 0 {
				end += i
				insideEnd := end
				if insideEnd > i+8 {
					insideEnd = i + 8
				}
				inside := s[i+1 : insideEnd]
				for _, p := range []string{"html", "/html", "head", "/head", "body", "/body", "meta", "link"} {
					if startsWithP(p, inside) {
						tag = true
						break
					}
				}
				if tag {
					i = end + 1
					continue
				}
			}
			out.WriteByte(s[i])
			i++
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

type boardView struct {
	title0    string
	amezoName string
	suffix    string
	bgAttr    string
	color     string
	textAttr  string
	title     string
}

// boardViewInfo は regen 用の見た目情報。
func boardViewInfo(folder string) boardView {
	color, title := boardInfo(folder)
	suffix := ""
	if folder == "main" {
		suffix = "(メイン)"
	}
	bgAttr := ""
	if bang := strings.IndexByte(color, '!'); bang >= 0 {
		bgAttr = fmt.Sprintf(" background=\"%s\"", color[bang+1:])
		color = color[:bang]
	}
	textAttr := ""
	if endsWithP("*", color) {
		textAttr = " text=\"#ffffff\" link=\"red\" vlink=\"orange\""
		color = color[:len(color)-1]
	}
	title = replaceAll(title, "#c#", ",")
	title0, name := title, amezoName
	if a := strings.Index(title0, "alt=\""); a >= 0 {
		if b := strings.IndexByte(title0[a+5:], '"'); b >= 0 {
			title0 = title0[a+5 : a+5+b]
			name = ""
		}
	}
	return boardView{title0: title0, amezoName: name, suffix: suffix, bgAttr: bgAttr,
		color: color, textAttr: textAttr, title: title}
}

func writeIndexPage(out *strings.Builder, folder string, page, total int, files []threadItem, v boardView) {
	pager := pagerHTML(folder, page, total)
	start := (page - 1) * perPage
	fmt.Fprintf(out, "<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<link rel=\"stylesheet\" href=\"../style.css\">%s\n<title>%s%s%s</title></head>\n%s<body%s bgcolor=\"%s\"%s>\n<font size=+2>%s%s%s</font>\n",
		scriptTag, v.title0, v.amezoName, v.suffix, copyrightText, v.bgAttr, v.color, v.textAttr, v.title, v.amezoName, v.suffix)
	if header, ok := readFileString(appPath("header.html")); ok {
		out.WriteString(header)
	}
	fmt.Fprintf(out, "%s<font size=2>\n<!-- HEAD LINE -->\n", pager)
	for i := start; i < start+headlineCount && i < len(files); i++ {
		fname := files[i].name
		ttl := ""
		if lines, ok := readFileLines(appPath(folder, fname)); ok && len(lines) > 0 {
			if b, ok2 := extractFirstB(lines[0]); ok2 {
				ttl = b
			}
		}
		fmt.Fprintf(out, " %d<a href=\"%s\">%s</a>\n", i+1, fname, ttl)
	}
	out.WriteString("<!-- HEAD LINE END -->\n</font>\n")
	if kakoListSize(folder) > 0 {
		fmt.Fprintf(out, "<div class=\"row links\"><a href=\"%s\">過去ログ</a></div>\n", kakoFile)
	}
	for i := start; i < start+perPage && i < len(files); i++ {
		fname := files[i].name
		out.WriteString("<table border=1 cellspacing=1 cellpadding=2 width=100% bgcolor=\"#efefef\"><tr><td><font color=\"#000000\">\n")
		first, lines, nLines, _ := readThreadExcerpt(appPath(folder, fname))
		if first != "" {
			out.WriteString(stripBoardTags(first))
			out.WriteString("\n")
		}
		for _, l := range lines {
			out.WriteString(l)
			out.WriteString("\n")
		}
		resID := fname[:len(fname)-5]
		subTag := ""
		if nLines > 200 {
			subTag = fmt.Sprintf("<input type=text name=\"from\" value=\"%d\" size=\"4\"><input type=submit name=\"sub\" value=\"〜\"> ", nLines-98)
		}
		links := fmt.Sprintf("<div class=\"row links\"><a href=\"%s.html\">レス全部を見る</a> %s<a href=\"#top\">上へ</a> <a href=\"./\">リロード</a></div>", resID, subTag)
		fmt.Fprintf(out, "</font></td></tr></table>\n%s<hr>\n", replyForm(resID, links, threadIsLocked(folder, resID)))
	}
	fmt.Fprintf(out, "%s<hr></body></html>\n", pager)
}

func replaceIndex(tmpPath, indexPath string) {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(indexPath); err == nil {
		mode = fi.Mode().Perm()
	}
	chmodFile(tmpPath, mode)
	if err := os.Rename(tmpPath, indexPath); err != nil {
		_ = os.Remove(tmpPath)
		pageError("indexを更新できません.")
	}
}

// regenIndex は (index名, 状態) を返す。状態は "done" / "cached" / "busy"。
func regenIndex(folder string, page int, reloadFlag bool, useCache bool) (string, string) {
	if page < 1 {
		page = 1
	}
	v := boardViewInfo(folder)
	// ---- 2ページ目以降 ----
	if page > 1 {
		files, _ := scanThreads(folder, false)
		if maxThreads > 0 && len(files) > maxThreads {
			files = files[:maxThreads]
		}
		total := totalPages(len(files))
		if page > total {
			page = total
		}
		if page == 1 {
			return regenIndex(folder, 1, true, useCache)
		}
		indexName := fmt.Sprintf("index%d.html", page)
		indexPath := appPath(folder, indexName)
		if useCache && pageCache > 0 {
			if mt, ok := fileMtimeOf(indexPath); ok && epochNow()-mt < int64(pageCache) {
				return indexName, "cached"
			}
		}
		var buf strings.Builder
		writeIndexPage(&buf, folder, page, total, files, v)
		tmp := fmt.Sprintf("%s.tmp%d", indexPath, os.Getpid())
		writeFileString(tmp, buf.String())
		replaceIndex(tmp, indexPath)
		return indexName, "done"
	}
	// ---- 1ページ目 ----
	indexName := "index.html"
	indexPath := appPath(folder, indexName)
	if useCache && reloadFlag && postInterval > 0 {
		if mt, ok := fileMtimeOf(indexPath); ok && epochNow()-mt < int64(postInterval) {
			return indexName, "cached"
		}
	}
	lockPath := appPath(folder, "regen.lock")
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		pageError("ロックファイルを作成できません.")
	}
	defer lf.Close()
	locked := false
	if reloadFlag {
		locked = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
	} else {
		for i := 0; i < 100 && !locked; i++ {
			locked = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
			if !locked {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	if !locked {
		return indexName, "busy"
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	files, oldNames := scanThreads(folder, kakoListSize(folder) <= 0)
	var retired []kakoItem
	if maxThreads > 0 {
		var kept []threadItem
		n := 0
		for _, f := range files {
			n++
			if n > maxThreads {
				// 上限超過分は index から落とす。過去ログ化できたら retired へ
				res := f.name[:len(f.name)-5]
				if !archivedExists(folder, res) {
					if renamePath(appPath(folder, f.name), appPath(folder, "o"+f.name)) {
						retired = append(retired, kakoItem{f.mt, "o" + f.name})
						_ = os.Remove(appPath(folder, res+".lock"))
					}
				}
			} else {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	kakoChanged := false
	if kakoListSize(folder) <= 0 {
		if len(oldNames) > 0 || len(retired) > 0 {
			names := append([]string{}, oldNames...)
			for _, r := range retired {
				names = append(names, r.name)
			}
			kakoChanged = rebuildKakoList(folder, names)
		}
	} else if len(retired) > 0 {
		kakoChanged = appendKakoList(folder, retired)
	}
	hasKako := kakoListSize(folder) > 0
	var buf strings.Builder
	writeIndexPage(&buf, folder, 1, totalPages(len(files)), files, v)
	tmp := fmt.Sprintf("%s.tmp%d", indexPath, os.Getpid())
	if !writeFileString(tmp, buf.String()) {
		pageError("indexを書き込めません.")
	}
	replaceIndex(tmp, indexPath)
	if hasKako && (kakoChanged || !fileRegularP(appPath(folder, kakoFile))) {
		writeKako(folder,
			fmt.Sprintf("<body%s bgcolor=\"%s\"%s>", v.bgAttr, v.color, v.textAttr),
			v.title0, v.amezoName, v.suffix, v.title)
	}
	// 古い「次のページ」ファイルの掃除
	for _, name := range listDirNames(appPath(folder)) {
		if len(name) > 6 && startsWithP("index", name) && name[5] >= '0' && name[5] <= '9' && endsWithP(".html", name) {
			if mt, ok := fileMtimeOf(appPath(folder, name)); ok && epochNow()-mt > 600 {
				_ = os.Remove(appPath(folder, name))
			}
		}
	}
	return indexName, "done"
}
