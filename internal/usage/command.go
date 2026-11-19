package usage

import (
	"path"
	"regexp"
	"strings"
)

// invocation is one prstk call found in a Bash command.
type invocation struct {
	Sub        string   // subcommand ("" for bare prstk)
	Args       []string // positionals after the subcommand
	Initiative string   // --initiative value, if any
}

// perPR are the subcommands whose first positional is a PR id.
var perPR = map[string]bool{
	"assign": true, "check": true, "open": true, "review": true, "status": true,
	"ready": true, "feedback": true, "propagate": true, "merged": true,
	"show": true, "spawn-prompt": true,
}

// valueFlags are prstk flags that take a separate value argument.
var valueFlags = map[string]bool{
	"--initiative": true, "--base": true, "--title": true, "--parent": true,
	"--model": true, "--conflicts-with": true, "--evidence": true, "--reason": true,
	"--worker": true, "--skip-evidence": true, "--file": true, "--verdict": true,
	"--summary": true, "--author": true, "--cmd": true, "--phase": true,
	"--step": true, "--caption": true, "--port": true, "--link": true,
	"--agents-dir": true,
}

// prefixWords may precede the command word without changing what runs.
var prefixWords = map[string]bool{"time": true, "command": true, "exec": true, "env": true, "nohup": true}

var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// parseInvocations finds the prstk invocations in a Bash command. It's a
// heuristic, not a shell parser: it splits on newlines, &&, ||, ;, | and &
// outside quotes, and looks for prstk in the command position of each piece.
func parseInvocations(cmd string) []invocation {
	var out []invocation
	for _, words := range splitCommand(cmd) {
		i := 0
		for i < len(words) {
			w := strings.TrimLeft(words[i], "({")
			if w == "" || envAssignRe.MatchString(w) || prefixWords[w] {
				i++
				continue
			}
			words[i] = w
			break
		}
		if i >= len(words) || !isPrstk(words[i]) {
			continue
		}
		inv := invocation{}
		var pos []string
		args := words[i+1:]
		for j := 0; j < len(args); j++ {
			a := strings.TrimRight(args[j], ")}")
			switch {
			case strings.HasPrefix(a, "--initiative="):
				inv.Initiative = strings.TrimPrefix(a, "--initiative=")
			case a == "--initiative":
				if j+1 < len(args) {
					inv.Initiative = strings.TrimRight(args[j+1], ")}")
				}
				j++
			case strings.HasPrefix(a, "-"):
				if valueFlags[a] {
					j++
				}
			case a != "":
				pos = append(pos, a)
			}
		}
		if len(pos) > 0 {
			inv.Sub = pos[0]
		}
		if len(pos) > 1 {
			inv.Args = pos[1:]
		}
		out = append(out, inv)
	}
	return out
}

func isPrstk(w string) bool {
	switch w {
	case "$PRSTK_BIN", "${PRSTK_BIN}":
		return true
	}
	b := path.Base(w)
	return b == "prstk" || strings.HasPrefix(b, "prstk-")
}

// splitCommand tokenizes a shell command into simple commands (lists of
// words), honouring quotes and backslashes but nothing else.
func splitCommand(cmd string) [][]string {
	var (
		pieces [][]string
		words  []string
		cur    strings.Builder
		inWord bool
		quote  rune
	)
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endPiece := func() {
		endWord()
		if len(words) > 0 {
			pieces = append(pieces, words)
			words = nil
		}
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\' || rs[i+1] == '$') {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\':
			if i+1 < len(rs) {
				i++
				if rs[i] != '\n' {
					cur.WriteRune(rs[i])
					inWord = true
				}
			}
		case c == '\n' || c == ';' || c == '&' || c == '|':
			endPiece()
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	endPiece()
	return pieces
}

// prID returns the PR id an invocation names, if it is a per-PR command.
func (inv invocation) prID() string {
	if perPR[inv.Sub] && len(inv.Args) > 0 {
		return inv.Args[0]
	}
	if inv.Sub == "evidence" && len(inv.Args) > 1 {
		return inv.Args[1]
	}
	return ""
}
