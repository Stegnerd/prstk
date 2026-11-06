package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
)

const timeFmt = "2006-01-02 15:04"

// readSource reads a message file; relative paths resolve against the cwd.
func (a *App) readSource(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Cwd, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cmdLog(a *App, typ string, msg []string, file string) error {
	switch typ {
	case "review", "feedback", "replan":
	default:
		return usageError{fmt.Sprintf("unknown log type %q (want review, feedback or replan)", typ)}
	}
	if (len(msg) > 0) == (file != "") {
		return usageError{"give the message as arguments or with --file, not both and not neither"}
	}
	body := strings.Join(msg, " ")
	if file != "" {
		var err error
		if body, err = a.readSource(file); err != nil {
			return err
		}
	}
	if strings.TrimSpace(body) == "" {
		return usageError{"empty message"}
	}
	return a.mutate(func(in *plan.Initiative) (string, error) {
		now := a.Now()
		if err := in.AppendEntry(now, typ, "director", body); err != nil {
			return "", err
		}
		return fmt.Sprintf("logged %s · %s", typ, now.Format(timeFmt)), nil
	})
}

type reviewOpts struct {
	id, file, verdict, summary string
	noPost                     bool
}

// firstLine is the first non-empty, non-heading line of s.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			return l
		}
	}
	return ""
}

func cmdReview(a *App, o reviewOpts) error {
	heading := plan.VerdictHeading(o.verdict)
	if heading == "" {
		return usageError{fmt.Sprintf("--verdict must be approved, approved-with-nits or changes-requested, got %q", o.verdict)}
	}
	body, err := a.readSource(o.file)
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%s is empty", o.file)
	}
	summary := strings.TrimSpace(o.summary)
	if summary == "" {
		summary = firstLine(body)
	}

	// Post first: if both gh paths fail, nothing is written locally.
	in, err := a.load()
	if err != nil {
		return err
	}
	p := in.PR(o.id)
	if p == nil {
		return fmt.Errorf("no PR %q in initiative %s", o.id, in.Name)
	}
	posted := p.PRNumber
	post := p.PRNumber > 0 && !o.noPost
	if post {
		posting := strings.TrimRight(body, " \t\r\n") + "\n\n" + plan.DirectorMarker + "\n"
		if err := a.GH.Review(in.Meta.Repo, p.PRNumber, posting); err != nil {
			return fmt.Errorf("posting review to #%d failed (nothing recorded): %w", p.PRNumber, err)
		}
	}
	err = a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(o.id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", o.id)
		}
		now := a.Now()
		h := heading
		if p.PRNumber == 0 {
			h += plan.BeforeOpenedSuffix
		}
		round, err := p.AppendDirectorRound(now, h, body)
		if err != nil {
			return "", err
		}
		entry := fmt.Sprintf("%s round %d: %s.", o.id, round, o.verdict)
		if summary != "" {
			entry += " " + summary
		}
		if err := in.AppendEntry(now, "review", "director", entry); err != nil {
			return "", err
		}
		where := "not posted"
		if post {
			where = fmt.Sprintf("posted #%d", posted)
		}
		return fmt.Sprintf("review %s round %d · %s · %s", o.id, round, o.verdict, where), nil
	})
	if err != nil && post {
		return fmt.Errorf("review was posted to #%d but not recorded locally: %w (re-run with --no-post to record it without posting again)", posted, err)
	}
	return err
}
