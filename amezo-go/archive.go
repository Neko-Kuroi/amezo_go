package main

// archive.go - 過去ログの gzip 圧縮 (Python 版 archive.py)
// gzip の作成・展開は外部の gzip コマンドを使う (中身の照合は自前の SHA-256)。

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	archLockTries  = 50
	archTmpMaxAge  = 86400
	archSelftestMt = 1700000000
)

// ---------- gzip 外部コマンド ----------
// gzipDecompressToFile は成功すれば true。outPath に展開結果を書く。
func gzipDecompressToFile(gz, outPath string) bool {
	out, err := os.Create(outPath)
	if err != nil {
		return false
	}
	defer out.Close()
	cmd := exec.Command("gzip", "-dc", gz)
	cmd.Stdout = out
	return cmd.Run() == nil
}

// gzipDecompressDigest は (hex, 長さ, ok) を展開しながら数える。
func gzipDecompressDigest(gz string) (string, int64, bool) {
	cmd := exec.Command("gzip", "-dc", gz)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, false
	}
	if err := cmd.Start(); err != nil {
		return "", 0, false
	}
	h := sha256.New()
	n, copyErr := io.Copy(h, stdout)
	waitErr := cmd.Wait()
	if copyErr != nil || waitErr != nil {
		return "", 0, false
	}
	return hexEncode(h.Sum(nil)), n, true
}

func gzipCompressFile(plain, tmp string) bool {
	out, err := os.Create(tmp)
	if err != nil {
		return false
	}
	defer out.Close()
	cmd := exec.Command("gzip", "-6", "-c", plain)
	cmd.Stdout = out
	return cmd.Run() == nil
}

// gzIsize は gzip 末尾の ISIZE (展開後サイズ mod 2^32)。取れなければ ok=false。
func gzIsize(gz string) (int64, bool) {
	f, err := os.Open(gz)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() < 4 {
		return 0, false
	}
	if _, err := f.Seek(-4, io.SeekEnd); err != nil {
		return 0, false
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(f, b); err != nil {
		return 0, false
	}
	return int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16 | int64(b[3])<<24, true
}

func archMB(b int64) string { return fmt.Sprintf("%.2fMB", float64(b)/1048576.0) }

// ---------- 走査 ----------
// archScan は (平文の名前, gz の名前 (o数字.html で), 一時ファイルのパス) を番号順で返す。
func archScan(folder string) ([]string, []string, []string) {
	var plain, gz, tmp []string
	for _, name := range listDirNames(appPath(folder)) {
		switch {
		case startsWithP("o", name) && endsWithP(".html", name) && len(name) > 6 && digitsP(name[1:len(name)-5], 1, -1):
			plain = append(plain, name)
		case startsWithP("o", name) && endsWithP(".html.gz", name) && len(name) > 9 && digitsP(name[1:len(name)-8], 1, -1):
			gz = append(gz, name[:len(name)-3])
		case startsWithP("o", name) && strings.Contains(name, ".html.gz.tmp"):
			if dot := strings.IndexByte(name, '.'); dot > 1 && digitsP(name[1:dot], 1, -1) {
				tmp = append(tmp, appPath(folder, name))
			}
		}
	}
	num := func(n string) int {
		dot := strings.IndexByte(n, '.')
		v, _ := strconv.Atoi(n[1:dot])
		return v
	}
	sort.Slice(plain, func(i, j int) bool { return num(plain[i]) < num(plain[j]) })
	sort.Slice(gz, func(i, j int) bool { return num(gz[i]) < num(gz[j]) })
	return plain, gz, tmp
}

// ---------- アーカイバ ----------
type archiver struct {
	quiet          int64
	dry            bool
	verbose        bool
	compressed     int
	plainRemoved   int
	staleGzRemoved int
	quarantined    int
	ng             int
	inBytes        int64
	outBytes       int64
	skip           map[string]int
	skipOrder      []string
}

func (a *archiver) say(path, msg string) {
	if strings.HasPrefix(path, "./") {
		path = path[2:]
	}
	fmt.Printf("%-34s %s\n", path, msg)
}

func (a *archiver) skipIt(path, reason string) bool {
	if _, ok := a.skip[reason]; !ok {
		a.skipOrder = append(a.skipOrder, reason)
	}
	a.skip[reason]++
	if a.verbose {
		a.say(path, "skip: "+reason)
	}
	return false
}

func (a *archiver) ngIt(path, msg string) bool {
	a.ng++
	a.say(path, "NG: "+msg)
	return false
}

// diskFreeBytes は df コマンドで空き容量 (バイト) を取る。取れなければ ok=false。
func diskFreeBytes(dir string) (int64, bool) {
	out, err := exec.Command("df", "-P", "-B1", dir).Output()
	if err != nil {
		return 0, false
	}
	lines := splitString(string(out), '\n')
	if len(lines) < 2 {
		return 0, false
	}
	cols := splitStringNoEmpty(lines[1], ' ')
	if len(cols) < 4 {
		return 0, false
	}
	v, err := strconv.ParseInt(cols[3], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func (a *archiver) stage1(plain, disp string) bool {
	stSize, okSize := fileSizeOf(plain)
	stMtime, _ := fileMtimeOf(plain)
	if !okSize {
		return a.skipIt(disp, "ファイルが無い")
	}
	if epochNow()-stMtime < a.quiet {
		return a.skipIt(disp, "最後の書き込みから間もない")
	}
	if free, ok := diskFreeBytes("."); ok && free < stSize+1048576 {
		return a.skipIt(disp, "ディスクの空きが足りない")
	}
	if a.dry {
		a.say(disp, fmt.Sprintf("[dry-run] 圧縮する (%s)", archMB(stSize)))
		a.compressed++
		return true
	}
	t0 := epochNow()
	gzpath := plain + ".gz"
	tmp := fmt.Sprintf("%s.tmp%d", gzpath, os.Getpid())
	if !gzipCompressFile(plain, tmp) {
		_ = os.Remove(tmp)
		return a.ngIt(disp, "開けない (権限・空き容量を確認)")
	}
	srcHex, srcN, _ := sha256File(plain)
	dHex, dN, dOK := gzipDecompressDigest(tmp)
	if !dOK || dHex != srcHex || dN != srcN {
		_ = os.Remove(tmp)
		return a.ngIt(disp, "展開して確かめたら、元と一致しない (一時ファイルを消した)")
	}
	st2Size, ok2 := fileSizeOf(plain)
	st2Mtime, _ := fileMtimeOf(plain)
	if !ok2 || st2Size != stSize || st2Mtime != stMtime || srcN != stSize {
		_ = os.Remove(tmp)
		return a.skipIt(disp, "圧縮している間に元が変わった")
	}
	setFileMtime(tmp, stMtime)
	chmodFile(tmp, 0o644)
	if !renamePath(tmp, gzpath) {
		_ = os.Remove(tmp)
		return a.ngIt(disp, "rename できない")
	}
	gsize, _ := fileSizeOf(gzpath)
	a.compressed++
	a.inBytes += stSize
	a.outBytes += gsize
	denom := gsize
	if denom < 1 {
		denom = 1
	}
	a.say(disp, fmt.Sprintf("圧縮  %s → %s (%.1f倍)  %dms",
		archMB(stSize), archMB(gsize), float64(stSize)/float64(denom), (epochNow()-t0)*1000))
	return true
}

func (a *archiver) stage2(plain, disp string) {
	gzpath := plain + ".gz"
	spSize, okSize := fileSizeOf(plain)
	spMtime, _ := fileMtimeOf(plain)
	sgMtime, okGz := fileMtimeOf(gzpath)
	sgCtime, _ := fileCtimeOf(gzpath)
	if !okSize || !okGz {
		return
	}
	if sgMtime != spMtime {
		if a.dry {
			a.say(disp, "[dry-run] 古い gz を削除する")
			a.staleGzRemoved++
		} else {
			_ = os.Remove(gzpath)
			a.staleGzRemoved++
			a.say(disp, "古い gz を削除 (平文が圧縮のあとに更新された。次回に再圧縮する)")
		}
		return
	}
	if epochNow()-sgCtime < a.quiet {
		a.skipIt(disp, "gz の作成から間もない")
		return
	}
	if spSize >= 4294967296 {
		a.skipIt(disp, "4GiB 以上は確認できない")
		return
	}
	isize, isizeOK := gzIsize(gzpath)
	dpHex, dpN, _ := sha256File(plain)
	dgHex, dgN, dgOK := gzipDecompressDigest(gzpath)
	if !isizeOK || isize != spSize || dpHex == "" || !dgOK || dpHex != dgHex || dpN != dgN {
		if a.dry {
			a.say(disp, "[dry-run] NG: gz の中身が平文と一致しない → gz を .bad に改名する")
			a.ng++
		} else {
			_ = renamePath(gzpath, gzpath+".bad")
			a.quarantined++
			a.ngIt(disp, "gz の中身が平文と一致しない。どちらも消さず、gz を oN.html.gz.bad に改名した (人が確認する)")
		}
		return
	}
	// 平文をロックして再確認してから消す
	f, err := os.Open(plain)
	if err != nil {
		a.skipIt(disp, "ロックのために開けない")
		return
	}
	got := false
	for i := 0; i < archLockTries && !got; i++ {
		got = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
		if !got {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !got {
		f.Close()
		a.skipIt(disp, "ロックが取れない (投稿中?)")
		return
	}
	same := false
	if fi, err := f.Stat(); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			same = st.Nlink > 0 && st.Size == spSize && st.Mtim.Sec == spMtime
		}
	}
	if !same {
		fdUnlock(f)
		f.Close()
		a.skipIt(disp, "ロックを待つあいだに変わった (次回に判断する)")
		return
	}
	if a.dry {
		fdUnlock(f)
		f.Close()
		a.say(disp, "[dry-run] 平文を削除する")
		a.plainRemoved++
		return
	}
	removeErr := os.Remove(plain)
	fdUnlock(f)
	f.Close()
	if removeErr == nil {
		a.plainRemoved++
		a.say(disp, "平文を削除 (gz に置き換え)")
	} else {
		a.ngIt(disp, "平文を消せない")
	}
}

// ---------- 自己チェック ----------
type archCheck struct {
	label  string
	ok     bool
	detail string
}

func fstatOf(f *os.File) (*syscall.Stat_t, bool) {
	fi, err := f.Stat()
	if err != nil {
		return nil, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return st, ok
}

func archSelftest(dirpath string, full bool) []archCheck {
	var r []archCheck
	add := func(label string, ok bool, detail string) {
		r = append(r, archCheck{label, ok, detail})
	}
	testGz := dirpath + "/.arch-gz-test"
	add("gzip コマンドが使える", gzipCompressFile(appPath("blist.txt"), testGz), "")
	_ = os.Remove(testGz)
	base := fmt.Sprintf("%s/.archive-selftest-%d", dirpath, os.Getpid())
	aPath, bPath := base+".a", base+".b"
	defer os.Remove(aPath)
	defer os.Remove(bPath)
	if !writeFileString(aPath, "selftest\n") {
		add("フォルダに書き込める", false, dirpath)
		return r
	}
	add("フォルダに書き込める", true, "")
	if f, err := os.OpenFile(aPath, os.O_RDWR, 0); err == nil {
		if st, ok := fstatOf(f); ok {
			add("通常のファイルの nlink は 1", st.Nlink == 1, fmt.Sprintf("nlink=%d", st.Nlink))
		}
		_ = os.Remove(aPath)
		if st, ok := fstatOf(f); ok {
			add("開いたまま unlink すると nlink が 0 になる", st.Nlink == 0, fmt.Sprintf("nlink=%d", st.Nlink))
		}
		f.Close()
	}
	writeFileString(aPath, "selftest\n")
	if f, err := os.OpenFile(aPath, os.O_RDWR, 0); err == nil {
		_ = os.Rename(aPath, bPath)
		if st, ok := fstatOf(f); ok {
			add("開いたまま rename しても nlink は 1 のまま", st.Nlink == 1, fmt.Sprintf("nlink=%d", st.Nlink))
		}
		f.Close()
	}
	setFileMtime(bPath, archSelftestMt)
	if st, ok := statOf(bPath); ok {
		add("os.utime で更新日時 (mtime) を指定できる", st.Mtim.Sec == archSelftestMt, "")
		c0 := st.Ctim.Sec
		if full {
			time.Sleep(1 * time.Second)
		}
		_ = os.Rename(bPath, aPath)
		if st2, ok2 := statOf(aPath); ok2 {
			ctimeOK := st2.Ctim.Sec >= c0
			if full {
				ctimeOK = st2.Ctim.Sec > c0
			}
			add("rename で ctime が更新され、mtime は保たれる",
				ctimeOK && st2.Mtim.Sec == archSelftestMt,
				fmt.Sprintf("ctime %d → %d", c0, st2.Ctim.Sec))
		}
	}
	// 別プロセスが持っている flock は取れないこと
	locked := false
	proc := exec.Command("flock", aPath, "-c", "sleep 3")
	if err := proc.Start(); err == nil {
		time.Sleep(500 * time.Millisecond)
		if f, err := os.OpenFile(aPath, os.O_RDWR, 0); err == nil {
			locked = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
			fdUnlock(f)
			f.Close()
		}
		_ = proc.Process.Signal(syscall.SIGTERM)
		_ = proc.Wait()
	}
	add("別プロセスが持っている flock は、こちらからは取れない", locked, "")
	_, dfOK := diskFreeBytes(".")
	add("ディスクの空き容量が確認できる", dfOK, "")
	return r
}

// ---------- restore ----------
func archValidBoard(b string) bool {
	return b != "" && !strings.Contains(b, "/") && !strings.Contains(b, "..") &&
		isBoard(b) && dirExistsP(appPath(b))
}

func archUsage() int {
	fmt.Fprintln(os.Stderr, "使い方:")
	fmt.Fprintln(os.Stderr, "  archive 板名… | --all [--quiet=秒] [--limit=件数] [--dry-run] [-v]")
	fmt.Fprintln(os.Stderr, "  archive --restore 板名 oN")
	fmt.Fprintln(os.Stderr, "  archive --selftest [板名]")
	return 2
}

func archRestore(folder, oname string) int {
	s := oname
	if strings.HasPrefix(s, "o") {
		s = s[1:]
	}
	if strings.HasSuffix(s, ".html") {
		s = s[:len(s)-5]
	}
	num := ""
	if digitsP(s, 1, -1) {
		num = s
	}
	if !archValidBoard(folder) || num == "" {
		return archUsage()
	}
	plain := appPath(folder, "o"+num+".html")
	gzpath := plain + ".gz"
	lk, err := os.OpenFile(appPath(folder, "archive.lock"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil || syscall.Flock(int(lk.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if lk != nil {
			lk.Close()
		}
		fmt.Fprintf(os.Stderr, "板 %s: 別の archive が実行中です\n", folder)
		return 2
	}
	defer lk.Close()
	defer fdUnlock(lk)
	if !fileRegularP(gzpath) {
		fmt.Fprintf(os.Stderr, "%s が無い\n", gzpath)
		return 2
	}
	if pathExists(plain) {
		fmt.Fprintf(os.Stderr, "%s がすでにある (上書きはしない)\n", plain)
		return 2
	}
	sgMtime, _ := fileMtimeOf(gzpath)
	tmp := fmt.Sprintf("%s.restore.tmp%d", plain, os.Getpid())
	gzipDecompressToFile(gzpath, tmp)
	n, nOK := fileSizeOf(tmp)
	if !nOK {
		n = -1
	}
	isize, isizeOK := gzIsize(gzpath)
	want := int64(-2)
	if isizeOK {
		want = isize
	}
	if n != want {
		_ = os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "展開した長さが、gz の記録と一致しない (gz が壊れている?)")
		return 1
	}
	setFileMtime(tmp, sgMtime)
	chmodFile(tmp, 0o644)
	if !renamePath(tmp, plain) {
		_ = os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "rename できない")
		return 1
	}
	fmt.Printf("%s を作った (%s)。gz は残してある。\n", plain, archMB(n))
	fmt.Println("  編集すると更新日時が変わり、配信は平文に切り替わる (古い gz は次回の実行で消え、その次に再圧縮される)。")
	fmt.Println("  編集しなければ、次回以降の実行で、この平文はまた消える。")
	return 0
}

// ---------- メイン ----------
func archiveMain(argv []string) int {
	optQuiet := int64(archiveQuietSeconds)
	var optLimit int64 = 0
	dry, verbose, all, restore, selftest := false, false, false, false, false
	var boards []string
	for _, a := range argv {
		switch {
		case a == "--all":
			all = true
		case a == "--dry-run":
			dry = true
		case a == "-v":
			verbose = true
		case a == "--restore":
			restore = true
		case a == "--selftest":
			selftest = true
		case strings.HasPrefix(a, "--quiet=") && digitsP(a[8:], 1, 7):
			if v, err := strconv.ParseInt(a[8:], 10, 64); err == nil && v <= 604800 {
				optQuiet = v
			} else {
				return archUsage()
			}
		case strings.HasPrefix(a, "--limit=") && digitsP(a[8:], 1, 9):
			if v, err := strconv.ParseInt(a[8:], 10, 64); err == nil && v >= 1 {
				optLimit = v
			} else {
				return archUsage()
			}
		case a != "" && a[0] != '-':
			boards = append(boards, a)
		default:
			return archUsage()
		}
	}
	if restore {
		if len(boards) != 2 {
			return archUsage()
		}
		return archRestore(boards[0], boards[1])
	}
	if all {
		boards = boardFolders()
	}
	if selftest {
		allb := boardFolders()
		b := ""
		if len(boards) > 0 {
			b = boards[0]
		} else if len(allb) > 0 {
			b = allb[0]
		}
		if b == "" || !archValidBoard(b) {
			return archUsage()
		}
		bad := 0
		fmt.Printf("自己チェック (フォルダ: ./%s)\n", b)
		for _, r := range archSelftest(appPath(b), true) {
			mark := "OK   "
			if !r.ok {
				mark = "NG   "
				bad++
			}
			detail := ""
			if r.detail != "" {
				detail = fmt.Sprintf("  (%s)", r.detail)
			}
			fmt.Printf("%s%s%s\n", mark, r.label, detail)
		}
		if bad == 0 {
			fmt.Println("すべて OK。この環境で archive を使えます。")
		} else {
			fmt.Printf("NG が %d 件。この環境では archive を動かさないでください。\n", bad)
		}
		if bad == 0 {
			return 0
		}
		return 2
	}
	if len(boards) == 0 {
		return archUsage()
	}
	for _, b := range boards {
		if !archValidBoard(b) {
			fmt.Fprintf(os.Stderr, "板 %s: 見つからない (blist.txt とフォルダを確認)\n", b)
			return 2
		}
	}
	if !dry {
		var bad []archCheck
		for _, r := range archSelftest(appPath(boards[0]), false) {
			if !r.ok {
				bad = append(bad, r)
			}
		}
		if len(bad) > 0 {
			for _, r := range bad {
				fmt.Fprintf(os.Stderr, "自己チェック NG: %s\n", r.label)
			}
			fmt.Fprintln(os.Stderr, "この環境では動かしません。`archive --selftest 板名` で詳細を確認してください。")
			return 2
		}
	}
	run := &archiver{quiet: optQuiet, dry: dry, verbose: verbose, skip: map[string]int{}}
	remaining := optLimit
	for _, folder := range boards {
		var lk *os.File
		skipped := false
		if !dry {
			f, err := os.OpenFile(appPath(folder, "archive.lock"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
			if err != nil || syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
				if f != nil {
					f.Close()
				}
				fmt.Printf("板 %s: 別の archive が実行中のため、スキップ\n", folder)
				skipped = true
			} else {
				lk = f
			}
		}
		if !skipped {
			plain, gz, tmps := archScan(folder)
			if !dry {
				for _, tp := range tmps {
					if mt, ok := fileMtimeOf(tp); ok && epochNow()-mt > archTmpMaxAge {
						_ = os.Remove(tp)
						fmt.Printf("%s  古い一時ファイルを削除\n", tp)
					}
				}
			}
			bc := []int64{int64(run.compressed), int64(run.plainRemoved), int64(run.staleGzRemoved),
				int64(run.quarantined), int64(run.ng), run.inBytes, run.outBytes}
			gzSet := map[string]bool{}
			for _, g := range gz {
				gzSet[g] = true
			}
			for _, name := range plain {
				if gzSet[name] {
					run.stage2(appPath(folder, name), "./"+folder+"/"+name)
				}
			}
			for _, name := range plain {
				if gzSet[name] {
					continue
				}
				p := "./" + folder + "/" + name
				if optLimit > 0 && remaining <= 0 {
					run.skipIt(p, "--limit に達した")
				} else if run.stage1(appPath(folder, name), p) {
					if optLimit > 0 {
						remaining--
					}
				}
			}
			d := func(i int, cur int64) int64 { return cur - bc[i] }
			fmt.Printf("板 %s: 圧縮 %d (%s → %s)、平文削除 %d、古い gz 削除 %d、gz を退避 %d、NG %d\n",
				folder, d(0, int64(run.compressed)), archMB(d(5, run.inBytes)), archMB(d(6, run.outBytes)),
				d(1, int64(run.plainRemoved)), d(2, int64(run.staleGzRemoved)),
				d(3, int64(run.quarantined)), d(4, int64(run.ng)))
		}
		if lk != nil {
			fdUnlock(lk)
			lk.Close()
		}
	}
	if len(run.skip) > 0 {
		var parts []string
		for _, reason := range run.skipOrder {
			parts = append(parts, fmt.Sprintf("%s %d", reason, run.skip[reason]))
		}
		fmt.Printf("スキップ: %s\n", joinStrings(parts, ", "))
	}
	if run.ng > 0 {
		return 1
	}
	return 0
}
