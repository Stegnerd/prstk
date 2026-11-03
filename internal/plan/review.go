package plan

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	directorHeading = "## Review: Director"
	reviewPrefix    = "## Review: "
)

// isHumanReviewHeading reports whether line starts the human reviewer's
// section: "## Review: <name>" with a non-empty name other than Director. Any
// name counts, so specs written when the reviewer was hard-coded
// ("## Review: Dalton") keep parsing. The caller must skip fenced lines.
func isHumanReviewHeading(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, " \t"), reviewPrefix)
	if !ok {
		return false
	}
	name := strings.TrimSpace(rest)
	return name != "" && name != "Director"
}

// AppendDirectorRound adds "### Round N (YYYY-MM-DD): <verdict>" plus body as
// the last item under "## Review: Director" (before the human
// "## Review: <name>" section if that exists, otherwise at the end of the file) and returns N, which is the
// number of existing "### Round" headings in that section plus one.
//
// Only the body is rewritten; the frontmatter bytes on disk are kept as-is.
// Headings of level 1-3 inside body are demoted to level 4 so they can't
// split the section or be miscounted as rounds.
func (s *Spec) AppendDirectorRound(date time.Time, verdict, body string) (int, error) {
	raw, err := os.ReadFile(s.SpecPath())
	if err != nil {
		return 0, err
	}
	doc, err := ParseDoc(s.SpecPath(), raw)
	if err != nil {
		return 0, err
	}
	newBody, round := insertDirectorRound(doc.Body, date, verdict, body)

	var out []byte
	if rest, ok := strings.CutSuffix(string(raw), doc.Body); ok {
		out = []byte(rest + newBody) // frontmatter bytes untouched
	} else { // CRLF file: fall back to a normalized rewrite
		doc.Body = newBody
		if out, err = doc.Bytes(); err != nil {
			return 0, err
		}
	}
	if err := WriteFileAtomic(s.SpecPath(), out); err != nil {
		return 0, err
	}
	s.doc = &Doc{Path: doc.Path, node: doc.node, Body: newBody}
	return round, nil
}

func isFence(line string) bool {
	t := strings.TrimLeft(line, " ")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// demoteHeadings turns "# ", "## ", "### " headings outside code fences into
// level-4 headings.
func demoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	fenced := false
	for i, l := range lines {
		if isFence(l) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, p := range []string{"### ", "## ", "# "} {
			if strings.HasPrefix(l, p) {
				lines[i] = "#### " + strings.TrimPrefix(l, p)
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func insertDirectorRound(doc string, date time.Time, verdict, body string) (string, int) {
	lines := strings.Split(doc, "\n")
	start, end := -1, len(lines) // section is lines[start+1:end]
	fenced := false
	for i, l := range lines {
		if isFence(l) {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(l, "## ") {
			continue
		}
		h := strings.TrimRight(l, " \t")
		switch {
		case start < 0 && h == directorHeading:
			start = i
		case start >= 0:
			end = i
		}
		if start >= 0 && end != len(lines) {
			break
		}
	}

	body = strings.TrimRight(demoteHeadings(strings.ReplaceAll(body, "\r\n", "\n")), " \t\r\n")
	if start < 0 {
		// No Director section: create it before the human reviewer's section, or at the end.
		at := len(lines)
		fenced = false
		for i, l := range lines {
			if isFence(l) {
				fenced = !fenced
			} else if !fenced && isHumanReviewHeading(l) {
				at = i
				break
			}
		}
		head := append([]string{}, lines[:at]...)
		tail := lines[at:]
		sec := []string{directorHeading, ""}
		return joinSection(head, sec, tail, 1, date, verdict, body), 1
	}

	n := 1
	fenced = false
	for _, l := range lines[start+1 : end] {
		if isFence(l) {
			fenced = !fenced
		} else if !fenced && strings.HasPrefix(l, "### Round") {
			n++
		}
	}
	head := append([]string{}, lines[:start+1]...)
	content := lines[start+1 : end]
	// Trim trailing blank lines of the section's content.
	for len(content) > 0 && strings.TrimSpace(content[len(content)-1]) == "" {
		content = content[:len(content)-1]
	}
	head = append(head, content...)
	return joinSection(head, nil, lines[end:], n, date, verdict, body), n
}

// joinSection appends the round to head (which ends with the section content),
// then tail (the rest of the document, starting at the next heading or empty).
func joinSection(head, sec []string, tail []string, n int, date time.Time, verdict, body string) string {
	// Normalize the blank-line gap before the new material.
	for len(head) > 0 && strings.TrimSpace(head[len(head)-1]) == "" {
		head = head[:len(head)-1]
	}
	out := head
	if len(out) > 0 {
		out = append(out, "")
	}
	out = append(out, sec...)
	out = append(out, fmt.Sprintf("### Round %d (%s): %s", n, date.Format("2006-01-02"), verdict), "")
	if body != "" {
		out = append(out, strings.Split(body, "\n")...)
	}
	// Tail: blank line before the next heading; or end the file with one newline.
	rest := strings.TrimLeft(strings.Join(tail, "\n"), "\n")
	if rest == "" {
		return strings.Join(out, "\n") + "\n"
	}
	return strings.Join(out, "\n") + "\n\n" + rest
}
