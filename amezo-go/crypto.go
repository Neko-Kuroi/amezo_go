package main

// crypto.go - SHA-256 / HMAC / PBKDF2 / base64 / base32
// Python 版 (hashlib / hmac / base64) と結果が一致する。PBKDF2 だけ標準に無いので自前実装。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"strings"
)

func utf8Bytes(s string) []byte { return []byte(s) }

func hexEncode(octs []byte) string { return hex.EncodeToString(octs) }

// hexDecode は壊れた入力で nil を返す。
func hexDecode(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func randomOctets(n int) []byte {
	v := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, v); err != nil {
		panic(err)
	}
	return v
}

func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

func constantTimeStringEqual(a, b string) bool {
	return constantTimeEqual([]byte(a), []byte(b))
}

// ---------- SHA-256 ----------
func sha256Octets(octs []byte) []byte {
	h := sha256.Sum256(octs)
	return h[:]
}

func sha256Hex(octs []byte) string { return hexEncode(sha256Octets(octs)) }

// sha256File はファイルの SHA-256 を [hex 長さ ok] で返す (archive 用)。
func sha256File(path string) (string, int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, false
	}
	return hexEncode(h.Sum(nil)), n, true
}

// ---------- HMAC-SHA256 ----------
func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

func hmacSHA256Hex(key, msg []byte) string { return hexEncode(hmacSHA256(key, msg)) }

// ---------- PBKDF2-HMAC-SHA256 (Python の hashlib.pbkdf2_hmac と同じ) ----------
func pbkdf2SHA256(password, salt []byte, iterations, dklen int) []byte {
	out := make([]byte, 0, dklen)
	for blockIndex := 1; len(out) < dklen; blockIndex++ {
		msg := make([]byte, 0, len(salt)+4)
		msg = append(msg, salt...)
		msg = append(msg, byte(blockIndex>>24), byte(blockIndex>>16), byte(blockIndex>>8), byte(blockIndex))
		u := hmacSHA256(password, msg)
		acc := make([]byte, len(u))
		copy(acc, u)
		for i := 1; i < iterations; i++ {
			u = hmacSHA256(password, u)
			for j := range acc {
				acc[j] ^= u[j]
			}
		}
		out = append(out, acc...)
	}
	return out[:dklen]
}

// ---------- パスワードハッシュ (pbkdf2_sha256$回数$salt.hex$hash.hex) ----------
const hashScheme = "pbkdf2_sha256"

func makeHash(password string) string {
	return makeHashWith(password, hashIterations, nil)
}

func makeHashWith(password string, iterations int, salt []byte) string {
	if salt == nil {
		salt = randomOctets(16)
	}
	dk := pbkdf2SHA256(utf8Bytes(password), salt, iterations, 32)
	return hashScheme + "$" + strconv.Itoa(iterations) + "$" + hexEncode(salt) + "$" + hexEncode(dk)
}

func verifyHash(password, stored string) bool {
	parts := strings.Split(strings.Trim(stored, " \t\r\n"), "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false
	}
	iters, err := strconv.Atoi(parts[1])
	if err != nil || iters < 1 || iters > 5000000 {
		return false
	}
	salt := hexDecode(parts[2])
	expected := hexDecode(parts[3])
	if salt == nil || expected == nil {
		return false
	}
	return constantTimeEqual(pbkdf2SHA256(utf8Bytes(password), salt, iters, 32), expected)
}

// ---------- base64 / base64url / base32 ----------
func base64Encode(octs []byte) string { return base64.StdEncoding.EncodeToString(octs) }

func b64urlEncode(octs []byte) string { return base64.RawURLEncoding.EncodeToString(octs) }

// b64urlDecode は文字種が違う・形が壊れていれば nil (Python 版と同じ)。
func b64urlDecode(s string) []byte {
	if s == "" {
		return nil
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return nil
		}
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

var b32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// base32Encode はパディングなしの base32 (Python の base64.b32encode から '=' を除いたもの)。
func base32Encode(octs []byte) string { return b32NoPad.EncodeToString(octs) }
