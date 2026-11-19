package usage

import (
	"reflect"
	"testing"
)

func TestParseInvocations(t *testing.T) {
	type inv = invocation
	for _, tc := range []struct {
		cmd  string
		want []inv
	}{
		{"prstk show 03", []inv{{Sub: "show", Args: []string{"03"}}}},
		{"prstk assign 02 --worker w-02 && prstk assign 05 --worker w-05 && prstk spawn-prompt 02",
			[]inv{{Sub: "assign", Args: []string{"02"}}, {Sub: "assign", Args: []string{"05"}}, {Sub: "spawn-prompt", Args: []string{"02"}}}},
		{"$PRSTK_BIN review 03 --file /tmp/r.md --verdict approved", []inv{{Sub: "review", Args: []string{"03"}}}},
		{`"${PRSTK_BIN}" open 04`, []inv{{Sub: "open", Args: []string{"04"}}}},
		{"PRSTK_HOME=/tmp/x FOO=bar prstk status 03 blocked --reason 'waits; on | 02'", []inv{{Sub: "status", Args: []string{"03", "blocked"}}}},
		{"cd /repo && /tmp/build/prstk-dev ready 01", []inv{{Sub: "ready", Args: []string{"01"}}}},
		{"ls; prstk doctor | head\nprstk next", []inv{{Sub: "doctor"}, {Sub: "next"}}},
		{"prstk --initiative 2026-01-01-0000-other show 01", []inv{{Sub: "show", Args: []string{"01"}, Initiative: "2026-01-01-0000-other"}}},
		{"prstk evidence add 02 --phase before --file x.png --caption \"step one\"", []inv{{Sub: "evidence", Args: []string{"add", "02"}}}},
		{"prstk add 04 slug --title \"Two words\" --parent 02", []inv{{Sub: "add", Args: []string{"04", "slug"}}}},
		{"gh pr diff 13", nil},
		{"cat prs/03-foo/spec.md", nil},
		{"go build -o /tmp/prstk ./cmd/prstk", nil},
		{"echo prstk show 03", nil},
		{"prstk", []inv{{}}},
	} {
		if got := parseInvocations(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q:\ngot  %+v\nwant %+v", tc.cmd, got, tc.want)
		}
	}
}

func TestPRID(t *testing.T) {
	for cmd, want := range map[string]string{
		"prstk show 03":                  "03",
		"prstk evidence add 02 --step 1": "02",
		"prstk evidence ls 05":           "05",
		"prstk evidence":                 "",
		"prstk validate":                 "",
		"prstk add 01 x --title y":       "",
		"prstk merged 06":                "06",
	} {
		if got := parseInvocations(cmd)[0].prID(); got != want {
			t.Errorf("%q: %q, want %q", cmd, got, want)
		}
	}
}
