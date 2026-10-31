package main

// dai.go - 板一覧 + 各板の HEAD LINE (Python 版 dai.py)

import (
	"fmt"
	"strings"
)

const headlineMax = 20

func readHeadline(folder, folderEscaped string) []string {
	lines, ok := readFileLines(appPath(folder, "index.html"))
	if !ok {
		return nil
	}
	pos := -1
	for i, l := range lines {
		if strings.Contains(l, "<!-- HEAD LINE -->") {
			pos = i
			break
		}
	}
	if pos < 0 {
		return nil
	}
	var out []string
	for _, line := range lines[pos+1:] {
		if len(out) >= headlineMax || strings.Contains(line, "<!-- HEAD LINE END -->") {
			break
		}
		out = append(out, replaceFirstDigitHTML(line, folderEscaped))
	}
	return out
}

// replaceFirstDigitHTML は最初の 数字.html だけを folder/数字.html に変える (Perl の s/// 相当)。
func replaceFirstDigitHTML(line, folderEscaped string) string {
	n := len(line)
	i := 0
	for i < n {
		if line[i] >= '0' && line[i] <= '9' {
			j := i
			for j < n && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j+5 <= n && line[j:j+5] == ".html" {
				return line[:i] + folderEscaped + "/" + line[i:]
			}
			i = j
		} else {
			i++
		}
	}
	return line
}

func handleDai(req *request) *response {
	var out strings.Builder
	out.WriteString("<!DOCTYPE html>\n<html lang=\"ja\"><head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<base target=\"naka\">\n<link rel=\"stylesheet\" href=\"style.css\">\n<title>板一覧</title>\n</head><body>\n")
	for _, line := range blistLines() {
		if line == "" {
			continue
		}
		parts := splitString(line, ',')
		folder := parts[0]
		color, title := "", ""
		if len(parts) > 1 {
			color = parts[1]
		}
		if len(parts) > 2 {
			title = parts[2]
		}
		if folder == "" || startsWithP(".", folder) || strings.Contains(folder, "/") || strings.Contains(folder, "..") {
			continue
		}
		// 背景画像指定 (!…) と白文字指定 (*) は色ではない
		if bang := strings.IndexByte(color, '!'); bang >= 0 {
			color = color[:bang]
		}
		if endsWithP("*", color) {
			color = color[:len(color)-1]
		}
		title = replaceAll(title, "#c#", ",")
		folderEscaped := htmlEscape(folder)
		fmt.Fprintf(&out, "<table border=0 cellspacing=1 cellpadding=2 width=100%% bgcolor=\"%s\"><tr><td>\n<a href=\"%s/\"><b><font size=\"3\" color=\"#F92500\">%s</font></b></a>\n",
			htmlEscape(color), folderEscaped, title)
		for _, hl := range readHeadline(folder, folderEscaped) {
			out.WriteString(hl)
			out.WriteString("\n")
		}
		out.WriteString("</td></tr></table>\n")
	}
	out.WriteString("</body></html>\n")
	return htmlHeaderResponse(out.String(), 200, nil)
}
