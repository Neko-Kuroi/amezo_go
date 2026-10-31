package main

// config.go - 設定値と config.ini (Python 版 amezo.config_load と同じ検査)

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

var copyrightText = "<!--\n    Script for amezo-like BBS (Go Version)\n    Original Copyright (C) 1999-2003 amezo_\n    http://www17.big.or.jp/~amezo_/\n    http://ame.x0.com/\n-->\n"

var (
	amezoName           = "＠あめぞう"
	urlbase             = "http://ame.x0.com/"
	adminName           = "サポート"
	postInterval        = 5
	globalWindow        = 180
	maxThreads          = 200
	jumpEnabled         = true
	archiveQuietSeconds = 600
	kakoFile            = "kako.html"
	kakoList            = "kako.txt"
	kakoSep             = "<>"
	maxPostBytes        = 7500
	subjMax             = 45
	nameMax             = 25
	mailMax             = 50
	commMax             = 400
	commLinesMax        = 20
	pageCache           = 10
	perPage             = 20
	headlineCount       = 100
	trustedProxies      = []string{"127.0.0.1", "::1"}
	delcryptFile        = "delpass.txt"
	hashIterations      = 100000
	tripSecretFile      = "trip_secret.txt"
	idSecretFile        = "id_secret.txt"
	idLen               = 8
	scriptTag           = `<script src="../hide.js" defer></script>`
	idCtx               = "id:"
	rlCtx               = "rl:"
	tripPassMax         = 100
	tripMark            = "◆"
	capMark             = "★"
	capFile             = "cap.txt"
	keySecretFile       = "key_secret.txt"
	keyCtx              = "key:"
	keysFile            = "keys.txt"
	keyAlphabet         = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	jumpSecretFile      = "jump_secret.txt"
	jumpCtx             = "jump:"
	jumpSigLen          = 22
	urlMaxLen           = 2000
)

// config.ini のスキーマ: 名前 -> (型 既定値 最小 最大)
type configSpec struct {
	name string
	typ  string
	def  any
	min  int
	max  int
}

func configSchema() []configSpec {
	return []configSpec{
		{"AMEZO", "text", "＠あめぞう", 0, 40},
		{"URLBASE", "url", "http://ame.x0.com/", 0, 200},
		{"ADMIN", "text", "サポート", 1, 20},
		{"POST_INTERVAL", "int", 5, 0, 3600},
		{"GLOBAL_WINDOW", "int", 180, 0, 86400},
		{"MAX_THREADS", "int", 200, 0, 100000},
		{"PAGE_CACHE", "int", 10, 0, 3600},
		{"MAX_POST_BYTES", "int", 7500, 1000, 1000000},
		{"SUBJ_MAX", "int", 45, 1, 500},
		{"NAME_MAX", "int", 25, 1, 200},
		{"MAIL_MAX", "int", 50, 1, 500},
		{"COMM_MAX", "int", 400, 1, 10000},
		{"COMM_LINES_MAX", "int", 20, 1, 1000},
		{"HASH_ITERATIONS", "int", 100000, 1000, 5000000},
		{"TRUSTED_PROXIES", "ips", []string{"127.0.0.1", "::1"}, 0, 16},
		{"JUMP_ENABLED", "bool", true, 0, 1},
		{"ARCHIVE_QUIET_SECONDS", "int", 600, 0, 604800},
	}
}

// configCheck は1つの値を検査する。(型を直した値, 通ったか, 理由)
func configCheck(typ string, v any, min, max int) (any, bool, string) {
	switch typ {
	case "int":
		s, isStr := v.(string)
		if !isStr {
			return nil, false, "整数ではありません"
		}
		s = trimSpace(s)
		body := strings.TrimPrefix(s, "-")
		ok := len(s) > 0 && utf8.RuneCountInString(s) <= 12 && body != "" && digitsP(body, 1, -1)
		if !ok {
			return nil, false, "整数ではありません"
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, false, "整数ではありません"
		}
		if n < min || n > max {
			return nil, false, fmt.Sprintf("%d〜%d の範囲外です", min, max)
		}
		return n, true, ""
	case "bool":
		s, isStr := v.(string)
		if !isStr {
			return nil, false, "true / false ではありません"
		}
		switch strings.ToLower(trimSpace(s)) {
		case "1", "true", "yes", "on":
			return true, true, ""
		case "0", "false", "no", "off":
			return false, true, ""
		}
		return nil, false, "true / false ではありません"
	case "text":
		s, isStr := v.(string)
		if !isStr {
			return nil, false, "文字列ではありません"
		}
		for _, c := range s {
			if c <= 0x1f || c == 0x7f || strings.ContainsRune(`<>"'&$\`, c) {
				return nil, false, "使えない文字 (制御文字 < > \" ' & $ \\) を含みます"
			}
		}
		if n := utf8.RuneCountInString(s); n < min || n > max {
			return nil, false, fmt.Sprintf("%d〜%d 文字ではありません", min, max)
		}
		return s, true, ""
	case "url":
		s, isStr := v.(string)
		if !isStr {
			return nil, false, "http:// か https:// で始まる URL ではありません"
		}
		if len(s) > max {
			return nil, false, "http:// か https:// で始まる URL ではありません"
		}
		if s == "" {
			return s, true, ""
		}
		i := 0
		switch {
		case startsWithP("http://", s):
			i = 7
		case startsWithP("https://", s):
			i = 8
		default:
			return nil, false, "http:// か https:// で始まる URL ではありません"
		}
		ok := i < len(s) && isAlnumByte(s[i])
		if ok {
			for j := i; j < len(s); j++ {
				c := s[j]
				if !isAlnumByte(c) && !strings.ContainsRune(".-:/_", rune(c)) {
					ok = false
					break
				}
			}
		}
		if !ok {
			return nil, false, "http:// か https:// で始まる URL ではありません"
		}
		return s, true, ""
	case "ips":
		if s, isStr := v.(string); isStr {
			if s == "" {
				return []string{}, true, ""
			}
			return nil, false, "IP アドレスの並びではありません"
		}
		list, isList := v.([]string)
		if !isList || len(list) > max {
			return nil, false, "IP アドレスの並びではありません"
		}
		out := make([]string, 0, len(list))
		for _, ip := range list {
			t := trimSpace(ip)
			if !ipValidP(t) {
				return nil, false, "IP アドレスではない値があります"
			}
			out = append(out, t)
		}
		return out, true, ""
	}
	return nil, false, "未対応の型です"
}

func isAlnumByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// iniCleanValue は INI の値の後処理: 引用符を外し、';' コメントを取り、空白を詰める。
func iniCleanValue(v string) string {
	if len(v) > 0 && v[0] == '"' {
		if end := strings.IndexByte(v[1:], '"'); end >= 0 && end+1 > 0 {
			return v[1 : 1+end]
		}
		return v
	}
	if p := strings.Index(v, " ;"); p >= 0 {
		v = v[:p]
	}
	if p := strings.Index(v, "\t;"); p >= 0 {
		v = v[:p]
	}
	return trimSpace(v)
}

type iniPair struct {
	key string
	val any // string または []string
}

// parseIniRaw は INI の生の値を読む (展開はしない)。書き方の誤りがあれば ok=false。
func parseIniRaw(text string) ([]iniPair, bool) {
	var raw []iniPair
	find := func(k string) int {
		for i := range raw {
			if raw[i].key == k {
				return i
			}
		}
		return -1
	}
	for _, line := range splitString(text, '\n') {
		s := trimSpace(strings.TrimRight(line, "\r"))
		if s == "" || s[0] == ';' || s[0] == '#' {
			continue
		}
		if s[0] == '[' || !strings.Contains(s, "=") {
			return nil, false
		}
		k, v, _ := splitOnce(s, '=')
		k, v = trimSpace(k), trimSpace(v)
		if k == "" {
			return nil, false
		}
		isList := endsWithP("[]", k)
		if isList {
			k = trimSpace(k[:len(k)-2])
		}
		v = iniCleanValue(v)
		prev := find(k)
		switch {
		case isList && prev >= 0:
			if lst, ok := raw[prev].val.([]string); ok {
				raw[prev].val = append(lst, v)
			} else {
				raw[prev].val = []string{raw[prev].val.(string), v}
			}
		case isList:
			raw = append(raw, iniPair{k, []string{v}})
		case prev >= 0:
			raw[prev].val = v
		default:
			raw = append(raw, iniPair{k, v})
		}
	}
	return raw, true
}

// configLoad は config.ini を読み、検査を通った値だけを返す。(値 無視した理由のリスト)
func configLoad(path string) (map[string]any, []string) {
	if !fileRegularP(path) {
		return map[string]any{}, nil
	}
	text, ok := readFileString(path)
	if !ok {
		return map[string]any{}, []string{"読み込めません (書き方の誤り)"}
	}
	raw, ok := parseIniRaw(text)
	if !ok {
		return map[string]any{}, []string{"読み込めません (書き方の誤り)"}
	}
	schema := configSchema()
	out := map[string]any{}
	var errors []string
	for _, kv := range raw {
		var spec *configSpec
		for i := range schema {
			if schema[i].name == kv.key {
				spec = &schema[i]
				break
			}
		}
		if spec == nil {
			errors = append(errors, fmt.Sprintf("不明なキー: %s", kv.key))
			continue
		}
		val, ok, reason := configCheck(spec.typ, kv.val, spec.min, spec.max)
		if ok {
			out[kv.key] = val
		} else {
			errors = append(errors, fmt.Sprintf("%s: %s", kv.key, reason))
		}
	}
	return out, errors
}

// loadConfig は config.ini を読み、検査を通った値だけを反映する (Python 版の起動時処理と同じ)。
func loadConfig() {
	values, errors := configLoad(appPath("config.ini"))
	for _, e := range errors {
		fmt.Fprintf(os.Stderr, "amezo: config.ini の %s (無視して、既定値を使います)\n", e)
	}
	for key, val := range values {
		switch key {
		case "AMEZO":
			amezoName = val.(string)
		case "URLBASE":
			urlbase = val.(string)
		case "ADMIN":
			adminName = val.(string)
		case "POST_INTERVAL":
			postInterval = val.(int)
		case "GLOBAL_WINDOW":
			globalWindow = val.(int)
		case "MAX_THREADS":
			maxThreads = val.(int)
		case "PAGE_CACHE":
			pageCache = val.(int)
		case "MAX_POST_BYTES":
			maxPostBytes = val.(int)
		case "SUBJ_MAX":
			subjMax = val.(int)
		case "NAME_MAX":
			nameMax = val.(int)
		case "MAIL_MAX":
			mailMax = val.(int)
		case "COMM_MAX":
			commMax = val.(int)
		case "COMM_LINES_MAX":
			commLinesMax = val.(int)
		case "HASH_ITERATIONS":
			hashIterations = val.(int)
		case "TRUSTED_PROXIES":
			trustedProxies = val.([]string)
		case "JUMP_ENABLED":
			jumpEnabled = val.(bool)
		case "ARCHIVE_QUIET_SECONDS":
			archiveQuietSeconds = val.(int)
		}
	}
	if fileRegularP(appPath("config.php")) {
		fmt.Fprintln(os.Stderr, "amezo: config.php は読み込みません (実行される形式の設定は使いません)。config.ini に移してください")
	}
}
