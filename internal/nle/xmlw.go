package nle

// A tiny XML tree for the writers: attribute order is kept as written (the
// editors' own files are easier to compare that way) and the output is
// indented with tabs.

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
)

type node struct {
	name  string
	attrs [][2]string
	kids  []*node
	text  string
	set   bool // text set (an element with empty text is <a></a>, not <a/>)
}

func el(name string, attrs ...string) *node {
	n := &node{name: name}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.attrs = append(n.attrs, [2]string{attrs[i], attrs[i+1]})
	}
	return n
}

// attr adds an attribute (skipped when v is "").
func (n *node) attr(k, v string) *node {
	if v != "" {
		n.attrs = append(n.attrs, [2]string{k, v})
	}
	return n
}

func (n *node) add(kids ...*node) *node {
	for _, k := range kids {
		if k != nil {
			n.kids = append(n.kids, k)
		}
	}
	return n
}

// child adds and returns a new child element.
func (n *node) child(name string, attrs ...string) *node {
	c := el(name, attrs...)
	n.kids = append(n.kids, c)
	return c
}

// leaf adds <name>text</name>.
func (n *node) leaf(name, text string) *node {
	c := el(name)
	c.text, c.set = text, true
	n.kids = append(n.kids, c)
	return n
}

func (n *node) leafInt(name string, v int64) *node { return n.leaf(name, strconv.FormatInt(v, 10)) }

func escText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func escAttr(s string) string {
	s = escText(s)
	return strings.ReplaceAll(s, `"`, "&quot;")
}

func (n *node) write(b *strings.Builder, depth int) {
	ind := strings.Repeat("\t", depth)
	b.WriteString(ind + "<" + n.name)
	for _, a := range n.attrs {
		b.WriteString(" " + a[0] + `="` + escAttr(a[1]) + `"`)
	}
	switch {
	case len(n.kids) > 0:
		b.WriteString(">\n")
		for _, k := range n.kids {
			k.write(b, depth+1)
		}
		b.WriteString(ind + "</" + n.name + ">\n")
	case n.set:
		b.WriteString(">" + escText(n.text) + "</" + n.name + ">\n")
	default:
		b.WriteString("/>\n")
	}
}

// document renders root with an XML declaration and an optional DOCTYPE line.
func document(root *node, doctype string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	if doctype != "" {
		b.WriteString(doctype + "\n")
	}
	root.write(&b, 0)
	return []byte(b.String())
}

func ftoa(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}
