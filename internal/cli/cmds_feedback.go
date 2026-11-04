package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/gh"
	"github.com/Stegnerd/prstk/internal/plan"
)

type feedbackOpts struct {
	id, file, author string
	fromGH           bool
}

// cmdFeedback records the human reviewer's feedback verbatim under
// "## Review: <reviewer>" in the spec and logs a feedback entry. The text
// comes from a file (feedback given in chat) or from the PR on GitHub.
func cmdFeedback(a *App, o feedbackOpts) error {
	if o.fromGH == (o.file != "") {
		return usageError{"give exactly one of --from-gh or --file"}
	}
	if o.author != "" && !o.fromGH {
		return usageError{"--author only applies with --from-gh"}
	}
	if o.file != "" {
		body, err := a.readSource(o.file)
		if err != nil {
			return err
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("%s is empty", o.file)
		}
		return a.mutate(func(in *plan.Initiative) (string, error) {
			p := in.PR(o.id)
			if p == nil {
				return "", fmt.Errorf("no PR %q", o.id)
			}
			now := a.Now()
			name := in.Config.ReviewerName()
			if err := p.AppendReviewerFeedback(name, now.Format(timeFmt)+" · chat", body); err != nil {
				return "", err
			}
			entry := fmt.Sprintf("%s: %s's feedback recorded (chat). %s", o.id, name, firstLine(body))
			if err := in.AppendEntry(now, "feedback", "director", entry); err != nil {
				return "", err
			}
			return fmt.Sprintf("recorded %s's feedback on %s · %s", name, o.id, feedbackHint(o.id)), nil
		})
	}

	// From GitHub: fetch outside the lock (network), filter again inside it.
	in, err := a.load()
	if err != nil {
		return err
	}
	p := in.PR(o.id)
	if p == nil {
		return fmt.Errorf("no PR %q in initiative %s", o.id, in.Name)
	}
	if p.PRNumber <= 0 {
		return fmt.Errorf("%s has no PR yet", o.id)
	}
	items, err := a.GH.Feedback(in.Meta.Repo, p.PRNumber)
	if err != nil {
		return err
	}
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(o.id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", o.id)
		}
		fresh := newFeedback(items, p.Body(), o.author)
		if len(fresh) == 0 {
			return fmt.Sprintf("no new feedback on #%d", p.PRNumber), nil
		}
		var blocks []string
		for _, it := range fresh {
			blocks = append(blocks, renderFeedbackItem(it))
		}
		now := a.Now()
		name := in.Config.ReviewerName()
		title := fmt.Sprintf("%s · GitHub #%d", now.Format(timeFmt), p.PRNumber)
		if err := p.AppendReviewerFeedback(name, title, strings.Join(blocks, "\n\n")); err != nil {
			return "", err
		}
		entry := fmt.Sprintf("%s: imported %d item(s) of %s's feedback from #%d into spec.md.", o.id, len(fresh), name, p.PRNumber)
		if err := in.AppendEntry(now, "feedback", "director", entry); err != nil {
			return "", err
		}
		return fmt.Sprintf("imported %d item(s) from #%d into %s · %s", len(fresh), p.PRNumber, o.id, feedbackHint(o.id)), nil
	})
}

func feedbackHint(id string) string {
	return fmt.Sprintf("if it needs changes: prstk status %s fix-pending", id)
}

// newFeedback drops items already imported (their marker is in specBody),
// reviews prstk posted for the director (marked, or already in the spec
// verbatim), empty comment-only reviews (the
// containers of inline comments), and, when author is set, other authors.
func newFeedback(items []gh.FeedbackItem, specBody, author string) []gh.FeedbackItem {
	var out []gh.FeedbackItem
	for _, it := range items {
		switch {
		case strings.Contains(specBody, plan.FeedbackMarker(it.Kind, it.ID)):
		case strings.Contains(it.Body, plan.DirectorMarker):
		case strings.TrimSpace(it.Body) != "" && strings.Contains(specBody, strings.TrimSpace(it.Body)):
			// Already in the spec verbatim: a director round posted before
			// prstk added DirectorMarker.
		case author != "" && !strings.EqualFold(it.Author, author):
		case strings.TrimSpace(it.Body) == "" && (it.Kind != "review" || it.State == "COMMENTED" || it.State == "PENDING"):
		default:
			out = append(out, it)
		}
	}
	return out
}

// renderFeedbackItem renders one item: a marker, a header line, and the body
// verbatim as a block quote (so its headings and fences can't break the spec).
func renderFeedbackItem(it gh.FeedbackItem) string {
	var head string
	switch it.Kind {
	case "review":
		head = "**review**"
		if st := strings.ToLower(strings.ReplaceAll(it.State, "_", " ")); st != "" && st != "commented" {
			head += " (" + st + ")"
		}
	case "inline":
		loc := it.Path
		if it.Line > 0 {
			loc = fmt.Sprintf("%s:%d", it.Path, it.Line)
		} else {
			loc += " (outdated)"
		}
		head = "**`" + loc + "`**"
		if it.ReplyTo != 0 {
			head += " (reply)"
		}
	default:
		head = "**comment**"
	}
	if it.Author != "" {
		head += " · @" + it.Author
	}
	if t, err := time.Parse(time.RFC3339, it.At); err == nil {
		head += " · " + t.Local().Format(timeFmt)
	}
	body := strings.TrimRight(strings.ReplaceAll(it.Body, "\r\n", "\n"), " \t\n")
	if body == "" {
		body = "(no text)"
	}
	var q []string
	for _, l := range strings.Split(body, "\n") {
		if l == "" {
			q = append(q, ">")
		} else {
			q = append(q, "> "+l)
		}
	}
	return plan.FeedbackMarker(it.Kind, it.ID) + "\n" + head + "\n\n" + strings.Join(q, "\n")
}
