// Package render prepares campaign templates once and fills immutable chunks per recipient.
package render

import (
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vanng822/go-premailer/premailer"
	xhtml "golang.org/x/net/html"
)

type Recipient struct {
	ID          int64
	Email, Name string
	// NamePlaceholder supplies a sample name only when no name or tag fallback is available.
	NamePlaceholder                      string
	Fields                               map[string]string
	Token, UnsubscribeURL, WebVersionURL string
}
type Options struct {
	CampaignID                       int64
	Subject, HTML, Text, TrackingURL string
	InlineCSS                        bool
	Fields                           []string
	Now                              time.Time
	// LinkID allocates durable global IDs. Links can restore an existing campaign mapping.
	LinkID func(string) (int64, error)
	Links  []Link
}
type Link struct {
	ID  int64
	URL string
}
type Prepared struct {
	Subject, HTML, Text  string
	Links                []Link
	subject, body, plain []chunk
	urlChecks            [][]chunk
}
type chunk struct {
	fixed, key, fallback string
	slot                 bool
	attribute            bool
}

var tags = regexp.MustCompile(`\{\{(?:[^{}"]|"(?:\\.|[^"\\])*")*\}\}|\[[A-Za-z_][A-Za-z0-9_]*(?:,fallback=[^\]]*)?\]`)
var hrefs = regexp.MustCompile(`(?is)(<a\b[^>]*?\bhref\s*=\s*)("[^"]*"|'[^']*')`)
var builtin = map[string]bool{"name": true, "email": true, "token": true, "unsubscribe": true, "webversion": true}
var dates = map[string]string{"currentdaydate": "02", "currentmonth": "January", "currentmonthnumber": "01", "currentyear": "2006", "currentday": "Monday", "currentdate": "2006-01-02", "currentdayofmonth": "2", "currentmonthname": "January", "currentdayname": "Monday"}

func Prepare(o Options) (*Prepared, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	// Validate before HTML processing can transform or discard invalid tags.
	for _, s := range []string{o.Subject, o.HTML, o.Text} {
		if _, e := compile(s, o.Fields, o.Now); e != nil {
			return nil, e
		}
	}
	if _, _, e := compileHTML(o.HTML, o.Fields, o.Now); e != nil {
		return nil, e
	}
	source := o.HTML
	if o.InlineCSS {
		var saved []string
		prefix := "POSTBOXMERGETAG"
		for strings.Contains(source, prefix) {
			prefix += "_"
		}
		source = tags.ReplaceAllStringFunc(source, func(tag string) string {
			saved = append(saved, tag)
			return fmt.Sprintf("%s%dPLACEHOLDER", prefix, len(saved)-1)
		})
		p, e := premailer.NewPremailerFromString(source, premailer.NewOptions())
		if e != nil {
			return nil, e
		}
		source, e = p.Transform()
		if e != nil {
			return nil, e
		}
		for i, tag := range saved {
			source = strings.ReplaceAll(source, fmt.Sprintf("%s%dPLACEHOLDER", prefix, i), tag)
		}
	}
	links := append([]Link(nil), o.Links...)
	mapping := map[string]int64{}
	var next int64
	for _, l := range links {
		mapping[l.URL] = l.ID
		if l.ID > next {
			next = l.ID
		}
	}
	var rewriteErr error
	base := strings.TrimRight(o.TrackingURL, "/")
	if base != "" {
		u, e := url.Parse(base)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("invalid tracking URL")
		}
		source = hrefs.ReplaceAllStringFunc(source, func(match string) string {
			parts := hrefs.FindStringSubmatch(match)
			q := parts[2][0]
			dest := html.UnescapeString(parts[2][1 : len(parts[2])-1])
			compiled, e := compile(dest, o.Fields, o.Now)
			if e != nil {
				rewriteErr = e
				return match
			}
			dest = serialize(compiled)
			u, e := url.Parse(tags.ReplaceAllString(dest, "postboxslot"))
			if e != nil || (u.Scheme != "https" && u.Scheme != "http") {
				return match
			}
			id, exists := mapping[dest]
			if !exists {
				next++
				id = next
				if o.LinkID != nil {
					id, e = o.LinkID(dest)
					if e != nil {
						rewriteErr = e
						return match
					}
				}
				mapping[dest] = id
				links = append(links, Link{id, dest})
			}
			tracked := base + "/c/" + strconv.FormatInt(id, 10) + "/[token]"
			return parts[1] + string(q) + html.EscapeString(tracked) + string(q)
		})
		if rewriteErr != nil {
			return nil, rewriteErr
		}
		pixel := `<img src="` + html.EscapeString(base) + `/o/[token]" width="1" height="1" alt="" style="display:none" />`
		if i := strings.LastIndex(strings.ToLower(source), "</body>"); i >= 0 {
			source = source[:i] + pixel + source[i:]
		} else {
			source += pixel
		}
	}
	text := o.Text
	if text == "" {
		text = plainText(source)
	}
	p, e := prepareCompiled(o.Subject, source, text, links, o.Fields, o.Now)
	return p, e
}

// Restore compiles persisted prepared sources without assigning IDs or rewriting links again.
func Restore(subject, body, text string, links []Link, fields []string) (*Prepared, error) {
	return prepareCompiled(subject, body, text, links, fields, time.Time{})
}
func prepareCompiled(subject, body, text string, links []Link, fields []string, now time.Time) (*Prepared, error) {
	p := &Prepared{Links: append([]Link(nil), links...)}
	var e error
	if p.subject, e = compile(canonicalLines(subject), fields, now); e != nil {
		return nil, e
	}
	if p.body, p.urlChecks, e = compileHTML(canonicalLines(body), fields, now); e != nil {
		return nil, e
	}
	if p.plain, e = compile(canonicalLines(text), fields, now); e != nil {
		return nil, e
	}
	// Persist already-resolved date constants, while retaining recipient placeholders.
	p.Subject = serialize(p.subject)
	p.HTML = serialize(p.body)
	p.Text = serialize(p.plain)
	return p, nil
}
func serialize(chunks []chunk) string {
	var b strings.Builder
	for _, c := range chunks {
		if c.slot {
			b.WriteString("{{ " + c.key + " | " + strconv.Quote(c.fallback) + " }}")
		} else {
			b.WriteString(c.fixed)
		}
	}
	return b.String()
}
func compile(source string, fields []string, now time.Time) ([]chunk, error) {
	allowed := map[string]bool{}
	for _, f := range fields {
		allowed[f] = true
	}
	out := make([]chunk, 0, 16)
	last := 0
	for _, loc := range tags.FindAllStringIndex(source, -1) {
		if loc[0] > last {
			out = append(out, chunk{fixed: source[last:loc[0]]})
		}
		tag := source[loc[0]:loc[1]]
		var key, fallback string
		if strings.HasPrefix(tag, "{{") {
			v := strings.TrimSpace(tag[2 : len(tag)-2])
			parts := strings.SplitN(v, "|", 2)
			key = strings.TrimSpace(parts[0])
			if len(parts) == 2 {
				var e error
				fallback, e = strconv.Unquote(strings.TrimSpace(parts[1]))
				if e != nil {
					return nil, fmt.Errorf("invalid fallback in %s", tag)
				}
			}
		} else {
			parts := strings.SplitN(tag[1:len(tag)-1], ",fallback=", 2)
			key = parts[0]
			if len(parts) == 2 {
				fallback = parts[1]
			}
		}
		lower := strings.ToLower(key)
		if lower == "endif" {
			out = append(out, chunk{fixed: tag})
			last = loc[1]
			continue
		}
		if lower == "unsubscribe_url" {
			lower = "unsubscribe"
		}
		if lower == "webversion_url" {
			lower = "webversion"
		}
		if layout, ok := dates[lower]; ok {
			if now.IsZero() {
				return nil, fmt.Errorf("unresolved date tag %s in prepared source", tag)
			}
			out = append(out, chunk{fixed: now.Format(layout)})
		} else {
			if builtin[lower] {
				key = lower
			} else if !allowed[key] {
				return nil, fmt.Errorf("unknown merge field %q", key)
			}
			out = append(out, chunk{key: key, fallback: fallback, slot: true})
		}
		last = loc[1]
	}
	if strings.Contains(source[last:], "{{") {
		return nil, fmt.Errorf("unterminated merge tag")
	}
	if last < len(source) {
		out = append(out, chunk{fixed: source[last:]})
	}
	return out, nil
}
func value(c chunk, r Recipient) string {
	var v string
	switch c.key {
	case "name":
		v = r.Name
		if v == "" && c.fallback == "" {
			v = r.NamePlaceholder
		}
	case "email":
		v = r.Email
	case "token":
		v = r.Token
	case "unsubscribe":
		v = r.UnsubscribeURL
	case "webversion":
		v = r.WebVersionURL
	default:
		v = r.Fields[c.key]
	}
	if v == "" {
		return c.fallback
	}
	return v
}
func fill(chunks []chunk, r Recipient, escape bool) string {
	var b strings.Builder
	n := 0
	for _, c := range chunks {
		n += len(c.fixed)
	}
	b.Grow(n + 256)
	for _, c := range chunks {
		if !c.slot {
			b.WriteString(c.fixed)
		} else {
			v := canonicalLines(value(c, r))
			if escape {
				v = html.EscapeString(v)
				if c.attribute {
					v = escapeAttributeWhitespace(v)
				}
			}
			b.WriteString(v)
		}
	}
	return b.String()
}
func (p *Prepared) Render(r Recipient) (subject, body, text string, err error) {
	subject = fill(p.subject, r, false)
	if strings.ContainsAny(subject, "\r\n\x00") {
		return "", "", "", fmt.Errorf("subject contains header control characters")
	}
	for _, check := range p.urlChecks {
		if !safeURL(fill(check, r, false), false) {
			return "", "", "", fmt.Errorf("unsafe personalized URL")
		}
	}
	return subject, fill(p.body, r, true), fill(p.plain, r, false), nil
}
func plainText(source string) string {
	z := xhtml.NewTokenizer(strings.NewReader(source))
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			if z.Err() != io.EOF {
				return ""
			}
			break
		}
		switch tt {
		case xhtml.StartTagToken, xhtml.EndTagToken, xhtml.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "style" || t.Data == "script" || t.Data == "head" {
				if tt == xhtml.StartTagToken {
					skip++
				} else if tt == xhtml.EndTagToken && skip > 0 {
					skip--
				}
			}
			if skip == 0 && (t.Data == "br" || t.Data == "p" || t.Data == "div" || t.Data == "tr" || t.Data == "li") {
				b.WriteByte('\n')
			}
			if skip == 0 && tt == xhtml.StartTagToken && t.Data == "a" {
				for _, a := range t.Attr {
					if a.Key == "href" {
						b.WriteString(" (" + a.Val + ") ")
					}
				}
			}
		case xhtml.TextToken:
			if skip == 0 {
				b.WriteString(string(z.Text()))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// canonicalLines makes web output and MIME-decoded bodies byte-identical.
func canonicalLines(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n", "\r\n")
}
