package plan

import "fmt"

type Status string

const (
	Planned          Status = "planned"
	InProgress       Status = "in-progress"
	Blocked          Status = "blocked"
	DirectorReview   Status = "director-review"
	ChangesRequested Status = "changes-requested"
	AwaitingYou      Status = "awaiting-you"
	FixPending       Status = "fix-pending"
	Fixing           Status = "fixing"
	Merged           Status = "merged"
	Abandoned        Status = "abandoned"
)

var AllStatuses = []Status{Planned, InProgress, Blocked, DirectorReview, ChangesRequested, AwaitingYou, FixPending, Fixing, Merged, Abandoned}

func (s Status) Known() bool {
	for _, k := range AllStatuses {
		if s == k {
			return true
		}
	}
	return false
}

// HoldsSlot reports whether a PR in this status occupies a worker slot.
func (s Status) HoldsSlot() bool {
	switch s {
	case InProgress, Blocked, DirectorReview, ChangesRequested, Fixing:
		return true
	}
	return false
}

// PassedReview: parent status counts as "passed director review" (§5).
func (s Status) PassedReview() bool {
	switch s {
	case AwaitingYou, FixPending, Fixing, Merged:
		return true
	}
	return false
}

// Started reports status ≥ in-progress (anything except planned/abandoned).
func (s Status) Started() bool { return s != Planned && s != Abandoned && s != "" }

// NeedsPR reports statuses that require a pr_number. fix-pending/fixing are
// exempt: `propagate` can send a never-opened in-progress PR to fix-pending.
func (s Status) NeedsPR() bool {
	switch s {
	case DirectorReview, ChangesRequested, AwaitingYou, Merged:
		return true
	}
	return false
}

// Dedicated statuses are set by their own command, not `prstk status`.
var dedicated = map[Status]string{
	InProgress:     "assign",
	Fixing:         "assign",
	DirectorReview: "open",
	AwaitingYou:    "ready",
	Merged:         "merged",
}

// genericTransitions lists legal `prstk status` transitions. Unblocking
// (blocked → whatever it was before) is handled separately.
var genericTransitions = map[Status][]Status{
	Planned:          {Abandoned},
	InProgress:       {Blocked, Abandoned},
	Fixing:           {Blocked, Abandoned},
	ChangesRequested: {Blocked, Abandoned},
	DirectorReview:   {ChangesRequested, Abandoned},
	AwaitingYou:      {FixPending, Abandoned},
	FixPending:       {Abandoned},
	Blocked:          {Abandoned},
	Abandoned:        {Planned},
}

// CheckTransition validates a `prstk status` transition. It returns the
// status actually to apply (unblocking maps "in-progress" back to the
// pre-block status).
func CheckTransition(from, to Status, blockedFrom Status) (Status, error) {
	if !to.Known() {
		return "", fmt.Errorf("unknown status %q", to)
	}
	if from == to {
		return to, nil // idempotent re-run
	}
	if from == Blocked && (to == InProgress || to == Fixing || to == ChangesRequested) {
		if blockedFrom != "" {
			return blockedFrom, nil
		}
		return to, nil
	}
	for _, ok := range genericTransitions[from] {
		if ok == to {
			return to, nil
		}
	}
	if cmd, ok := dedicated[to]; ok {
		return "", fmt.Errorf("%s → %s is not a `status` transition; use `prstk %s`", from, to, cmd)
	}
	return "", fmt.Errorf("illegal transition %s → %s", from, to)
}
