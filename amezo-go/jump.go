package main

// jump.go - 署名つき中継ページ (Python 版 jump.py)

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const jumpStyle = ":root{color-scheme:light dark}body{font-family:sans-serif;max-width:40em;margin:1.5em auto;padding:0 1em;line-height:1.6}.url{word-break:break-all;border:1px solid #888;padding:.6em .8em;margin:.8em 0;font-family:monospace}.host{font-size:1.4em;font-weight:bold;word-break:break-all}.btns{margin:1.2em 0}.go,.back{display:inline-block;padding:.5em 1.6em;margin-right:.8em;border:1px solid #888;border-radius:6px;font-size:1em;text-decoration:none;cursor:pointer;background:transparent;color:inherit}.go{font-weight:bold}.note{font-size:.9em;opacity:.85}.warn{border:1px solid #c60;padding:.4em .7em;margin:.6em 0}.ad{margin:1.5em 0}.ad small{display:block;opacity:.7}.ad iframe{width:100%;height:260px;border:1px solid #888}"

func jumpPage(title, body, nonce string, withBack bool) {
	back := ""
	if withBack {
		back = fmt.Sprintf("<script nonce=\"%s\">document.getElementById(\"back\").addEventListener(\"click\",function(){if(history.length>1){history.back();}else{window.close();}});</script>", nonce)
	}
	bail(&response{
		status: 200,
		headers: []headerPair{
			{"Content-Type", "text/html; charset=UTF-8"},
			{"Cache-Control", "no-store"},
			{"X-Robots-Tag", "noindex, nofollow"},
			{"Referrer-Policy", "no-referrer"},
			{"X-Content-Type-Options", "nosniff"},
			{"Content-Security-Policy", fmt.Sprintf("default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-%s'; frame-src 'self'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'", nonce)},
		},
		body: fmt.Sprintf("<!DOCTYPE html><html lang=\"ja\"><head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><meta name=\"robots\" content=\"noindex, nofollow\"><meta name=\"referrer\" content=\"no-referrer\"><title>%s</title><style>%s</style></head><body><h2>%s</h2>%s%s</body></html>",
			htmlEscape(title), jumpStyle, htmlEscape(title), body, back),
	})
}

// parseHost は URL からホスト名を取り出す。取れなければ ""。
func parseHost(url string) string {
	p := strings.Index(url, "://")
	if p < 0 {
		return ""
	}
	rest := url[p+3:]
	authEnd := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' || rest[i] == '?' || rest[i] == '#' {
			authEnd = i
			break
		}
	}
	auth := rest[:authEnd]
	if at := strings.LastIndexByte(auth, '@'); at >= 0 {
		auth = auth[at+1:]
	}
	var host string
	if startsWithP("[", auth) {
		if e := strings.IndexByte(auth, ']'); e >= 0 {
			host = auth[:e+1]
		} else {
			host = auth
		}
	} else if c := strings.IndexByte(auth, ':'); c >= 0 {
		host = auth[:c]
	} else {
		host = auth
	}
	return host
}

func handleJump(req *request) *response {
	return runHandler(func() *response {
		jumpMain(req)
		return nil
	})
}

func jumpMain(req *request) {
	currentRequest = req
	nonce := hexEncode(randomOctets(12))
	params := parseParams(req.queryString)
	u := formGet(params, "u")
	s := formGet(params, "s")
	url := ""
	urlOK := false
	if len(u) <= 3000 {
		if raw := b64urlDecode(u); raw != nil && utf8.Valid(raw) {
			url, urlOK = string(raw), true
		}
	}
	if !urlOK || !jumpVerify(url, s) {
		body := "<p>このリンクは、署名が合わないため、自動では開きません。<br>掲示板に投稿された URL ではないか、投稿のあとに署名の鍵が変わった可能性があります。</p>"
		if urlOK && urlIsValid(url) {
			body += fmt.Sprintf("<p class=\"note\">URL (リンクにはしていません。確かめてから、自分で開いてください):</p><div class=\"url\">%s</div>", htmlEscape(url))
		}
		body += "<div class=\"btns\"><button type=\"button\" class=\"back\" id=\"back\">戻る</button></div>"
		jumpPage("このリンクは使えません", body, nonce, true)
	}
	host := parseHost(url)
	if host == "" {
		host = "(ホスト名を読み取れません)"
	}
	body := fmt.Sprintf("<p>この掲示板に投稿された、外部サイトへのリンクです。移動先を確かめてから、「進む」を押してください。</p><p class=\"note\">移動先のサイト</p><div class=\"host\">%s</div><p class=\"note\">URL 全体</p><div class=\"url\">%s</div>",
		htmlEscape(host), htmlEscape(url))
	if strings.Contains(strings.ToLower(host), "xn--") {
		body += "<div class=\"warn\">ドメイン名に <code>xn--</code> が含まれています。見た目が似た別のサイトのことがあるので、特に注意してください。</div>"
	}
	body += fmt.Sprintf("<div class=\"btns\"><a class=\"go\" href=\"%s\" rel=\"noopener noreferrer nofollow\" referrerpolicy=\"no-referrer\">進む</a><button type=\"button\" class=\"back\" id=\"back\">戻る</button></div><p class=\"note\">このページは自動では転送しません。リンク先の内容について、掲示板は責任を負いません。</p>",
		htmlEscape(url))
	if fileRegularP(appPath("ad.html")) {
		body += "<div class=\"ad\"><small>広告</small><iframe src=\"ad.html\" sandbox=\"allow-scripts allow-popups\" referrerpolicy=\"no-referrer\" loading=\"lazy\" title=\"広告\"></iframe></div>"
	}
	jumpPage("外部サイトへ移動します", body, nonce, true)
}
