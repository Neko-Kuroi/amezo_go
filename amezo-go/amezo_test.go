package main

// amezo_test.go - 関数レベルのテスト (go test)。Lisp 版 unit-test.lisp の移植。

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

var okCount, ngCount int

func chk(t *testing.T, name string, cond bool, extra string) {
	if cond {
		okCount++
		t.Logf("OK %s", name)
	} else {
		ngCount++
		t.Errorf("NG %s %s", name, extra)
	}
}

func chkEq(t *testing.T, name, got, want string) {
	chk(t, name, got == want, fmt.Sprintf("got=%q want=%q", got, want))
}

func TestUnit(t *testing.T) {
	appDir = t.TempDir() + "/"
	writeFileString(appPath("blist.txt"), "main,#ddeedd,広場\nzatsu,#ffeedd,雑談\ntest,#ffffff,テスト\n")
	ensureDir(appPath("main"))
	loadConfig()

	// ---- SHA-256 / HMAC / PBKDF2 (既知のベクタ) ----
	chkEq(t, "sha256 abc", sha256Hex(utf8Bytes("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	chkEq(t, "sha256 空", sha256Hex(utf8Bytes("")), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	chkEq(t, "sha256 長い", sha256Hex(utf8Bytes("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")),
		"248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1")
	chkEq(t, "hmac RFC4231-1",
		hmacSHA256Hex(bytes.Repeat([]byte{0x0b}, 20), utf8Bytes("Hi There")),
		"b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7")
	chkEq(t, "pbkdf2 1回", hexEncode(pbkdf2SHA256(utf8Bytes("password"), utf8Bytes("salt"), 1, 32)),
		"120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b")
	chkEq(t, "pbkdf2 2回", hexEncode(pbkdf2SHA256(utf8Bytes("password"), utf8Bytes("salt"), 2, 32)),
		"ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43")

	// ---- base64 / base32 ----
	chkEq(t, "base64", base64Encode(utf8Bytes("hello")), "aGVsbG8=")
	chkEq(t, "base64url decode", utf8Decode(b64urlDecode("aHR0cHM6Ly9leGFtcGxlLmNvbS8_YT0xJmI9Mg")), "https://example.com/?a=1&b=2")
	chk(t, "b64url 不正文字は nil", b64urlDecode("a+b/c") == nil, "")
	chkEq(t, "base32 15バイト", base32Encode(make([]byte, 15)), "AAAAAAAAAAAAAAAAAAAAAAAA")

	// ---- パスワードハッシュ (Python 版と相互運用) ----
	chk(t, "verify: Python で作ったハッシュが通る",
		verifyHash("adminpw", "pbkdf2_sha256$100000$00112233445566778899aabbccddeeff$98f2efc2fb0e7cb91857ece58878bd93a63f06ed7e166fdbdddcf780bfbf96dd"), "")
	chk(t, "verify: 間違いは通らない",
		!verifyHash("wrongpw", "pbkdf2_sha256$100000$00112233445566778899aabbccddeeff$98f2efc2fb0e7cb91857ece58878bd93a63f06ed7e166fdbdddcf780bfbf96dd"), "")
	chk(t, "make-hash 往復", verifyHash("ひみつ", makeHash("ひみつ")), "")

	// ---- トリップ / ID (鍵を固定して Python 版の計算と一致するか) ----
	secret := strings.Repeat("aa", 32) + "\n"
	for _, f := range []string{tripSecretFile, idSecretFile, jumpSecretFile, keySecretFile} {
		writeFileString(appPath(f), secret)
	}
	chkEq(t, "トリップ", makeTrip("testpw"), "LzM4cgqcw.")
	chkEq(t, "トリップ: 空はなし", makeTrip(""), "")
	chkEq(t, "ID", makeID("127.0.0.1", "main", 1793433455), "8KeqkkDP")
	chkEq(t, "JST 日時", formatJstDate(1793433455), "2026年10月31日(土)16時57分35秒")
	chkEq(t, "スレッド番号", formatResNumber(1793433455), "261031165735")

	// ---- 中継リンク ----
	chkEq(t, "jump 署名", jumpSign("https://example.com/?a=1&b=2"), "YcP70X8lmTkV4tYoh5YGrM")
	chk(t, "jump 照合", jumpVerify("https://example.com/?a=1&b=2", "YcP70X8lmTkV4tYoh5YGrM"), "")
	chk(t, "jump 改ざんは通らない", !jumpVerify("https://example.com/?a=1&b=2", "AAAAAAAAAAAAAAAAAAAAAA"), "")
	chkEq(t, "linkify",
		linkifyUrls("見て https://example.com/?a=1&amp;b=2 ね", true),
		"見て <a href=\"../jump.cgi?u=aHR0cHM6Ly9leGFtcGxlLmNvbS8_YT0xJmI9Mg&amp;s=YcP70X8lmTkV4tYoh5YGrM\" target=\"_blank\" rel=\"noopener noreferrer nofollow\">https://example.com/?a=1&amp;b=2</a> ね")
	chk(t, "url_is_valid: javascript は不可", !urlIsValid("javascript:alert(1)"), "")

	// ---- 認証キー ----
	kid, disp := keyIssue("main", "テスト", "260101000000")
	chk(t, "鍵: 発行できる", kid != "" && len(disp) == 29, fmt.Sprintf("kid=%q disp=%q", kid, disp))
	chk(t, "鍵: 照合できる", keyVerify("main", "260101000000", disp), "")
	chk(t, "鍵: 範囲外は不可", !keyVerify("main", "260101000001", disp), "")
	chk(t, "鍵: 失効できる", keyRevoke("main", kid), "")
	chk(t, "鍵: 失効後は不可", !keyVerify("main", "260101000000", disp), "")
	chkEq(t, "鍵: ハッシュ", keyHash("ABCD2345EFGH2345IJKL2345"),
		"f22f36da875f3f733d0214c3e77019e9e9b61c50b160a6e2c5fba07d554e3670")

	// ---- config.ini の検査 ----
	_, okInt, _ := configCheck("int", "42", 0, 100)
	chk(t, "config: 整数", okInt, "")
	_, ngRange, _ := configCheck("int", "4200", 0, 100)
	chk(t, "config: 範囲外", !ngRange, "")
	defaultsOK := true
	for _, spec := range configSchema() {
		var v any
		switch spec.typ {
		case "ips":
			v = spec.def
		case "bool":
			if spec.def.(bool) {
				v = "true"
			} else {
				v = "false"
			}
		default:
			v = fmt.Sprintf("%v", spec.def)
		}
		if _, ok, _ := configCheck(spec.typ, v, spec.min, spec.max); !ok {
			defaultsOK = false
		}
	}
	chk(t, "config: 表の既定値は自分の検査を通る", defaultsOK, "")

	// ---- ページ送り・文字数 ----
	chk(t, "html-chars-len", htmlCharsLen("a&amp;b&lt;") == 4, fmt.Sprint(htmlCharsLen("a&amp;b&lt;")))
	chkEq(t, "ccut", ccut("あ&amp;いう", 2), "あ&amp;")
	chkEq(t, "ip-unit IPv6", ipUnit("2001:db8:85a3::8a2e:370:7334"), "20010db885a30000")
	chkEq(t, "ip-unit IPv4", ipUnit("192.168.0.1"), "192.168.0.1")
	chk(t, "ip-valid", ipValidP("127.0.0.1") && ipValidP("::1") && !ipValidP("999.1.1.1") && !ipValidP("example"), "")

	t.Logf("== OK %d / NG %d", okCount, ngCount)
}
