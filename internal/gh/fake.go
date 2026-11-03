package gh

import (
	"fmt"
	"sync"
)

// Fake is an in-memory GitHub for tests.
type Fake struct {
	mu    sync.Mutex
	next  int
	PRs   map[int]*FakePR
	Calls []string
	// Reviews records every posted review/comment, in order.
	Reviews []FakeReview
	// FailOn makes the named method return an error once (for idempotency tests).
	FailOn map[string]bool
}

// FakeReview is one posted review. Kind is "review" or, after the primary
// path failed, "comment".
type FakeReview struct {
	Repo string
	N    int
	Kind string
	Body string
}

type FakePR struct {
	PRInfo
	Head, Title string
}

func NewFake() *Fake { return &Fake{next: 100, PRs: map[int]*FakePR{}, FailOn: map[string]bool{}} }

func (f *Fake) fail(m string) error {
	if f.FailOn[m] {
		delete(f.FailOn, m)
		return fmt.Errorf("fake %s failure", m)
	}
	return nil
}

func (f *Fake) FindOpenPR(repo, head string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "find "+head)
	for n, p := range f.PRs {
		if p.Head == head && p.State == "OPEN" {
			return n, nil
		}
	}
	return 0, nil
}

func (f *Fake) CreatePR(repo string, a CreateArgs) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreatePR"); err != nil {
		return 0, err
	}
	f.next++
	n := f.next
	f.PRs[n] = &FakePR{PRInfo: PRInfo{Number: n, State: "OPEN", IsDraft: a.Draft, Body: a.Body, BaseRef: a.Base}, Head: a.Head, Title: a.Title}
	f.Calls = append(f.Calls, fmt.Sprintf("create %s→%s #%d", a.Head, a.Base, n))
	return n, nil
}

func (f *Fake) get(n int) (*FakePR, error) {
	p, ok := f.PRs[n]
	if !ok {
		return nil, fmt.Errorf("no PR #%d", n)
	}
	return p, nil
}

func (f *Fake) View(repo string, n int) (PRInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(n)
	if err != nil {
		return PRInfo{}, err
	}
	return p.PRInfo, nil
}

func (f *Fake) EditBody(repo string, n int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(n)
	if err != nil {
		return err
	}
	p.Body = body
	f.Calls = append(f.Calls, fmt.Sprintf("body #%d", n))
	return nil
}

func (f *Fake) EditBase(repo string, n int, base string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(n)
	if err != nil {
		return err
	}
	p.BaseRef = base
	f.Calls = append(f.Calls, fmt.Sprintf("base #%d %s", n, base))
	return nil
}

func (f *Fake) Ready(repo string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.get(n)
	if err != nil {
		return err
	}
	p.IsDraft = false
	f.Calls = append(f.Calls, fmt.Sprintf("ready #%d", n))
	return nil
}

// Review mimics Exec.Review: FailOn["Review"] makes the comment-review path
// fail once (so the plain-comment fallback is used); FailOn["Comment"] makes
// the fallback fail once too.
func (f *Fake) Review(repo string, n int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(n); err != nil {
		return err
	}
	kind := "review"
	if err := f.fail("Review"); err != nil {
		if err := f.fail("Comment"); err != nil {
			return err
		}
		kind = "comment"
	}
	f.Reviews = append(f.Reviews, FakeReview{Repo: repo, N: n, Kind: kind, Body: body})
	f.Calls = append(f.Calls, fmt.Sprintf("%s #%d", kind, n))
	return nil
}

func (f *Fake) AuthStatus() error { return nil }

// Merge simulates the human reviewer merging a PR.
func (f *Fake) Merge(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.PRs[n]; ok {
		p.State = "MERGED"
		p.MergedAt = "2026-11-03T20:00:00Z"
	}
}
