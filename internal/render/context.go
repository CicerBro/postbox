package render

import (
	"fmt"
	"html"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

var attributeEscaper = strings.NewReplacer(" ", "&#32;", "\t", "&#9;", "\r", "&#13;", "\n", "&#10;", "\f", "&#12;", "`", "&#96;", "=", "&#61;")

func escapeAttributeWhitespace(s string) string {
	return attributeEscaper.Replace(s)
}

// Compile contexts once. Mask merge tags so quoted fallbacks cannot confuse the HTML tokenizer.
func compileHTML(source string, fields []string, now time.Time) ([]chunk, [][]chunk, error) {
	chunks, err := compile(source, fields, now)
	if err != nil {
		return nil, nil, err
	}
	prefix := "POSTBOXCONTEXT"
	for strings.Contains(source, prefix) {
		prefix += "X"
	}
	var originals []string
	var slotIndices []int
	slot := 0
	masked := tags.ReplaceAllStringFunc(source, func(tag string) string {
		c, _ := compile(tag, fields, now)
		if len(c) == 1 && !c[0].slot {
			return tag
		}
		originals = append(originals, tag)
		index := -1
		if len(c) == 1 && c[0].slot {
			index = slot
			slot++
		}
		slotIndices = append(slotIndices, index)
		return prefix + strconv.Itoa(len(originals)-1) + "END"
	})
	markers := make([]string, len(originals))
	for i := range markers {
		markers[i] = prefix + strconv.Itoa(i) + "END"
	}
	attrs := make(map[int]bool)
	var checks [][]chunk
	z := xhtml.NewTokenizer(strings.NewReader(masked))
	rawText := ""
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			if z.Err() != io.EOF {
				return nil, nil, z.Err()
			}
			break
		}
		raw := string(z.Raw())
		if tt == xhtml.StartTagToken || tt == xhtml.SelfClosingTagToken {
			t := z.Token()
			if strings.Contains(t.Data, strings.ToLower(prefix)) {
				return nil, nil, fmt.Errorf("merge tags in HTML tag names are unsupported")
			}
			for _, a := range t.Attr {
				if strings.Contains(a.Key, strings.ToLower(prefix)) {
					return nil, nil, fmt.Errorf("merge tags in HTML attribute names are unsupported")
				}
				if !strings.Contains(a.Val, prefix) {
					continue
				}
				if strings.HasPrefix(a.Key, "on") || a.Key == "style" || a.Key == "srcdoc" || a.Key == "srcset" || (t.Data == "meta" && (a.Key == "content" || a.Key == "http-equiv")) {
					return nil, nil, fmt.Errorf("merge tags in %s attributes are unsupported", a.Key)
				}
				restored := a.Val
				for i, m := range markers {
					if strings.Contains(a.Val, m) {
						if slotIndices[i] >= 0 {
							attrs[slotIndices[i]] = true
						}
						restored = strings.ReplaceAll(restored, m, originals[i])
					}
				}
				switch a.Key {
				case "href", "src", "action", "formaction", "poster", "background", "cite", "data", "xlink:href":
					c, e := compile(restored, fields, now)
					if e != nil {
						return nil, nil, e
					}
					checks = append(checks, c)
				}
			}
			if t.Data == "script" || t.Data == "style" {
				rawText = t.Data
			}
		} else if tt == xhtml.EndTagToken {
			if z.Token().Data == rawText {
				rawText = ""
			}
		} else if strings.Contains(raw, prefix) && (rawText != "" || tt != xhtml.TextToken) {
			return nil, nil, fmt.Errorf("merge tags in %s HTML context are unsupported", rawText)
		}
	}
	slot = 0
	for i := range chunks {
		if chunks[i].slot {
			chunks[i].attribute = attrs[slot]
			slot++
		}
	}
	return chunks, checks, nil
}

func safeURL(value string, strict bool) bool {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\x00\r\n\t") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return !strict
	case "":
		return !strict && u.Host == "" && !strings.HasPrefix(value, "//")
	default:
		return false
	}
}

// ResolveLink fills a persisted URL template from the frozen recipient snapshot.
// Values are escaped as query components or path segments and the result is validated.
func ResolveLink(destination string, recipient Recipient, fields []string) (string, error) {
	chunks, err := compile(html.UnescapeString(destination), fields, time.Time{})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range chunks {
		if !c.slot {
			b.WriteString(c.fixed)
			continue
		}
		prefix := b.String()
		// Personalized origins can change the redirect target and are unsupported.
		scheme := strings.Index(prefix, "://")
		if scheme < 0 || !strings.ContainsAny(prefix[scheme+3:], "/?#") {
			return "", fmt.Errorf("personalized URL origins are unsupported")
		}
		v := value(c, recipient)
		if strings.ContainsAny(v, "\x00\r\n\t") {
			return "", fmt.Errorf("URL merge value contains control characters")
		}
		if strings.Contains(prefix, "?") && !strings.Contains(prefix, "#") {
			v = url.QueryEscape(v)
		} else {
			v = url.PathEscape(v)
		}
		b.WriteString(v)
	}
	result := b.String()
	if !safeURL(result, true) {
		return "", fmt.Errorf("invalid personalized link URL")
	}
	return result, nil
}
