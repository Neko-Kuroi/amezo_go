package main

// util.go - 文字列・ファイル・時刻・flock などの土台

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// ---------- 文字列 ----------
func splitString(s string, delim byte) []string { return strings.Split(s, string(delim)) }

func splitStringNoEmpty(s string, delim byte) []string {
	parts := strings.Split(s, string(delim))
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitOnce は最初の delim で2つに分ける。無ければ (全体, "", false)。
func splitOnce(s string, delim byte) (string, string, bool) {
	i := strings.IndexByte(s, delim)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func joinStrings(list []string, sep string) string { return strings.Join(list, sep) }

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	return strings.ReplaceAll(s, old, new)
}

func startsWithP(prefix, s string) bool { return strings.HasPrefix(s, prefix) }
func endsWithP(suffix, s string) bool   { return strings.HasSuffix(s, suffix) }

func trimSpace(s string) string { return strings.Trim(s, " \t\n\r\u3000") }

// digitsP は s が minLen..maxLen 文字の数字だけからなるか (maxLen < 0 で上限なし)。
func digitsP(s string, minLen, maxLen int) bool {
	n := utf8.RuneCountInString(s)
	if n < minLen || (maxLen >= 0 && n > maxLen) {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func allDigits(s string) bool { return digitsP(s, 1, -1) }

// safeInteger は整数として読めなければ def を返す。
func safeInteger(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func stringNthField(s string, index int, delim byte) string {
	parts := splitString(s, delim)
	if index < len(parts) {
		return parts[index]
	}
	return ""
}

// ---------- HTML ----------
var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	`"`, "&quot;",
	"<", "&lt;",
	">", "&gt;",
	"'", "&#39;",
)

func htmlEscape(s string) string { return htmlEscaper.Replace(s) }

func htmlUnescape(s string) string {
	r := s
	r = replaceAll(r, "&lt;", "<")
	r = replaceAll(r, "&gt;", ">")
	r = replaceAll(r, "&quot;", `"`)
	r = replaceAll(r, "&#39;", "'")
	r = replaceAll(r, "&amp;", "&")
	return r
}

// stripTags は単純なタグ除去 (admin の題名の平文用)。
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, c := range s {
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag:
			b.WriteRune(c)
		}
	}
	return b.String()
}

var htmlEntities = []string{"&amp;", "&quot;", "&lt;", "&gt;", "&#39;"}

// entityAt は位置 i (バイト) から HTML エンティティが始まればその長さ (バイト)。
func entityAt(s string, i int) int {
	for _, e := range htmlEntities {
		if strings.HasPrefix(s[i:], e) {
			return len(e)
		}
	}
	return 0
}

// htmlCharsLen は Python 版 html_chars_len: エンティティを1文字として数える。
func htmlCharsLen(s string) int {
	n, i := 0, 0
	for i < len(s) {
		if e := entityAt(s, i); e > 0 {
			i += e
		} else {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
		n++
	}
	return n
}

// ccut は Python 版 ccut: エンティティを1文字として maxLen 文字まで。
func ccut(s string, maxLen int) string {
	n, i := 0, 0
	for i < len(s) && n < maxLen {
		if e := entityAt(s, i); e > 0 {
			i += e
		} else {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
		n++
	}
	return s[:i]
}

// stripInvisible は Python 版 _INVISIBLE の文字を取り除く。
func stripInvisible(s string) string {
	return strings.Map(func(c rune) rune {
		code := c
		switch {
		case code >= 0x0080 && code <= 0x009F, code == 0x00AD, code == 0x034F, code == 0x061C,
			code >= 0x115F && code <= 0x1160, code >= 0x17B4 && code <= 0x17B5,
			code >= 0x180B && code <= 0x180E, code == 0x200B, code == 0x200C,
			code >= 0x200E && code <= 0x200F, code >= 0x2028 && code <= 0x202E,
			code >= 0x2060 && code <= 0x206F, code == 0x3164, code == 0xFEFF,
			code == 0xFFA0, code >= 0xFFF9 && code <= 0xFFFB:
			return -1
		}
		return c
	}, s)
}

// ---------- URL ----------
// utf8Decode は octet 列を文字列にする。壊れた部分は置換文字 (U+FFFD)。
func utf8Decode(octs []byte) string {
	var b strings.Builder
	for len(octs) > 0 {
		c, size := utf8.DecodeRune(octs)
		if c == utf8.RuneError && size == 1 {
			b.WriteRune('\uFFFD')
			octs = octs[1:]
			continue
		}
		b.WriteRune(c)
		octs = octs[size:]
	}
	return b.String()
}

func hexVal(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// urlDecodeBytes は URL エンコードされたバイト列を、+ を空白として、UTF-8 で戻す。
func urlDecodeBytes(bytes []byte) string {
	out := make([]byte, 0, len(bytes))
	for i := 0; i < len(bytes); i++ {
		b := bytes[i]
		switch {
		case b == '+':
			out = append(out, ' ')
		case b == '%' && i+2 < len(bytes):
			hi, ok1 := hexVal(bytes[i+1])
			lo, ok2 := hexVal(bytes[i+2])
			if ok1 && ok2 {
				out = append(out, byte(hi*16+lo))
				i += 2
			} else {
				out = append(out, b)
			}
		default:
			out = append(out, b)
		}
	}
	return utf8Decode(out)
}

func urlDecode(x string) string { return urlDecodeBytes([]byte(x)) }

// urlEncode は Python の urllib.parse.quote(s, safe=”) 相当。
func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ---------- 時刻 (JST 固定。Python 版と同じく OS の TZ に頼らない) ----------
var jstZone = time.FixedZone("JST", 9*3600)

func epochNow() int64 { return time.Now().Unix() }

// epochToJST はエポック秒 -> (年 月 日 時 分 秒 曜日) 曜日は月曜=0 (Python の weekday())。
func epochToJST(epoch int64) (int, int, int, int, int, int, int) {
	t := time.Unix(epoch, 0).In(jstZone)
	wd := (int(t.Weekday()) + 6) % 7
	return t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), wd
}

// jstToEpoch は JST の日時 -> エポック秒 (admin の日時戻し用)。
func jstToEpoch(year, month, day, hour, min, sec int) int64 {
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, jstZone).Unix()
}

var wdays = []string{"月", "火", "水", "木", "金", "土", "日"}

// formatJstDate は Python 版 date_str: 20yy年mm月dd日(曜)HH時MM分SS秒
func formatJstDate(epoch int64) string {
	y, m, d, hh, mm, ss, wd := epochToJST(epoch)
	return fmt.Sprintf("%d年%02d月%02d日(%s)%02d時%02d分%02d秒", y, m, d, wdays[wd], hh, mm, ss)
}

// formatResNumber はスレッド番号: yyMMddHHmmss (JST)
func formatResNumber(epoch int64) string {
	y, m, d, hh, mm, ss, _ := epochToJST(epoch)
	return fmt.Sprintf("%02d%02d%02d%02d%02d%02d", y%100, m, d, hh, mm, ss)
}

func formatAdminTime(epoch int64) string {
	y, m, d, hh, mm, _, _ := epochToJST(epoch)
	return fmt.Sprintf("%d-%02d-%02d %02d:%02d", y, m, d, hh, mm)
}

// ---------- ファイル ----------
var appDir string

// appPath は掲示板フォルダ (appDir) からの相対パスを組み立てる。
func appPath(parts ...string) string {
	base := appDir
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + strings.Join(parts, "/")
}

func fileRegularP(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func dirExistsP(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// ensureDir はディレクトリを作る (既にあれば何もしない)。親までは作らない。
func ensureDir(path string) bool {
	_ = os.Mkdir(path, 0o755)
	return dirExistsP(path)
}

func statOf(path string) (*syscall.Stat_t, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return st, ok
}

func fileSizeOf(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}

func fileMtimeOf(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.ModTime().Unix(), true
}

func fileCtimeOf(path string) (int64, bool) {
	st, ok := statOf(path)
	if !ok {
		return 0, false
	}
	return st.Ctim.Sec, true
}

func chmodFile(path string, mode os.FileMode) {
	_ = os.Chmod(path, mode)
}

// setFileMtime は更新日時 (とアクセス日時) をエポック秒に設定する。
func setFileMtime(path string, epoch int64) bool {
	t := time.Unix(epoch, 0)
	return os.Chtimes(path, t, t) == nil
}

func renamePath(from, to string) bool { return os.Rename(from, to) == nil }
func unlinkPath(path string) bool     { return os.Remove(path) == nil }

// listDirNames はディレクトリ内の名前の一覧。無ければ nil。
func listDirNames(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func readFileOctets(path string) ([]byte, error) { return os.ReadFile(path) }

// readFileString は UTF-8 で読む。失敗したら (def, false)。
func readFileString(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return utf8Decode(b), true
}

// readFileLines は行の一覧 (改行と末尾の CR を除く)。失敗したら (nil, false)。
func readFileLines(path string) ([]string, bool) {
	s, ok := readFileString(path)
	if !ok {
		return nil, false
	}
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines, true
}

func writeFileString(path, content string) bool {
	return os.WriteFile(path, []byte(content), 0o644) == nil
}

func writeFileOctets(path string, octs []byte) bool {
	return os.WriteFile(path, octs, 0o644) == nil
}

func appendFileString(path, content string) bool {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err == nil
}

// writeFileAtomic は一時ファイルに書いて rename する (Python 版の os.replace 相当)。既存ファイルのモードを保つ。
func writeFileAtomic(path, content string) bool {
	tmp := fmt.Sprintf("%s.tmp%d", path, os.Getpid())
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if !writeFileString(tmp, content) {
		return false
	}
	chmodFile(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	return true
}

// ---------- flock ----------
func fdLock(f *os.File, nonblocking bool) bool {
	op := syscall.LOCK_EX
	if nonblocking {
		op |= syscall.LOCK_NB
	}
	return syscall.Flock(int(f.Fd()), op) == nil
}

func fdUnlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// callWithFdLock は PATH を flock して fn を呼ぶ。timeout 秒まで NB で再試行する。
// ロックが取れなければ busy=true を返す (fn は呼ばれない)。
func callWithFdLock(path string, timeout int, createMode os.FileMode, fn func(f *os.File)) (busy bool) {
	if createMode == 0 {
		createMode = 0o644
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, createMode)
	if err != nil {
		return true
	}
	defer f.Close()
	deadline := epochNow() + int64(timeout)
	got := false
	for {
		got = fdLock(f, true)
		if got || epochNow() >= deadline {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !got {
		return true
	}
	defer fdUnlock(f)
	fn(f)
	return false
}

// ---------- IP ----------
func ipv4ValidP(s string) bool {
	parts := splitString(s, '.')
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if !digitsP(p, 1, 3) {
			return false
		}
		if len(p) > 1 && p[0] == '0' {
			return false
		}
		if n, _ := strconv.Atoi(p); n > 255 {
			return false
		}
	}
	return true
}

// ipv6Groups は IPv6 文字列を 8 グループ (16bit 値) に展開する。壊れていれば nil。末尾の IPv4 表記にも対応。
func ipv6Groups(s string) []int {
	str := s
	if idx := strings.LastIndex(str, ":"); idx >= 0 && strings.Contains(str, ".") {
		v4 := str[idx+1:]
		if ipv4ValidP(v4) {
			p := splitString(v4, '.')
			n := make([]int, 4)
			for i := range p {
				n[i], _ = strconv.Atoi(p[i])
			}
			str = fmt.Sprintf("%s%x:%x", str[:idx+1], n[0]*256+n[1], n[2]*256+n[3])
		}
	}
	validGroup := func(g string) bool {
		if len(g) < 1 || len(g) > 4 {
			return false
		}
		for _, c := range g {
			if _, ok := hexVal(byte(c)); !ok {
				return false
			}
		}
		return true
	}
	parse := func(gs []string) []int {
		out := make([]int, 0, len(gs))
		for _, g := range gs {
			v, err := strconv.ParseInt(g, 16, 32)
			if err != nil {
				return nil
			}
			out = append(out, int(v))
		}
		return out
	}
	if dbl := strings.Index(str, "::"); dbl >= 0 {
		var left, right []string
		if dbl > 0 {
			left = splitString(str[:dbl], ':')
		}
		if rest := str[dbl+2:]; rest != "" {
			right = splitString(rest, ':')
		}
		if len(left)+len(right) > 7 {
			return nil
		}
		for _, g := range append(append([]string{}, left...), right...) {
			if !validGroup(g) {
				return nil
			}
		}
		zeros := make([]int, 8-len(left)-len(right))
		out := parse(left)
		out = append(out, zeros...)
		out = append(out, parse(right)...)
		if len(out) != 8 {
			return nil
		}
		return out
	}
	gs := splitString(str, ':')
	if len(gs) != 8 {
		return nil
	}
	for _, g := range gs {
		if !validGroup(g) {
			return nil
		}
	}
	return parse(gs)
}

func ipValidP(s string) bool {
	if s == "" {
		return false
	}
	if ipv4ValidP(s) {
		return true
	}
	return strings.Contains(s, ":") && ipv6Groups(s) != nil
}

// ipUnit は Python 版 ip_unit: IPv6 は上位64bit の hex、IPv4 はそのまま。
func ipUnit(ip string) string {
	if strings.Contains(ip, ":") {
		if gs := ipv6Groups(ip); gs != nil {
			var b strings.Builder
			for _, g := range gs[:4] {
				fmt.Fprintf(&b, "%04x", g)
			}
			return b.String()
		}
	}
	return ip
}

// ---------- その他 ----------
func getenvDefault(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func copyFileSimple(from, to string) bool {
	b, err := os.ReadFile(from)
	if err != nil {
		return false
	}
	return writeFileOctets(to, b)
}
