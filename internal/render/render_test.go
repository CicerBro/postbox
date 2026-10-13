package render

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestTagsEscapingAndRestore(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	p, e := Prepare(Options{CampaignID: 7, Subject: `Hello [Name,fallback=there] {{ country | "UK" }}`, HTML: `<html><body><!--[if mso]>yes<![endif]--><p>[Name,fallback=there] [Email] [currentdaydate] [currentmonth] [currentyear] {{ country | "UK" }}</p><a href="https://example.com/?a=1&amp;b=2">Go</a><a href='https://example.com/?a=1&amp;b=2'>Again</a><a href="[unsubscribe]">Bye</a></body></html>`, Fields: []string{"country"}, TrackingURL: "https://track.example", Now: now})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Links) != 1 || p.Links[0].URL != "https://example.com/?a=1&b=2" {
		t.Fatalf("links %#v", p.Links)
	}
	r := Recipient{Name: `<b>"A" & B</b>`, Email: "a@example.com", Token: "abc", UnsubscribeURL: "https://track.example/u/abc", Fields: map[string]string{"country": "<UK>"}}
	subject, body, text, e := p.Render(r)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(body, "&lt;b&gt;&#34;A&#34; &amp; B&lt;/b&gt;") || !strings.Contains(body, "10 October 2026") || !strings.Contains(text, r.Name) {
		t.Fatalf("incorrect rendering %s %s", body, text)
	}
	if !strings.Contains(body, "/c/1/abc") || !strings.Contains(body, "/o/abc") {
		t.Fatal(body)
	}
	restored, e := Restore(p.Subject, p.HTML, p.Text, p.Links, []string{"country"})
	if e != nil {
		t.Fatal(e)
	}
	s, h, txt, e := restored.Render(r)
	if e != nil || s != subject || h != body || txt != text {
		t.Fatalf("restore changed personalized output: %v", e)
	}
	fallback, e := Prepare(Options{Subject: `[Name,fallback=there] {{ country | "UK" }}`, Fields: []string{"country"}})
	if e != nil {
		t.Fatal(e)
	}
	s, _, _, e = fallback.Render(Recipient{})
	if e != nil || s != "there UK" {
		t.Fatal(s, e)
	}
}
func TestUnknownFieldsAndInjection(t *testing.T) {
	for _, s := range []string{"[Missing]", `{{ missing }}`, `{{ name | broken }}`, `{{ name`} {
		if _, e := Prepare(Options{Subject: s}); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	p, e := Prepare(Options{Subject: "[Name]"})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = p.Render(Recipient{Name: "Hi\r\nBcc: x@example.com"}); e == nil {
		t.Fatal("accepted injected subject")
	}
}
func TestLinkIDMappingAndCSS(t *testing.T) {
	p, e := Prepare(Options{HTML: `<html><head><style>p{color:red}</style></head><body><p>[Name]</p><a href="https://example.com">Go</a></body></html>`, InlineCSS: true, TrackingURL: "https://track.example", LinkID: func(s string) (int64, error) { return 902, nil }})
	if e != nil {
		t.Fatal(e)
	}
	_, body, _, e := p.Render(Recipient{Name: "A", Token: "tok"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(body, "color:red") || !strings.Contains(body, "/c/902/tok") {
		t.Fatal(body)
	}
	p, e = Prepare(Options{HTML: `<a href="https://example.com">Go</a>`, TrackingURL: "https://track.example", Links: []Link{{902, "https://example.com"}}, LinkID: func(string) (int64, error) { t.Fatal("allocated existing link"); return 0, nil }})
	if e != nil || len(p.Links) != 1 {
		t.Fatal(p, e)
	}
}
func TestMIMERoundTrip(t *testing.T) {
	m := Message{FromName: "Nieuws José", FromEmail: "news@example.com", ToName: "Zoë", ToEmail: "USER@bücher.example", ReplyTo: "Replies <reply@example.com>", Subject: strings.Repeat("Olá 世界 ", 40), HTML: "<p>héllo</p>" + strings.Repeat("a", 2000), Text: "héllo\nworld", UnsubscribeURL: "https://track.example/u/tok", UnsubscribeMailto: "bye@example.com", Date: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	raw, e := BuildMIME(m)
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line too long: %d", len(line))
		}
	}
	parsed, e := mail.ReadMessage(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	subject, e := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if e != nil || subject != m.Subject {
		t.Fatal(subject, e)
	}
	to, e := parsed.Header.AddressList("To")
	if e != nil || to[0].Address != "user@xn--bcher-kva.example" {
		t.Fatal(to, e)
	}
	if parsed.Header.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Fatal("missing one click")
	}
	kind, params, e := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if e != nil || kind != "multipart/alternative" {
		t.Fatal(kind, e)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	for i, want := range []string{m.Text, m.HTML} {
		part, e := reader.NextRawPart()
		if e != nil {
			t.Fatal(e)
		}
		kind, params, e = mime.ParseMediaType(part.Header.Get("Content-Type"))
		if e != nil || params["charset"] != "UTF-8" || kind != []string{"text/plain", "text/html"}[i] {
			t.Fatal(kind, params, e)
		}
		data, e := io.ReadAll(quotedprintable.NewReader(part))
		if e != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != want {
			t.Fatalf("part mismatch %v", e)
		}
	}
	if _, e = reader.NextPart(); e != io.EOF {
		t.Fatal(e)
	}
	m.Subject = "Hello\nBcc: injected@example.com"
	if _, e = BuildMIME(m); e == nil {
		t.Fatal("accepted header injection")
	}
	for _, bad := range []string{"é@example.com", "a@-example.com", "a@example..com", "a@example.com\n"} {
		if _, e = NormalizeEmail(bad); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func BenchmarkRenderMIME100KB(b *testing.B) {
	p, e := Prepare(Options{Subject: "News for [Name]", HTML: "<html><body><p>[Name]</p>" + strings.Repeat("<p>This is a realistic newsletter paragraph with useful links and information.</p>", 1280) + "</body></html>"})
	if e != nil {
		b.Fatal(e)
	}
	r := Recipient{Name: "Zoë", Email: "zoe@example.com"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subject, body, text, e := p.Render(r)
		if e != nil {
			b.Fatal(e)
		}
		_, e = BuildMIME(Message{FromEmail: "news@example.com", ToEmail: r.Email, ToName: r.Name, Subject: subject, HTML: body, Text: text, UnsubscribeURL: "https://track.example/u/token"})
		if e != nil {
			b.Fatal(e)
		}
	}
}

func TestSentAndWebBodyIdentity(t *testing.T) {
	p, err := Prepare(Options{HTML: "<html>\n<body><p>[Name]</p>\n<a href='https://example.com'>Go</a>\n</body></html>", TrackingURL: "https://track.example"})
	if err != nil {
		t.Fatal(err)
	}
	_, body, text, err := p.Render(Recipient{Name: "line one\nline two", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := BuildMIME(Message{FromEmail: "news@example.com", ToEmail: "a@example.com", HTML: body, Text: text, UnsubscribeURL: "https://track.example/u/tok"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	for _, want := range []string{text, body} {
		part, err := r.NextRawPart()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil || string(got) != want {
			t.Fatalf("sent/web body mismatch: %q != %q (%v)", got, want, err)
		}
	}
}

func TestQuotedPrintableBytes(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		source.WriteByte(byte(i))
	}
	source.WriteString(" \t\n" + strings.Repeat("abc ", 100) + " \t")
	var encoded bytes.Buffer
	writeQuotedPrintable(&encoded, source.String())
	got, err := io.ReadAll(quotedprintable.NewReader(&encoded))
	if err != nil || string(got) != canonicalLines(source.String()) {
		t.Fatalf("QP round trip mismatch %v", err)
	}
}

func TestFallbackBracesRoundTrip(t *testing.T) {
	for _, body := range []string{`<p>[Name,fallback={friend}]</p>`, `<p>{{ name | "a }} {friend} \\\" quote" }}</p>`, `<div title='[Name,fallback={friend}]'>Hello</div>`} {
		p, err := Prepare(Options{HTML: body})
		if err != nil {
			t.Fatal(err)
		}
		restored, err := Restore(p.Subject, p.HTML, p.Text, p.Links, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, want, _, err := p.Render(Recipient{})
		if err != nil {
			t.Fatal(err)
		}
		_, got, _, err := restored.Render(Recipient{})
		if err != nil || got != want {
			t.Fatalf("%q != %q: %v", got, want, err)
		}
	}
}

func TestHTMLMergeContexts(t *testing.T) {
	p, err := Prepare(Options{HTML: `<div title=[Name]>Hi [Name]</div><a href="[URL]">Link</a>`, Fields: []string{"URL"}})
	if err != nil {
		t.Fatal(err)
	}
	_, body, _, err := p.Render(Recipient{Name: "x onmouseover=alert(1)", Fields: map[string]string{"URL": "https://example.com/a"}})
	if err != nil || !strings.Contains(body, "title=x&#32;onmouseover&#61;alert(1)") || !strings.Contains(body, "Hi x onmouseover=alert(1)") {
		t.Fatal(body, err)
	}
	for _, bad := range []string{"javascript:alert(1)", "JaVaScRiPt:alert(1)", "data:text/html,evil", "//evil.example", "java\tscript:alert(1)"} {
		if _, _, _, err := p.Render(Recipient{Fields: map[string]string{"URL": bad}}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, bad := range []string{`<div onclick="[Name]">`, `<div style="[Name]">`, `<script>[Name]</script>`, `<[Name]>Hi</[Name]>`, `<div [Name]="x">`, `<!-- [Name] -->`} {
		if _, err := Prepare(Options{HTML: bad}); err == nil {
			t.Fatalf("accepted unsupported context %s", bad)
		}
	}
}

func TestPersonalizedTrackedLink(t *testing.T) {
	p, err := Prepare(Options{HTML: `<a href="https://example.com/?email=[Email]&amp;name=[Name]&amp;year=[currentyear]">Link</a>`, TrackingURL: "https://track.example", Now: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)})
	if err != nil || len(p.Links) != 1 {
		t.Fatal(p, err)
	}
	got, err := ResolveLink(p.Links[0].URL, Recipient{Email: "a+b@example.com", Name: "A & B"}, nil)
	if err != nil || got != "https://example.com/?email=a%2Bb%40example.com&name=A+%26+B&year=2026" {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,bad", "https://[Name]/path"} {
		if _, err := ResolveLink(bad, Recipient{Name: "evil.example"}, nil); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestTransactionalMIME(t *testing.T) {
	m := Message{FromEmail: "from@example.com", ToEmail: "to@example.com", Subject: "Confirm", HTML: "<p>Confirm</p>", Text: "Confirm"}
	raw, err := BuildTransactionalMIME(m)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("List-Unsubscribe") != "" || parsed.Header.Get("List-Unsubscribe-Post") != "" {
		t.Fatal("transactional message contains list headers")
	}
	if _, err := BuildMIME(m); err == nil {
		t.Fatal("newsletter accepted missing unsubscribe URL")
	}
	m.Subject = "Confirm\r\nBcc: injected@example.com"
	if _, err := BuildTransactionalMIME(m); err == nil {
		t.Fatal("transactional header injection")
	}
}
