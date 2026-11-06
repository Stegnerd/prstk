// Package gh wraps the GitHub CLI behind an interface so tests can fake it.
package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type PRInfo struct {
	Number   int    `json:"number"`
	State    string `json:"state"` // OPEN, CLOSED, MERGED
	MergedAt string `json:"mergedAt"`
	IsDraft  bool   `json:"isDraft"`
	Body     string `json:"body"`
	BaseRef  string `json:"baseRefName"`
}

type CreateArgs struct {
	Base, Head, Title, Body string
	Draft                   bool
}

// Client is the subset of gh that prstk needs. repo is "owner/name".
type Client interface {
	FindOpenPR(repo, head string) (int, error) // 0 if none
	CreatePR(repo string, a CreateArgs) (int, error)
	View(repo string, n int) (PRInfo, error)
	EditBody(repo string, n int, body string) error
	EditBase(repo string, n int, base string) error
	Ready(repo string, n int) error
	// Review posts body as a comment review on PR n, falling back to a plain
	// PR comment if the review is refused (e.g. reviewing your own PR).
	Review(repo string, n int, body string) error
	// Feedback lists PR n's reviews, inline review comments and conversation
	// comments, oldest first.
	Feedback(repo string, n int) ([]FeedbackItem, error)
	// Comments lists PR n's conversation (issue) comments, oldest first.
	Comments(repo string, n int) ([]Comment, error)
	// CreateComment posts body as a new conversation comment on PR n and
	// returns its id.
	CreateComment(repo string, n int, body string) (int64, error)
	// EditComment replaces the body of conversation comment id.
	EditComment(repo string, id int64, body string) error
	AuthStatus() error
}

// Comment is one conversation comment on a PR.
type Comment struct {
	ID   int64
	Body string
}

// FeedbackItem is one piece of human feedback on a PR.
type FeedbackItem struct {
	Kind    string // "review", "inline" or "comment"
	ID      int64
	Author  string
	Body    string
	State   string // reviews: APPROVED, CHANGES_REQUESTED, COMMENTED, …
	Path    string // inline: file
	Line    int    // inline: line (0 when outdated)
	At      string // RFC 3339
	ReplyTo int64  // inline: the comment this replies to
}

type Logger func(format string, args ...any)

// Exec runs the real gh binary.
type Exec struct{ Log Logger }

func (e Exec) run(stdin string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if e.Log != nil {
		e.Log("gh %s → err=%v\n%s%s", strings.Join(args, " "), err, out.String(), errb.String())
	}
	if err != nil {
		return "", fmt.Errorf("gh %s: %v: %s", args[0]+" "+args[1], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (e Exec) FindOpenPR(repo, head string) (int, error) {
	out, err := e.run("", "pr", "list", "-R", repo, "--head", head, "--state", "open", "--json", "number", "--limit", "1")
	if err != nil {
		return 0, err
	}
	var prs []struct{ Number int }
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return 0, err
	}
	if len(prs) == 0 {
		return 0, nil
	}
	return prs[0].Number, nil
}

func (e Exec) CreatePR(repo string, a CreateArgs) (int, error) {
	args := []string{"pr", "create", "-R", repo, "--base", a.Base, "--head", a.Head, "--title", a.Title, "--body-file", "-"}
	if a.Draft {
		args = append(args, "--draft")
	}
	out, err := e.run(a.Body, args...)
	if err != nil {
		return 0, err
	}
	// prints the PR URL: https://github.com/o/r/pull/123
	lines := strings.Split(out, "\n")
	url := lines[len(lines)-1]
	i := strings.LastIndex(url, "/")
	n, err := strconv.Atoi(url[i+1:])
	if err != nil {
		return 0, fmt.Errorf("can't parse PR number from %q", url)
	}
	return n, nil
}

func (e Exec) View(repo string, n int) (PRInfo, error) {
	var info PRInfo
	out, err := e.run("", "pr", "view", strconv.Itoa(n), "-R", repo, "--json", "number,state,mergedAt,isDraft,body,baseRefName")
	if err != nil {
		return info, err
	}
	err = json.Unmarshal([]byte(out), &info)
	return info, err
}

func (e Exec) EditBody(repo string, n int, body string) error {
	_, err := e.run(body, "pr", "edit", strconv.Itoa(n), "-R", repo, "--body-file", "-")
	return err
}

func (e Exec) EditBase(repo string, n int, base string) error {
	_, err := e.run("", "pr", "edit", strconv.Itoa(n), "-R", repo, "--base", base)
	return err
}

func (e Exec) Ready(repo string, n int) error {
	_, err := e.run("", "pr", "ready", strconv.Itoa(n), "-R", repo)
	return err
}

func (e Exec) Review(repo string, n int, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("review body is empty")
	}
	num := strconv.Itoa(n)
	if _, err := e.run(body, "pr", "review", num, "-R", repo, "--comment", "--body-file", "-"); err == nil {
		return nil
	}
	// Falls back on any error, so it can double-post if the review actually landed.
	_, err := e.run(body, "pr", "comment", num, "-R", repo, "--body-file", "-")
	return err
}

// apiList GETs a paginated list endpoint and decodes every page into out.
func apiList[T any](e Exec, path string) ([]T, error) {
	out, err := e.run("", "api", "--paginate", path)
	if err != nil {
		return nil, err
	}
	// --paginate prints one JSON array per page, back to back.
	var all []T
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []T
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("gh api %s: %w", path, err)
		}
		all = append(all, page...)
	}
	return all, nil
}

type ghUser struct {
	Login string `json:"login"`
}

func (e Exec) Feedback(repo string, n int) ([]FeedbackItem, error) {
	var items []FeedbackItem
	reviews, err := apiList[struct {
		ID          int64  `json:"id"`
		User        ghUser `json:"user"`
		Body        string `json:"body"`
		State       string `json:"state"`
		SubmittedAt string `json:"submitted_at"`
	}](e, fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, n))
	if err != nil {
		return nil, err
	}
	for _, r := range reviews {
		items = append(items, FeedbackItem{Kind: "review", ID: r.ID, Author: r.User.Login, Body: r.Body, State: r.State, At: r.SubmittedAt})
	}
	inline, err := apiList[struct {
		ID        int64  `json:"id"`
		User      ghUser `json:"user"`
		Body      string `json:"body"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		CreatedAt string `json:"created_at"`
		ReplyTo   int64  `json:"in_reply_to_id"`
	}](e, fmt.Sprintf("repos/%s/pulls/%d/comments", repo, n))
	if err != nil {
		return nil, err
	}
	for _, c := range inline {
		items = append(items, FeedbackItem{Kind: "inline", ID: c.ID, Author: c.User.Login, Body: c.Body, Path: c.Path, Line: c.Line, At: c.CreatedAt, ReplyTo: c.ReplyTo})
	}
	comments, err := apiList[struct {
		ID        int64  `json:"id"`
		User      ghUser `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}](e, fmt.Sprintf("repos/%s/issues/%d/comments", repo, n))
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		items = append(items, FeedbackItem{Kind: "comment", ID: c.ID, Author: c.User.Login, Body: c.Body, At: c.CreatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At < items[j].At })
	return items, nil
}

func (e Exec) AuthStatus() error {
	_, err := e.run("", "auth", "status")
	return err
}

func (e Exec) Comments(repo string, n int) ([]Comment, error) {
	raw, err := apiList[struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}](e, fmt.Sprintf("repos/%s/issues/%d/comments", repo, n))
	if err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(raw))
	for _, c := range raw {
		out = append(out, Comment{ID: c.ID, Body: c.Body})
	}
	return out, nil
}

// apiBody sends {"body": body} to path with method and returns the id from
// the response.
func (e Exec) apiBody(method, path, body string) (int64, error) {
	in, err := json.Marshal(struct {
		Body string `json:"body"`
	}{body})
	if err != nil {
		return 0, err
	}
	out, err := e.run(string(in), "api", "-X", method, path, "--input", "-")
	if err != nil {
		return 0, err
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return 0, fmt.Errorf("gh api %s %s: %w", method, path, err)
	}
	return resp.ID, nil
}

func (e Exec) CreateComment(repo string, n int, body string) (int64, error) {
	return e.apiBody("POST", fmt.Sprintf("repos/%s/issues/%d/comments", repo, n), body)
}

func (e Exec) EditComment(repo string, id int64, body string) error {
	_, err := e.apiBody("PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repo, id), body)
	return err
}
