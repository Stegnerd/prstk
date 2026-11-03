package plan

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Doc is a markdown file with YAML frontmatter. The frontmatter is kept as a
// yaml.Node so that field order, comments, and unknown fields survive a
// round-trip; only keys we explicitly set are replaced.
type Doc struct {
	Path string
	node *yaml.Node // mapping node
	Body string
}

var errNoFrontmatter = errors.New("missing YAML frontmatter")

// ParseDoc splits a document into frontmatter and body.
func ParseDoc(path string, data []byte) (*Doc, error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, fmt.Errorf("%s: %w", path, errNoFrontmatter)
	}
	rest := s[4:]
	end := -1
	if strings.HasPrefix(rest, "---\n") || rest == "---" {
		end = 0
	} else if i := strings.Index(rest, "\n---\n"); i >= 0 {
		end = i + 1
	} else if strings.HasSuffix(rest, "\n---") {
		end = len(rest) - 3
	}
	if end < 0 {
		return nil, fmt.Errorf("%s: unterminated frontmatter", path)
	}
	fm := rest[:end]
	body := strings.TrimPrefix(rest[end:], "---")
	body = strings.TrimPrefix(body, "\n")

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fm), &doc); err != nil {
		return nil, fmt.Errorf("%s: frontmatter: %w", path, err)
	}
	var m *yaml.Node
	switch {
	case doc.Kind == 0: // empty
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode:
		m = doc.Content[0]
	default:
		return nil, fmt.Errorf("%s: frontmatter must be a mapping", path)
	}
	return &Doc{Path: path, node: m, Body: body}, nil
}

// LoadDoc reads and parses a document from disk.
func LoadDoc(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseDoc(path, data)
}

// NewDoc creates an empty document at path (not yet written).
func NewDoc(path, body string) *Doc {
	return &Doc{Path: path, node: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, Body: body}
}

// Decode decodes the frontmatter into v.
func (d *Doc) Decode(v any) error {
	if err := d.node.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", d.Path, err)
	}
	return nil
}

// Set merges the keys of mapping m into the frontmatter. Existing keys keep
// their position and comments; new keys are appended.
func (d *Doc) Set(m *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		found := false
		for j := 0; j+1 < len(d.node.Content); j += 2 {
			if d.node.Content[j].Value == k.Value {
				old := d.node.Content[j+1]
				v.LineComment = old.LineComment
				v.HeadComment = old.HeadComment
				v.FootComment = old.FootComment
				d.node.Content[j+1] = v
				found = true
				break
			}
		}
		if !found {
			d.node.Content = append(d.node.Content, k, v)
		}
	}
}

// Bytes renders the document.
func (d *Doc) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	if len(d.node.Content) > 0 {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(d.node); err != nil {
			return nil, err
		}
		enc.Close()
	}
	buf.WriteString("---\n")
	buf.WriteString(d.Body)
	return buf.Bytes(), nil
}

// Save writes the document atomically.
func (d *Doc) Save() error {
	b, err := d.Bytes()
	if err != nil {
		return err
	}
	return WriteFileAtomic(d.Path, b)
}

// WriteFileAtomic writes to a temp file in the same directory, then renames.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// --- node builders ---

type mapBuilder struct{ n *yaml.Node }

func newMap() *mapBuilder { return &mapBuilder{&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}} }

func (b *mapBuilder) add(key string, v *yaml.Node) *mapBuilder {
	b.n.Content = append(b.n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return b
}

func scalar(v any) *yaml.Node {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		panic(err)
	}
	return &n
}

func quoted(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle}
}

func nullable(s string) *yaml.Node {
	if s == "" {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	return quoted(s)
}

func quotedList(ss []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, s := range ss {
		n.Content = append(n.Content, quoted(s))
	}
	return n
}
