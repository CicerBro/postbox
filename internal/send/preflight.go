package send

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"postbox/internal/render"
)

// PreflightOptions allows an isolated HTTP transport in tests. Production callers
// should leave Client nil to use the public-address-only, DNS-pinned transport.
type PreflightOptions struct{ Client *http.Client }
type PreflightIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Tag     string `json:"tag,omitempty"`
}
type PreflightLink struct {
	URL          string `json:"url"`
	Status       string `json:"status"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Message      string `json:"message,omitempty"`
	Personalized bool   `json:"personalized"`
}
type PreflightChecks struct {
	Ready                   bool             `json:"ready"`
	Errors                  []PreflightIssue `json:"errors"`
	Warnings                []PreflightIssue `json:"warnings"`
	Links                   []PreflightLink  `json:"links"`
	HTMLBytes               int64            `json:"html_bytes"`
	HTMLClipping            bool             `json:"html_clipping"`
	Suppressed              int64            `json:"suppressed"`
	Inactive                int64            `json:"inactive"`
	Skipped                 int64            `json:"skipped"`
	RemainingDailyQuota     float64          `json:"remaining_daily_quota"`
	UserRemainingDailyQuota *int64           `json:"user_remaining_daily_quota,omitempty"`
	SampleRecipient         bool             `json:"sample_recipient"`
}
type Checklist struct {
	Estimate
	Checks PreflightChecks `json:"checks"`
}

var preflightTags = regexp.MustCompile(`\{\{(?:[^{}"]|"(?:\\.|[^"\\])*")*\}\}|\[[A-Za-z_][A-Za-z0-9_]*(?:,fallback=[^\]]*)?\]`)

// Checklist does not persist preparations, freeze recipients, or reserve quota.
func (e *Engine) Checklist(ctx context.Context, id int64, opts PreflightOptions) (Checklist, error) {
	out := Checklist{Checks: PreflightChecks{Errors: []PreflightIssue{}, Warnings: []PreflightIssue{}, Links: []PreflightLink{}, SampleRecipient: true}}
	addError := func(code, message string) {
		out.Checks.Errors = append(out.Checks.Errors, PreflightIssue{Code: code, Message: message})
	}
	c, err := e.prepare(ctx, id, false)
	if err != nil {
		if err == sql.ErrNoRows {
			return out, err
		}
		// Return template failures in the structured checklist so the UI can explain
		// why sending is unavailable. Each unknown tag is independently validated by
		// the same strict compiler used to send the campaign.
		var source string
		if dbErr := e.db.Reader.QueryRowContext(ctx, `SELECT subject||' '||html||' '||coalesce(text,'') FROM campaigns WHERE id=?`, id).Scan(&source); dbErr != nil {
			return out, dbErr
		}
		seen := map[string]bool{}
		for _, tag := range preflightTags.FindAllString(source, -1) {
			if seen[tag] {
				continue
			}
			seen[tag] = true
			if _, tagErr := render.Prepare(render.Options{Subject: tag, HTML: "<p>test</p>", Fields: c.fields}); tagErr != nil {
				out.Checks.Errors = append(out.Checks.Errors, PreflightIssue{Code: "merge_tag", Message: tagErr.Error(), Tag: tag})
			}
		}
		if len(out.Checks.Errors) == 0 {
			addError("template", err.Error())
		}
		return out, nil
	}
	audience, args, err := e.audienceSQL(ctx, c)
	if err != nil {
		addError("audience", err.Error())
		return out, nil
	}
	if err = e.db.Reader.QueryRowContext(ctx, `SELECT count(*) `+audience, args...).Scan(&out.Recipients); err != nil {
		return out, err
	}
	// Remove only the engine's eligibility filters, retaining the exact include
	// and exclude predicates, to count people skipped in the selected audience.
	selected := strings.Split(audience, ` AND NOT EXISTS(SELECT 1 FROM suppressions`)[0]
	selected = strings.Replace(selected, ` AND s.status='active'`, "", 1)
	activeSelected := selected + ` AND s.status='active'`
	var active int64
	if err = e.db.Reader.QueryRowContext(ctx, `SELECT count(*) `+activeSelected, args...).Scan(&active); err != nil {
		return out, err
	}
	if err = e.db.Reader.QueryRowContext(ctx, `SELECT count(*) `+selected+` AND s.status<>'active'`, args...).Scan(&out.Checks.Inactive); err != nil {
		return out, err
	}
	out.Checks.Suppressed = active - out.Recipients
	out.Checks.Skipped = out.Checks.Suppressed + out.Checks.Inactive
	// Missing recipient values are warnings because the renderer permits empty
	// values and supports fallbacks. Count only eligible selected recipients.
	referenced := map[string]bool{}
	for _, tag := range preflightTags.FindAllString(c.subject+" "+c.html+" "+c.text, -1) {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tag, "{{"), "}}"))
		if strings.HasPrefix(tag, "[") {
			key = strings.TrimSuffix(strings.TrimPrefix(tag, "["), "]")
		}
		if strings.Contains(key, "|") || strings.Contains(key, ",fallback=") {
			continue
		}
		referenced[key] = true
	}
	for _, field := range c.fields {
		if !referenced[field] {
			continue
		}
		var missing int64
		fieldArgs := append(append([]any{}, args...), field)
		if err = e.db.Reader.QueryRowContext(ctx, `SELECT count(*) `+audience+` AND NOT EXISTS(SELECT 1 FROM json_each(s.fields) f WHERE f.key=? AND f.value IS NOT NULL AND trim(CAST(f.value AS TEXT))<>'')`, fieldArgs...).Scan(&missing); err != nil {
			return out, err
		}
		if missing > 0 {
			out.Checks.Warnings = append(out.Checks.Warnings, PreflightIssue{Code: "missing_merge_value", Tag: field, Message: fmt.Sprintf("%d eligible recipients have no value for %s and this tag has no fallback", missing, field)})
		}
	}
	if out.Recipients == 0 {
		addError("empty_audience", "No eligible recipients in the selected audience")
	}
	q, err := e.sender.GetAccount(ctx)
	if err != nil {
		addError("account_quota", err.Error())
	} else {
		out.Quota = q
		out.Checks.RemainingDailyQuota = math.Max(0, q.Max24HourSend-q.SentLast24Hours)
		out.FitsDailyQuota = float64(out.Recipients) <= out.Checks.RemainingDailyQuota
		if q.MaxSendRate <= 0 || math.IsNaN(q.MaxSendRate) || math.IsInf(q.MaxSendRate, 0) {
			addError("account_quota", "Invalid SES sending rate")
		} else {
			out.DurationSeconds = float64(out.Recipients) / q.MaxSendRate
		}
		if !out.FitsDailyQuota {
			addError("daily_quota", "Campaign exceeds the remaining SES daily quota")
		}
	}
	if err = e.checkUserBudget(ctx, id, c.brand, out.Recipients, &out.Checks); err != nil {
		return out, err
	}
	sample := render.Recipient{ID: 1, Email: "recipient@example.org", Name: "Recipient", Fields: map[string]string{}, Token: "preflight", UnsubscribeURL: c.trackingURL + "/u/preflight", WebVersionURL: c.trackingURL + "/w/preflight"}
	for _, key := range c.fields {
		sample.Fields[key] = "sample"
	}
	if len(c.fields) > 0 {
		out.Checks.Warnings = append(out.Checks.Warnings, PreflightIssue{Code: "sample_merge_values", Message: "Message size uses sample custom field values; preview a real recipient to verify missing values and personalized size"})
	}
	_, body, _, renderErr := c.prepared.Render(sample)
	if renderErr != nil {
		addError("render", renderErr.Error())
	} else {
		out.Checks.HTMLBytes = int64(len(body))
		out.Checks.HTMLClipping = len(body) >= 102*1024
		if out.Checks.HTMLClipping {
			out.Checks.Warnings = append(out.Checks.Warnings, PreflightIssue{Code: "html_clipping", Message: "Sample rendered HTML reaches Gmail's 102 KB clipping threshold"})
		}
	}
	sampleFields, _ := json.Marshal(sample.Fields)
	raw, renderErr := e.render(c, recipient{ID: 1, Email: sample.Email, Name: sample.Name, Fields: string(sampleFields)})
	if renderErr != nil {
		addError("message", renderErr.Error())
	} else {
		out.MessageBytes = int64(len(raw))
		out.UploadBytes = out.MessageBytes * out.Recipients
	}
	links := c.prepared.Links
	if len(links) > 100 {
		links = links[:100]
		out.Checks.Warnings = append(out.Checks.Warnings, PreflightIssue{Code: "link_limit", Message: "Only the first 100 links were checked"})
	}
	out.Checks.Links = checkPreflightLinks(ctx, links, c.fields, sample, opts)
	for _, l := range out.Checks.Links {
		if l.Status == "broken" || l.Status == "blocked" {
			addError("link", l.URL+": "+l.Message)
		}
		if l.Status == "warning" {
			out.Checks.Warnings = append(out.Checks.Warnings, PreflightIssue{Code: "link", Message: l.URL + ": " + l.Message})
		}
		if l.Personalized {
			out.Checks.Warnings = append(out.Checks.Warnings, PreflightIssue{Code: "personalized_link", Message: l.URL + ": checked with sample values; verify with a real recipient"})
		}
	}
	out.Checks.Ready = len(out.Checks.Errors) == 0
	return out, nil
}

func (e *Engine) checkUserBudget(ctx context.Context, id, brand, n int64, checks *PreflightChecks) error {
	var owner sql.NullInt64
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT created_by FROM campaigns WHERE id=?`, id).Scan(&owner); err != nil {
		return err
	}
	if !owner.Valid {
		return nil
	}
	var role string
	if err := e.db.Reader.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, owner.Int64).Scan(&role); err != nil {
		return err
	}
	if role == "admin" {
		return nil
	}
	var permissions string
	var limit sql.NullInt64
	var used, previous int64
	err := e.db.Reader.QueryRowContext(ctx, `SELECT permissions,daily_send_limit,coalesce((SELECT reserved FROM user_brand_daily WHERE user_id=? AND brand_id=? AND day=?),0),coalesce((SELECT recipients FROM campaign_user_reservations WHERE campaign_id=?),0) FROM user_brands WHERE user_id=? AND brand_id=?`, owner.Int64, brand, time.Now().UTC().Truncate(24*time.Hour).UnixMilli(), id, owner.Int64, brand).Scan(&permissions, &limit, &used, &previous)
	if err == sql.ErrNoRows {
		checks.Errors = append(checks.Errors, PreflightIssue{Code: "user_permission", Message: "Campaign owner no longer has brand access"})
		return nil
	}
	if err != nil {
		return err
	}
	var scopes []string
	if err = json.Unmarshal([]byte(permissions), &scopes); err != nil {
		return err
	}
	allowed := false
	for _, scope := range scopes {
		if scope == "send" {
			allowed = true
		}
	}
	if !allowed {
		checks.Errors = append(checks.Errors, PreflightIssue{Code: "user_permission", Message: "Campaign owner lacks send permission"})
	}
	if limit.Valid {
		remaining := max(0, limit.Int64-used)
		checks.UserRemainingDailyQuota = &remaining
		if n-previous > remaining {
			checks.Errors = append(checks.Errors, PreflightIssue{Code: "user_quota", Message: "Campaign exceeds owner's remaining daily sending limit"})
		}
	}
	return nil
}

func publicPreflightURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("only public HTTP or HTTPS URLs without credentials can be checked")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || host == "metadata.google.internal" {
		return fmt.Errorf("local or metadata hosts cannot be checked")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicPreflightIP(ip) {
		return fmt.Errorf("non-public addresses cannot be checked")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return fmt.Errorf("only HTTP and HTTPS ports can be checked")
	}
	return nil
}
func publicPreflightIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
func preflightClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("host has no addresses")
		}
		for _, ip := range addresses {
			if !publicPreflightIP(ip) {
				return nil, fmt.Errorf("host resolves to a non-public address")
			}
		}
		// Dial the validated IP rather than resolving the hostname a second time.
		var last error
		for _, ip := range addresses {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: tr, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func checkPreflightLinks(ctx context.Context, links []render.Link, fields []string, sample render.Recipient, opts PreflightOptions) []PreflightLink {
	client := opts.Client
	if client == nil {
		client = preflightClient()
	}
	copyClient := *client
	copyClient.Timeout = 3 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if tr, ok := copyClient.Transport.(*http.Transport); ok {
		defer tr.CloseIdleConnections()
	}
	result := make([]PreflightLink, len(links))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, l := range links {
		wg.Add(1)
		go func(i int, l render.Link) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := PreflightLink{URL: l.URL, Personalized: preflightTags.MatchString(l.URL)}
			destination, err := render.ResolveLink(l.URL, sample, fields)
			if err == nil {
				err = publicPreflightURL(destination)
			}
			if err != nil {
				item.Status = "blocked"
				item.Message = err.Error()
				result[i] = item
				return
			}
			requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(requestCtx, http.MethodHead, destination, nil)
			if err == nil {
				var response *http.Response
				response, err = copyClient.Do(req)
				if response != nil {
					item.HTTPStatus = response.StatusCode
					response.Body.Close()
				}
			}
			if err != nil {
				item.Status = "broken"
				item.Message = err.Error()
			} else if item.HTTPStatus >= 200 && item.HTTPStatus < 300 {
				item.Status = "ok"
			} else if item.HTTPStatus >= 300 && item.HTTPStatus < 400 {
				item.Status = "warning"
				item.Message = "Redirect was not followed; verify the destination"
			} else if item.HTTPStatus == 405 || item.HTTPStatus == 501 {
				item.Status = "warning"
				item.Message = "Server does not support HEAD checks"
			} else {
				item.Status = "broken"
				item.Message = fmt.Sprintf("HTTP %d", item.HTTPStatus)
			}
			result[i] = item
		}(i, l)
	}
	wg.Wait()
	return result
}
