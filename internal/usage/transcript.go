package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// message is one deduplicated assistant message.
type message struct {
	key       string
	at        time.Time
	sidechain bool
	model     string
	tokens    Tokens
	bash      []bashCall
}

// counted reports whether the message's usage counts (real model, nonzero).
func (m *message) counted() bool {
	return m.model != "<synthetic>" && m.tokens.Total() > 0
}

type bashCall struct {
	id, command string
}

// transcript is a parsed .jsonl file.
type transcript struct {
	msgs      []*message      // in file order of first appearance
	errored   map[string]bool // tool_use ids whose result was an error
	malformed int
}

type rawLine struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	RequestID   string          `json:"requestId"`
	UUID        string          `json:"uuid"`
	Message     json.RawMessage `json:"message"`
}

type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreation            *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u rawUsage) tokens() Tokens {
	t := Tokens{Input: u.InputTokens, CacheRead: u.CacheReadInputTokens, Output: u.OutputTokens}
	if u.CacheCreation != nil {
		t.CacheWrite5m, t.CacheWrite1h = u.CacheCreation.Ephemeral5m, u.CacheCreation.Ephemeral1h
	} else {
		t.CacheWrite5m = u.CacheCreationInputTokens
	}
	return t
}

type rawMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Usage   *rawUsage       `json:"usage"`
	Content json.RawMessage `json:"content"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

func blocks(content json.RawMessage) []rawBlock {
	content = bytes.TrimSpace(content)
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var bs []rawBlock
	if json.Unmarshal(content, &bs) != nil {
		return nil
	}
	return bs
}

// parseTranscript reads a Claude Code transcript. Claude Code writes one
// line per content block, each repeating the message's usage, so lines are
// merged by message id (then requestId, then uuid).
func parseTranscript(path string) (*transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readTranscript(f)
}

func readTranscript(r io.Reader) (*transcript, error) {
	t := &transcript{errored: map[string]bool{}}
	byKey := map[string]*message{}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			t.line(line, byKey)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *transcript) line(line []byte, byKey map[string]*message) {
	var l rawLine
	if err := json.Unmarshal(line, &l); err != nil {
		t.malformed++
		return
	}
	switch l.Type {
	case "assistant":
		var m rawMessage
		if json.Unmarshal(l.Message, &m) != nil {
			t.malformed++
			return
		}
		at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		key := m.ID
		if key == "" {
			key = "req:" + l.RequestID
			if l.RequestID == "" {
				key = "uuid:" + l.UUID
			}
		}
		msg := byKey[key]
		if msg == nil {
			msg = &message{key: key, at: at, sidechain: l.IsSidechain}
			byKey[key] = msg
			t.msgs = append(t.msgs, msg)
		}
		if msg.at.IsZero() || (!at.IsZero() && at.Before(msg.at)) {
			msg.at = at
		}
		if m.Model != "" {
			msg.model = m.Model
		}
		if m.Usage != nil {
			msg.tokens.max(m.Usage.tokens())
		}
		for _, b := range blocks(m.Content) {
			if b.Type != "tool_use" || b.Name != "Bash" {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
				msg.bash = append(msg.bash, bashCall{id: b.ID, command: in.Command})
			}
		}
	case "user":
		if !bytes.Contains(l.Message, []byte(`"is_error"`)) {
			return
		}
		var m rawMessage
		if json.Unmarshal(l.Message, &m) != nil {
			return
		}
		for _, b := range blocks(m.Content) {
			if b.Type == "tool_result" && b.IsError {
				t.errored[b.ToolUseID] = true
			}
		}
	}
}

// subagentMeta is the part of agent-<x>.meta.json we read.
type subagentMeta struct {
	Name string `json:"name"`
}

var agentFileRe = regexp.MustCompile(`^agent-a(.+)-[0-9a-f]{16}\.jsonl$`)

// subagentName is the subagent's name from its sibling .meta.json, or else
// from its file name (agent-a<name>-<16 hex>.jsonl). "" when neither says.
func subagentName(path string) string {
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		var m subagentMeta
		if json.Unmarshal(b, &m) == nil && m.Name != "" {
			return m.Name
		}
	}
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	if m := agentFileRe.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	return ""
}
