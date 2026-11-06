// Package prstk embeds the agent definitions so `prstk setup` can install
// them without a checkout. The files under agents/ stay the single source.
package prstk

import "embed"

// Agents holds agents/prstk-director.md and agents/prstk-worker.md.
//
//go:embed agents/prstk-director.md agents/prstk-worker.md
var Agents embed.FS
