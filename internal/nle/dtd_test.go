package nle

// A DTD check of the FCPXML files: element content models (as regular
// expressions over the child names), declared/required/enumerated
// attributes, ID/IDREF. Apple's DTD is copyrighted and not shipped — point
// FCPXML_DTD at FCPXMLv1_10.dtd (inside Final Cut Pro, or the copy many
// FCPXML tools mirror) to run it; FCPXML_FILES (paths separated by the OS
// list separator) adds more files to check, e.g. a real export.

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type dtdAttr struct {
	name, typ, def string
	enum           []string
}

type dtd struct {
	models map[string]*regexp.Regexp // element → model over "name;" sequences
	mixed  map[string]bool           // #PCDATA allowed
	attrs  map[string]map[string]dtdAttr
}

var (
	reComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reEntity  = regexp.MustCompile(`(?s)<!ENTITY\s+%\s+([\w.-]+)\s+(?:"([^"]*)"|'([^']*)')\s*>`)
	reElement = regexp.MustCompile(`(?s)<!ELEMENT\s+([\w.:-]+)\s+(.*?)>`)
	reAttlist = regexp.MustCompile(`(?s)<!ATTLIST\s+([\w.:-]+)\s+(.*?)>`)
	reToken   = regexp.MustCompile(`#PCDATA|[\w.:-]+|[()|,?*+]`)
	reAttrDef = regexp.MustCompile(`(?s)([\w.:-]+)\s+(\([^)]*\)|[A-Z]+)\s+(#REQUIRED|#IMPLIED|#FIXED\s+(?:"[^"]*"|'[^']*')|"[^"]*"|'[^']*')`)
)

func parseDTD(text string) (*dtd, error) {
	text = reComment.ReplaceAllString(text, "")
	ents := map[string]string{}
	for _, m := range reEntity.FindAllStringSubmatch(text, -1) {
		ents[m[1]] = m[2] + m[3]
	}
	text = reEntity.ReplaceAllString(text, "")
	expand := func(s string) string {
		for i := 0; i < 20 && strings.Contains(s, "%"); i++ {
			for k, v := range ents {
				s = strings.ReplaceAll(s, "%"+k+";", v)
			}
		}
		return s
	}
	text = expand(text)
	d := &dtd{models: map[string]*regexp.Regexp{}, mixed: map[string]bool{}, attrs: map[string]map[string]dtdAttr{}}
	for _, m := range reElement.FindAllStringSubmatch(text, -1) {
		name, model := m[1], strings.TrimSpace(m[2])
		switch model {
		case "EMPTY":
			d.models[name] = regexp.MustCompile(`^$`)
			continue
		case "ANY":
			d.models[name] = regexp.MustCompile(`^.*$`)
			d.mixed[name] = true
			continue
		}
		var b strings.Builder
		for _, tok := range reToken.FindAllString(model, -1) {
			switch tok {
			case "(":
				b.WriteString("(?:")
			case ")", "|", "?", "*", "+":
				b.WriteString(tok)
			case ",":
			case "#PCDATA":
				d.mixed[name] = true
				b.WriteString("(?:)")
			default:
				b.WriteString("(?:" + regexp.QuoteMeta(tok) + ";)")
			}
		}
		re, err := regexp.Compile("^(?:" + b.String() + ")$")
		if err != nil {
			return nil, fmt.Errorf("element %s: %v (%s)", name, err, model)
		}
		d.models[name] = re
	}
	for _, m := range reAttlist.FindAllStringSubmatch(text, -1) {
		el := m[1]
		if d.attrs[el] == nil {
			d.attrs[el] = map[string]dtdAttr{}
		}
		for _, a := range reAttrDef.FindAllStringSubmatch(m[2], -1) {
			at := dtdAttr{name: a[1], typ: a[2], def: a[3]}
			if strings.HasPrefix(at.typ, "(") {
				for _, v := range strings.Split(strings.Trim(at.typ, "()"), "|") {
					at.enum = append(at.enum, strings.TrimSpace(v))
				}
			}
			d.attrs[el][at.name] = at
		}
	}
	if len(d.models) < 50 {
		return nil, fmt.Errorf("only %d elements parsed", len(d.models))
	}
	return d, nil
}

// validate checks doc against d; every problem is returned.
func (d *dtd) validate(doc []byte) []error {
	var errs []error
	type frame struct {
		name string
		kids strings.Builder
		text bool
	}
	var stack []*frame
	ids := map[string]bool{}
	var refs []string
	dec := xml.NewDecoder(strings.NewReader(string(doc)))
	dec.Strict = true
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return append(errs, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if len(stack) > 0 {
				stack[len(stack)-1].kids.WriteString(name + ";")
			}
			if _, ok := d.models[name]; !ok {
				errs = append(errs, fmt.Errorf("<%s> is not declared", name))
			}
			decl := d.attrs[name]
			seen := map[string]bool{}
			for _, a := range t.Attr {
				seen[a.Name.Local] = true
				def, ok := decl[a.Name.Local]
				if !ok {
					errs = append(errs, fmt.Errorf("<%s %s=…>: attribute not declared", name, a.Name.Local))
					continue
				}
				if len(def.enum) > 0 && !contains(def.enum, a.Value) {
					errs = append(errs, fmt.Errorf("<%s %s=%q>: not one of %v", name, a.Name.Local, a.Value, def.enum))
				}
				if strings.HasPrefix(def.def, "#FIXED") && !strings.Contains(def.def, `"`+a.Value+`"`) {
					errs = append(errs, fmt.Errorf("<%s %s=%q>: fixed %s", name, a.Name.Local, a.Value, def.def))
				}
				switch def.typ {
				case "ID":
					ids[a.Value] = true
				case "IDREF":
					refs = append(refs, a.Value)
				}
			}
			for an, def := range decl {
				if def.def == "#REQUIRED" && !seen[an] {
					errs = append(errs, fmt.Errorf("<%s>: required attribute %s missing", name, an))
				}
			}
			stack = append(stack, &frame{name: name})
		case xml.CharData:
			if len(stack) > 0 && strings.TrimSpace(string(t)) != "" {
				stack[len(stack)-1].text = true
			}
		case xml.EndElement:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if re, ok := d.models[f.name]; ok && !re.MatchString(f.kids.String()) {
				errs = append(errs, fmt.Errorf("<%s> children %q do not match its content model", f.name, f.kids.String()))
			}
			if f.text && !d.mixed[f.name] {
				errs = append(errs, fmt.Errorf("<%s> has text", f.name))
			}
		}
	}
	for _, r := range refs {
		if !ids[r] {
			errs = append(errs, fmt.Errorf("IDREF %q points nowhere", r))
		}
	}
	return errs
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestFCPXMLAgainstDTD(t *testing.T) {
	path := os.Getenv("FCPXML_DTD")
	if path == "" {
		t.Skip("set FCPXML_DTD=<path to FCPXMLv1_10.dtd> to check the FCPXML against Apple's DTD")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseDTD(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	// The checker itself: a broken document must fail.
	if errs := d.validate([]byte(`<fcpxml version="1.10"><resources><asset id="a"/></resources><spine/></fcpxml>`)); len(errs) == 0 {
		t.Fatal("the DTD check accepts a broken document")
	}
	docs := map[string][]byte{}
	for name, o := range map[string]FCPXMLOptions{"final cut": {}, "resolve": {StaticStills: true}} {
		data, err := FCPXML(fixture(), o)
		if err != nil {
			t.Fatal(err)
		}
		docs[name] = data
	}
	for _, f := range filepath.SplitList(os.Getenv("FCPXML_FILES")) {
		if f == "" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		docs[f] = data
	}
	for name, data := range docs {
		if errs := d.validate(data); len(errs) > 0 {
			for _, e := range errs {
				t.Errorf("%s: %v", name, e)
			}
		} else {
			t.Logf("%s: valid against %s", name, filepath.Base(path))
		}
	}
}
