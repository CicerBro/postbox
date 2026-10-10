package render

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

type Message struct {
	FromName, FromEmail, ReplyTo, ToName, ToEmail, Subject, HTML, Text, UnsubscribeURL, UnsubscribeMailto string
	Date                                                                                                  time.Time
}

var mimeBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func NormalizeEmail(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("email contains header control characters")
	}
	value = strings.TrimSpace(value)
	a, e := mail.ParseAddress(value)
	if e != nil || a.Name != "" || a.Address != value {
		return "", fmt.Errorf("invalid email address %q", value)
	}
	i := strings.LastIndexByte(value, '@')
	if i <= 0 {
		return "", fmt.Errorf("invalid email address")
	}
	local, domain := value[:i], value[i+1:]
	if len(local) > 64 {
		return "", fmt.Errorf("email local part is too long")
	}
	for _, c := range local {
		if c > 127 || c < 32 {
			return "", fmt.Errorf("SMTPUTF8 local parts are unsupported")
		}
	}
	domain, e = idna.Lookup.ToASCII(domain)
	if e != nil || domain == "" || len(domain) > 253 {
		return "", fmt.Errorf("invalid email domain")
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid email domain")
		}
	}
	return strings.ToLower(local + "@" + domain), nil
}
func encodedHeader(value string) string {
	if len(value) <= 60 {
		return mime.QEncoding.Encode("UTF-8", value)
	}
	var words []string
	for len(value) > 0 {
		n := 45
		if len(value) < n {
			n = len(value)
		}
		for n > 0 && !utf8.ValidString(value[:n]) {
			n--
		}
		if n == 0 {
			n = 1
		}
		words = append(words, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString([]byte(value[:n]))+"?=")
		value = value[n:]
	}
	return strings.Join(words, "\r\n ")
}
func address(name, email string) (string, error) {
	normalized, e := NormalizeEmail(email)
	if e != nil {
		return "", e
	}
	if name == "" {
		return normalized, nil
	}
	if len(name) > 60 {
		return encodedHeader(name) + " <" + normalized + ">", nil
	}
	return (&mail.Address{Name: name, Address: normalized}).String(), nil
}
func BuildMIME(m Message) ([]byte, error) { return buildMIME(m, true) }

// BuildTransactionalMIME builds direct service notifications without mailing list headers.
func BuildTransactionalMIME(m Message) ([]byte, error) { return buildMIME(m, false) }

func buildMIME(m Message, newsletter bool) ([]byte, error) {
	if !utf8.ValidString(m.HTML) || !utf8.ValidString(m.Text) {
		return nil, fmt.Errorf("message bodies must be valid UTF-8")
	}
	for _, h := range []string{m.FromName, m.FromEmail, m.ReplyTo, m.ToName, m.ToEmail, m.Subject, m.UnsubscribeURL, m.UnsubscribeMailto} {
		if strings.ContainsAny(h, "\r\n\x00") {
			return nil, fmt.Errorf("header contains control characters")
		}
		if !utf8.ValidString(h) {
			return nil, fmt.Errorf("header contains invalid UTF-8")
		}
	}
	from, e := address(m.FromName, m.FromEmail)
	if e != nil {
		return nil, e
	}
	to, e := address(m.ToName, m.ToEmail)
	if e != nil {
		return nil, e
	}
	reply := ""
	if m.ReplyTo != "" {
		a, e := mail.ParseAddress(m.ReplyTo)
		if e != nil {
			return nil, e
		}
		reply, e = address(a.Name, a.Address)
		if e != nil {
			return nil, e
		}
	}
	unsub := ""
	if newsletter {
		u, e := url.Parse(m.UnsubscribeURL)
		if e != nil || u.Scheme != "https" || u.Host == "" || strings.ContainsAny(m.UnsubscribeURL, "<>") {
			return nil, fmt.Errorf("unsubscribe URL must be an absolute HTTPS URL")
		}
		unsub = "<" + m.UnsubscribeURL + ">"
		if m.UnsubscribeMailto != "" {
			v := strings.TrimPrefix(m.UnsubscribeMailto, "mailto:")
			a, e := NormalizeEmail(v)
			if e != nil {
				return nil, e
			}
			unsub += ",\r\n <mailto:" + a + ">"
		}
	}
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	var entropy [18]byte
	if _, e = rand.Read(entropy[:]); e != nil {
		return nil, e
	}
	boundary := "postbox_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	b := mimeBuffers.Get().(*bytes.Buffer)
	b.Reset()
	defer func() {
		if b.Cap() <= 2<<20 {
			mimeBuffers.Put(b)
		}
	}()
	b.Grow(len(m.HTML) + len(m.Text) + 1024)
	headers := [][2]string{{"From", from}, {"To", to}, {"Subject", encodedHeader(m.Subject)}, {"Date", m.Date.Format(time.RFC1123Z)}, {"MIME-Version", "1.0"}, {"Content-Type", `multipart/alternative; boundary="` + boundary + `"`}}
	if newsletter {
		headers = append(headers, [2]string{"List-Unsubscribe", unsub}, [2]string{"List-Unsubscribe-Post", "List-Unsubscribe=One-Click"})
	}
	if reply != "" {
		headers = append(headers, [2]string{"Reply-To", reply})
	}
	for _, h := range headers {
		for _, line := range strings.Split(h[1], "\r\n") {
			if len(line)+len(h[0])+2 > 998 {
				return nil, fmt.Errorf("%s header exceeds line limit", h[0])
			}
		}
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	for _, part := range [][2]string{{"plain", m.Text}, {"html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\nContent-Type: text/" + part[0] + "; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		writeQuotedPrintable(b, part[1])
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return append([]byte(nil), b.Bytes()...), nil
}

// writeQuotedPrintable writes safe ASCII runs together and bounds encoded lines to 76 bytes.
func writeQuotedPrintable(b *bytes.Buffer, source string) {
	const hex = "0123456789ABCDEF"
	col := 0
	for i := 0; i < len(source); {
		ch := source[i]
		if ch == '\r' || ch == '\n' {
			if ch == '\r' && i+1 < len(source) && source[i+1] == '\n' {
				i++
			}
			b.WriteString("\r\n")
			col = 0
			i++
			continue
		}
		safe := func(j int) bool {
			c := source[j]
			return (c >= 33 && c <= 126 && c != '=') || ((c == ' ' || c == '\t') && j+1 < len(source) && source[j+1] != '\r' && source[j+1] != '\n')
		}
		if safe(i) {
			if col == 75 {
				b.WriteString("=\r\n")
				col = 0
			}
			end := i
			for end < len(source) && end-i < 75-col && safe(end) {
				end++
			}
			b.WriteString(source[i:end])
			col += end - i
			i = end
		} else {
			if col+3 > 75 {
				b.WriteString("=\r\n")
				col = 0
			}
			b.WriteByte('=')
			b.WriteByte(hex[ch>>4])
			b.WriteByte(hex[ch&15])
			col += 3
			i++
		}
	}
}
