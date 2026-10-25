// Go port of djvulibre's libdjvu/DjVuAnno.cpp and libdjvu/miniexp.cpp.
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

package djvu

import (
	"fmt"
	"strings"
)

// Node is one node of the parenthesized annotation syntax used by ANTa
// and ANTz chunks ("(maparea ...)", "(metadata ...)", "(zoom stretch)",
// and so on).
//
// A node is either a text token (a bare symbol such as stretch or #FF0000,
// or a quoted string) or a list of nodes. Annotation files are a
// sequence of top-level list nodes, each starting with a symbol that
// names the construct ("maparea", "metadata", "zoom", "mode", "align",
// "background", ...). Unknown constructs are preserved as parsed.
type Node struct {
	// IsList reports whether the node is a parenthesized list.
	IsList bool
	// Text holds the token text when IsList is false. The distinction
	// between quoted strings and bare symbols is not preserved.
	Text string
	// List holds the child nodes when IsList is true.
	List []Node
}

// Key returns the leading token of a list node, or the token text of a
// text node. It is the construct name for annotation expressions.
func (n Node) Key() string {
	if n.IsList && len(n.List) > 0 {
		return n.List[0].Text
	}
	return n.Text
}

// String reconstructs a printable form of the node.
func (n Node) String() string {
	if !n.IsList {
		return n.Text
	}
	var b strings.Builder
	b.WriteByte('(')
	for i, c := range n.List {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(c.String())
	}
	b.WriteByte(')')
	return b.String()
}

// Annotations holds the annotation expressions of a page: the contents of
// its ANTa or ANTz chunks, plus the annotations of included component
// files and the document's shared annotation file.
type Annotations []Node

// Metadata returns the (metadata ...) entries found in the annotations,
// flattened into key/value pairs. Later entries overwrite earlier ones
// with the same key. Keys are matched case-insensitively.
func (a Annotations) Metadata() map[string]string {
	meta := make(map[string]string)
	for _, n := range a {
		if !n.IsList || n.Key() != "metadata" {
			continue
		}
		for _, entry := range n.List[1:] {
			if !entry.IsList || len(entry.List) < 2 {
				continue
			}
			k := entry.List[0].Text
			v := entry.List[1].Text
			meta[strings.ToLower(k)] = v
		}
	}
	return meta
}

// annoParser parses the annotation syntax. It follows the GLParser of
// the reference implementation (DjVuAnno.cpp): tokens are parenthesized
// lists, quoted strings with backslash escapes (octal escapes and the
// usual single character escapes), and bare symbols terminated by
// whitespace or a closing parenthesis. Malformed constructs are
// tolerated: parsing stops at the first unrecoverable error and returns
// the expressions parsed so far, like the reference implementation which
// silently ignores erroneous constructs.
type annoParser struct {
	data []byte
	pos  int
}

// parseAnnotations parses the concatenated contents of annotation chunks.
func parseAnnotations(data []byte) Annotations {
	p := &annoParser{data: data}
	var out Annotations
	for {
		n, ok := p.parseNode()
		if !ok {
			break
		}
		if n.IsList {
			out = append(out, n)
		}
	}
	return out
}

func (p *annoParser) skipSpace() {
	for p.pos < len(p.data) && isAnnoSpace(p.data[p.pos]) {
		p.pos++
	}
}

func (p *annoParser) parseNode() (Node, bool) {
	p.skipSpace()
	if p.pos >= len(p.data) {
		return Node{}, false
	}
	switch p.data[p.pos] {
	case '(':
		p.pos++
		return p.parseList()
	case ')':
		// Unbalanced closing parenthesis: tolerate and stop.
		return Node{}, false
	case '"':
		text, ok := p.parseString()
		if !ok {
			return Node{}, false
		}
		return Node{Text: text}, true
	default:
		start := p.pos
		for p.pos < len(p.data) && p.data[p.pos] != ')' && !isAnnoSpace(p.data[p.pos]) {
			p.pos++
		}
		if p.pos == start {
			return Node{}, false
		}
		return Node{Text: string(p.data[start:p.pos])}, true
	}
}

func (p *annoParser) parseList() (Node, bool) {
	list := Node{IsList: true}
	for {
		p.skipSpace()
		if p.pos >= len(p.data) {
			// Unterminated list: tolerate like the reference parser.
			break
		}
		if p.data[p.pos] == ')' {
			p.pos++
			break
		}
		n, ok := p.parseNode()
		if !ok {
			break
		}
		list.List = append(list.List, n)
	}
	return list, true
}

// parseString parses a quoted string with the escapes understood by the
// reference implementation: up to three octal digits after a backslash,
// or one of the characters t, n, r, b, f, v, a, " and \.
func (p *annoParser) parseString() (string, bool) {
	p.pos++ // opening quote
	var b strings.Builder
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		switch c {
		case '"':
			p.pos++
			return b.String(), true
		case '\\':
			p.pos++
			if p.pos >= len(p.data) {
				return "", false
			}
			e := p.data[p.pos]
			if e >= '0' && e <= '7' {
				x := 0
				for i := 0; i < 3 && p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '7'; i++ {
					x = x*8 + int(p.data[p.pos]-'0')
					p.pos++
				}
				b.WriteByte(byte(x & 0xff))
			} else {
				switch e {
				case 't':
					b.WriteByte('\t')
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 'b':
					b.WriteByte('\b')
				case 'f':
					b.WriteByte('\f')
				case 'v':
					b.WriteByte(0x0b)
				case 'a':
					b.WriteByte(0x07)
				default:
					// covers " and \ and anything else literally
					b.WriteByte(e)
				}
				p.pos++
			}
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", false // unterminated string
}

func isAnnoSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// Metadata holds the document-level strings stored in (metadata ...)
// annotation expressions. The five named fields cover the keys
// conventionally used by DjVu software; any other key (year, booktitle,
// ...) is collected in Others.
type Metadata struct {
	// Title is the document title.
	Title string
	// Author is the document author.
	Author string
	// Subject is the document subject.
	Subject string
	// Keywords holds the document keywords.
	Keywords string
	// Producer names the software that produced the document.
	Producer string
	// Others holds all metadata keys that do not map to a field above.
	Others map[string]string
}

// Get returns the value of the metadata entry with the given key, or the
// empty string. Keys are matched case-insensitively.
func (m Metadata) Get(key string) string {
	switch strings.ToLower(key) {
	case "title":
		return m.Title
	case "author":
		return m.Author
	case "subject":
		return m.Subject
	case "keywords":
		return m.Keywords
	case "producer":
		return m.Producer
	}
	if m.Others != nil {
		for k, v := range m.Others {
			if strings.EqualFold(k, key) {
				return v
			}
		}
	}
	return ""
}

// metadataFrom collects metadata entries from raw annotation expressions
// in scan order; later entries overwrite earlier ones.
func metadataFrom(entries map[string]string) Metadata {
	var m Metadata
	for k, v := range entries {
		switch k {
		case "title":
			m.Title = v
		case "author":
			m.Author = v
		case "subject":
			m.Subject = v
		case "keywords":
			m.Keywords = v
		case "producer":
			m.Producer = v
		default:
			if m.Others == nil {
				m.Others = make(map[string]string)
			}
			m.Others[k] = v
		}
	}
	return m
}

// errIndirect is returned by the page data accessors of indirect documents.
var errIndirect = fmt.Errorf("djvu: page data is stored in external files for indirect documents")
