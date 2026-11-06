package cli

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
)

// EvidenceCommentMarker starts the one audit-evidence comment prstk keeps on
// a PR; publish edits the comment that starts with it instead of posting a
// second one.
const EvidenceCommentMarker = "<!-- prstk:evidence -->"

// maxPublishBytes is the largest capture publish pushes (GitHub warns above
// 50 MB); a variable so tests can lower it.
var maxPublishBytes int64 = 50 << 20

// beforeEvidencePush runs before each push attempt; tests use it to race
// another publisher.
var beforeEvidencePush = func(attempt int) {}

// publishFile is one capture going onto the evidence branch.
type publishFile struct {
	item  plan.EvidenceItem
	local string // absolute path under the PR's evidence dir
	path  string // path in the evidence branch: <initiative>/<id>/<phase>/<file>
	blob  string
}

type publishResult struct {
	Branch    string
	SHA       string // evidence commit the comment links to
	Committed bool   // false when the tree was unchanged and the tip was reused
	CommentID int64
	Created   bool // comment created (false: edited)
}

func (r publishResult) verb() string {
	if r.Created {
		return "created"
	}
	return "updated"
}

func byteLimit(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d-byte", n)
}

// ---- evidence publish ----

func cmdEvidencePublish(a *App, id string) error {
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		if !in.Config.PublishEvidence() {
			return "", fmt.Errorf("evidence publishing is off (evidence_publish: false in %s/config.yaml)", in.RepoDir())
		}
		r, err := a.publishEvidence(in, p)
		if err != nil {
			return "", err
		}
		return publishSummary(p, r), nil
	})
}

func publishSummary(p *plan.Spec, r publishResult) string {
	s := fmt.Sprintf("%s evidence published to %s@%s", p.ID, r.Branch, shortSHA(r.SHA))
	if !r.Committed {
		s += " (unchanged)"
	}
	return s + fmt.Sprintf(" · comment %d %s on #%d", r.CommentID, r.verb(), p.PRNumber)
}

// readyPublish is ready's publish step, after the evidence gate. It does
// nothing for a PR without evidence or captures, or when publishing is off.
// It returns a suffix for ready's success message.
func (a *App) readyPublish(in *plan.Initiative, p *plan.Spec, noPublish bool) (string, error) {
	if p.Evidence == plan.EvidenceNone || !in.Config.PublishEvidence() {
		return "", nil
	}
	m, err := plan.LoadEvidence(p)
	if err != nil {
		return "", err
	}
	if len(m.Items) == 0 {
		return "", nil
	}
	if noPublish {
		if err := in.AppendLog(a.Now(), "evidence", fmt.Sprintf("%s publish skipped at ready (--no-publish)", p.ID)); err != nil {
			return "", err
		}
		return " · evidence not published (--no-publish)", nil
	}
	r, err := a.publishEvidence(in, p)
	if err != nil {
		return "", fmt.Errorf("publishing audit evidence failed: %w\nfix it and re-run prstk ready %s, or pass --no-publish", err, p.ID)
	}
	return fmt.Sprintf(" · evidence published (%s@%s)", r.Branch, shortSHA(r.SHA)), nil
}

// publishEvidence commits p's current captures (newest before and after per
// step) to the evidence branch with git plumbing, pushes it without force,
// and creates or edits the PR's one evidence comment. It never touches a
// worktree, the index or the checked-out branch.
func (a *App) publishEvidence(in *plan.Initiative, p *plan.Spec) (publishResult, error) {
	r := publishResult{Branch: in.Config.EvidenceBranchName()}
	if p.PRNumber <= 0 {
		return r, fmt.Errorf("%s has no GitHub PR yet; run prstk open %s first", p.ID, p.ID)
	}
	if p.Evidence == plan.EvidenceNone {
		return r, fmt.Errorf("%s has no audit evidence level", p.ID)
	}
	m, err := plan.LoadEvidence(p)
	if err != nil {
		return r, err
	}
	pairs := m.Pairs()
	prefix := in.Name + "/" + p.ID
	var files []*publishFile
	for _, pr := range pairs {
		for _, it := range []*plan.EvidenceItem{pr.Before, pr.After} {
			if it == nil {
				continue
			}
			files = append(files, &publishFile{
				item:  *it,
				local: p.EvidenceFilePath(*it),
				path:  path.Join(prefix, string(it.Phase), it.File),
			})
		}
	}
	if len(files) == 0 {
		return r, fmt.Errorf("%s has no captures to publish", p.ID)
	}
	for _, f := range files {
		fi, err := os.Lstat(f.local)
		if err != nil {
			return r, fmt.Errorf("capture %s: %w", f.item.File, err)
		}
		if !fi.Mode().IsRegular() {
			return r, fmt.Errorf("capture %s is not a regular file", f.local)
		}
		if fi.Size() > maxPublishBytes {
			return r, fmt.Errorf("capture %s is %d bytes, over the %s publish limit; re-capture it smaller (prstk evidence add %s --phase %s --step %d --file <smaller file>)",
				f.local, fi.Size(), byteLimit(maxPublishBytes), p.ID, f.item.Phase, f.item.Step)
		}
	}
	repo, err := a.repoPath(in)
	if err != nil {
		return r, err
	}
	if _, err := a.Git.Run(repo, "check-ref-format", "--branch", r.Branch); err != nil {
		return r, fmt.Errorf("evidence_branch %q is not a valid branch name", r.Branch)
	}

	// Store the blobs.
	var paths []string
	for _, f := range files {
		paths = append(paths, f.local)
	}
	out, err := a.Git.RunWith(repo, nil, strings.Join(paths, "\n")+"\n", "hash-object", "-w", "--stdin-paths")
	if err != nil {
		return r, err
	}
	blobs := strings.Fields(out)
	if len(blobs) != len(files) {
		return r, fmt.Errorf("git hash-object returned %d ids for %d files", len(blobs), len(files))
	}
	for i, f := range files {
		f.blob = blobs[i]
	}

	// Commit on top of origin's tip and push; on a rejected push, rebuild on
	// the new tip once.
	for attempt := 0; ; attempt++ {
		tip, err := a.evidenceTip(repo, r.Branch)
		if err != nil {
			return r, err
		}
		tree, err := a.evidenceTree(repo, tip, prefix, files)
		if err != nil {
			return r, err
		}
		if tip != "" {
			tipTree, err := a.Git.Run(repo, "rev-parse", tip+"^{tree}")
			if err != nil {
				return r, err
			}
			if tipTree == tree {
				r.SHA = tip
				break
			}
		}
		args := []string{"commit-tree", tree, "-m", fmt.Sprintf("prstk evidence: %s (#%d)", prefix, p.PRNumber)}
		if tip != "" {
			args = append(args, "-p", tip)
		}
		sha, err := a.Git.Run(repo, args...)
		if err != nil {
			return r, err
		}
		beforeEvidencePush(attempt)
		_, perr := a.Git.Run(repo, "push", "origin", sha+":refs/heads/"+r.Branch)
		if perr == nil {
			r.SHA, r.Committed = sha, true
			break
		}
		if attempt > 0 {
			return r, fmt.Errorf("push to origin/%s was rejected again after rebuilding on the new tip (another publish in progress?); re-run to retry: %w", r.Branch, perr)
		}
		// Retry only when someone else moved the branch; otherwise the
		// failure isn't a race and retrying won't help.
		newTip, err := a.evidenceTip(repo, r.Branch)
		if err != nil {
			return r, fmt.Errorf("%w (and re-fetching failed: %v)", perr, err)
		}
		if newTip == tip {
			return r, perr
		}
	}

	head := ""
	if h, err := a.worktreeHead(in, p.ID); err == nil {
		head = h
	}
	body := evidenceComment(evidenceCommentOpts{
		Repo: in.Meta.Repo, Level: p.Evidence, Branch: r.Branch, SHA: r.SHA, Prefix: prefix,
		Pairs: pairs, Head: head, SkipReason: lastReadySkip(in, p.ID),
	})
	comments, err := a.GH.Comments(in.Meta.Repo, p.PRNumber)
	if err != nil {
		return r, err
	}
	for _, c := range comments {
		if strings.HasPrefix(strings.TrimSpace(c.Body), EvidenceCommentMarker) {
			r.CommentID = c.ID
			break
		}
	}
	if r.CommentID != 0 {
		err = a.GH.EditComment(in.Meta.Repo, r.CommentID, body)
	} else {
		r.Created = true
		r.CommentID, err = a.GH.CreateComment(in.Meta.Repo, p.PRNumber, body)
	}
	if err != nil {
		return r, err
	}
	if err := in.AppendLog(a.Now(), "evidence", fmt.Sprintf("evidence publish %s → %s@%s (comment #%d %s)", p.ID, r.Branch, shortSHA(r.SHA), r.CommentID, r.verb())); err != nil {
		return r, err
	}
	return r, nil
}

// evidenceTip fetches the evidence branch from origin and returns its tip,
// or "" when origin has no such branch yet (first publish).
func (a *App) evidenceTip(repo, branch string) (string, error) {
	ref := "refs/heads/" + branch
	out, err := a.Git.Run(repo, "ls-remote", "origin", ref)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", nil
	}
	track := "refs/remotes/origin/" + branch
	if _, err := a.Git.Run(repo, "fetch", "--no-tags", "origin", "+"+ref+":"+track); err != nil {
		return "", err
	}
	return a.Git.Run(repo, "rev-parse", "--verify", track+"^{commit}")
}

// evidenceTree builds, in a temporary index, tip's tree with prefix's subtree
// replaced by files, and returns the tree id.
func (a *App) evidenceTree(repo, tip, prefix string, files []*publishFile) (string, error) {
	dir, err := os.MkdirTemp("", "prstk-evidence-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	env := []string{"GIT_INDEX_FILE=" + dir + "/index"}
	run := func(stdin string, args ...string) (string, error) { return a.Git.RunWith(repo, env, stdin, args...) }
	if tip != "" {
		_, err = run("", "read-tree", tip)
	} else {
		_, err = run("", "read-tree", "--empty")
	}
	if err != nil {
		return "", err
	}
	old, err := run("", "ls-files", "-z", "--", ":(top,literal)"+prefix+"/")
	if err != nil {
		return "", err
	}
	var info strings.Builder
	for _, p := range strings.Split(old, "\x00") {
		if p != "" {
			fmt.Fprintf(&info, "0 0000000000000000000000000000000000000000\t%s\n", p)
		}
	}
	for _, f := range files {
		fmt.Fprintf(&info, "100644 %s\t%s\n", f.blob, f.path)
	}
	if _, err := run(info.String(), "update-index", "--index-info"); err != nil {
		return "", err
	}
	return run("", "write-tree")
}

var readySkipRe = regexp.MustCompile(`(?m)^(\S+) gate skipped at ready: (.*)$`)

// lastReadySkip is the reason of id's latest ready-gate skip in LOG.md, or "".
func lastReadySkip(in *plan.Initiative, id string) string {
	data, err := os.ReadFile(in.LogPath())
	if err != nil {
		return ""
	}
	reason := ""
	for _, m := range readySkipRe.FindAllStringSubmatch(string(data), -1) {
		if m[1] == id {
			reason = strings.TrimSpace(m[2])
		}
	}
	return reason
}

// ---- comment ----

type evidenceCommentOpts struct {
	Repo, Level, Branch, SHA string
	Prefix                   string // <initiative>/<id>
	Pairs                    []plan.EvidencePair
	Head                     string // worktree HEAD; "" when unknown (no stale marks)
	SkipReason               string // latest ready-gate skip reason, for "no after captures"
}

// evidenceComment renders the PR's evidence comment. Every link is pinned to
// the evidence commit, so later publishes don't change old links.
func evidenceComment(o evidenceCommentOpts) string {
	blob := func(it plan.EvidenceItem) string {
		return fmt.Sprintf("https://github.com/%s/blob/%s/%s/%s/%s?raw=true", o.Repo, o.SHA, o.Prefix, it.Phase, it.File)
	}
	// The capture commit is the HEAD the newest after capture was taken at.
	var newest *plan.EvidenceItem
	for i := range o.Pairs {
		if a := o.Pairs[i].After; a != nil && (newest == nil || !a.CapturedAt.Before(newest.CapturedAt)) {
			newest = a
		}
	}
	capture, hasAfter := "", newest != nil
	if hasAfter {
		capture = newest.Commit
	}

	var b strings.Builder
	b.WriteString(EvidenceCommentMarker + "\n")
	b.WriteString("### Audit evidence\n\n")
	fmt.Fprintf(&b, "**Level:** %s", o.Level)
	if capture != "" {
		fmt.Fprintf(&b, " · **After captures at:** `%s`", shortSHA(capture))
	}
	fmt.Fprintf(&b, " · **Files:** [`%s@%s`](https://github.com/%s/tree/%s/%s)\n\n", o.Branch, shortSHA(o.SHA), o.Repo, o.SHA, o.Prefix)
	if !hasAfter {
		if o.SkipReason != "" {
			fmt.Fprintf(&b, "No after captures (skipped: %s).\n\n", cellText(o.SkipReason))
		} else {
			b.WriteString("No after captures yet.\n\n")
		}
	}
	b.WriteString("| Step | Before | After |\n| --- | --- | --- |\n")
	cell := func(it *plan.EvidenceItem, after bool) string {
		if it == nil {
			return "—"
		}
		caption := strings.TrimSpace(it.Caption)
		label := caption
		if label == "" {
			label = it.File
		}
		var media string
		if it.Kind == plan.KindVideo {
			media = fmt.Sprintf("[▶ %s (video)](%s)", linkText(label), blob(*it))
		} else {
			media = fmt.Sprintf("![%s](%s)", linkText(label), blob(*it))
		}
		meta := "`" + shortSHA(it.Commit) + "`"
		if caption != "" {
			meta = cellText(caption) + " · " + meta
		}
		if after && o.Head != "" && it.Commit != o.Head {
			meta += " (stale)"
		}
		return media + "<br><sub>" + meta + "</sub>"
	}
	for _, pr := range o.Pairs {
		fmt.Fprintf(&b, "| %d | %s | %s |\n", pr.Step, cell(pr.Before, false), cell(pr.After, true))
	}
	fmt.Fprintf(&b, "\n<sub>Published by `prstk evidence publish`. Links are pinned to commit `%s` of the `%s` branch: don't delete that branch or merge it anywhere.</sub>\n\n", shortSHA(o.SHA), o.Branch)
	// So feedback --from-gh doesn't import this comment as human feedback.
	b.WriteString(plan.DirectorMarker + "\n")
	return b.String()
}

// cellText makes s safe inside a markdown table cell.
func cellText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;").Replace(s)
}

// linkText makes s safe as markdown link or image text.
func linkText(s string) string {
	return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(cellText(s))
}
