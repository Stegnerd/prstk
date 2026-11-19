// Package usage turns Claude Code session transcripts into an initiative's
// token usage (usage.json): split between the director and the workers, per
// PR and in total, with an estimated cost.
//
// # Sources
//
// prstk records the director's session ids (CLAUDE_CODE_SESSION_ID) in
// PLAN.md as mutating commands run. For each id, the director transcript is
// <claude>/projects/*/<id>.jsonl and the in-process teammates' transcripts
// are <claude>/projects/*/<id>/subagents/agent-*.jsonl, named by the sibling
// .meta.json (or the file name agent-a<name>-<16 hex>.jsonl). A subagent
// whose name is a worker assigned in LOG.md is a worker; any other subagent
// (Explore, a setup helper…) is director overhead ("other agents").
//
// A director that restarts in a new session is picked up by its first
// mutating prstk command; read-only commands (next, show) don't record, so
// its tokens since the restart appear only after the next mutating command.
// Teammates running as separate processes (tmux mode) aren't found.
//
// # Counting
//
// Only assistant lines with message.usage count. Claude Code writes one line
// per content block, each repeating the message's usage, so lines are merged
// by message.id (then requestId, then uuid). Cache writes are split into the
// 5-minute and 1-hour kinds from usage.cache_creation; without it, all of
// cache_creation_input_tokens counts as 5-minute. <synthetic> and zero-usage
// messages are skipped.
//
// # Window
//
// The initiative ends at the first director `prstk teardown` call at or
// after PLAN.md's created time (calls whose tool result was an error, and
// calls with --initiative naming another initiative, don't count). Each
// director session starts after its last teardown call before created, so
// planning done in the same conversation before `prstk init` counts, but a
// reused session's earlier initiative doesn't. Worker messages count from
// created to the end.
//
// # Attributing director tokens (a heuristic)
//
// Workers map to PRs through LOG.md assign entries; a name assigned more
// than once goes, per message, to the PR whose assign most recently precedes
// it. Director tokens are segmented by the prstk commands the director runs:
// walking its messages in time order, tokens pile up in a pending bucket
// until a message runs a Bash command with a prstk call. A per-PR call
// (assign, check, open, review, status, ready, feedback, propagate, merged,
// show, spawn-prompt with a PR id of this initiative, or evidence <sub> <id>)
// sends the pending tokens to that PR, split evenly between the distinct ids
// named; any other prstk call (init, add, validate, next, log, …) sends them
// to overhead. Whatever is pending at the end is overhead. Commands are
// split on newlines, &&, ||, ;, | and & outside quotes, and prstk must be the
// command word (after env assignments), as prstk, a path ending in prstk or
// prstk-*, or $PRSTK_BIN. So "the director was working on PR 03" means "the
// next prstk command it ran named 03": a guess, not a measurement.
//
// # Snapshot
//
// Claude Code deletes old transcripts, so usage.json keeps each source
// file's attributed contribution. A refresh recomputes sources that still
// exist (skipping files whose size, mtime and context are unchanged), keeps
// the stored contribution of deleted ones (marked missing), and re-derives
// every total from the sources. Costs are re-priced on every refresh.
package usage
