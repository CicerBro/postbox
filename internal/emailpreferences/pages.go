package emailpreferences

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/http"
)

// Start keeps public email pages self-contained on tracking domains.
func Start(w http.ResponseWriter, title, brandName string) {
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title><style>
:root{color-scheme:light;--background:#fff;--foreground:#18181b;--card:#fff;--border:#e4e4e7;--muted:#71717a;--hover:#f4f4f5;--ring:#a16207}
@media(prefers-color-scheme:dark){:root{color-scheme:dark;--background:oklch(.141 .005 285.823);--foreground:oklch(.985 0 0);--card:oklch(.21 .006 285.885);--border:oklch(1 0 0 / 10%%);--muted:oklch(.705 .015 286.067);--hover:oklch(.274 .006 286.033);--ring:oklch(.795 .184 86.047)}}
*{box-sizing:border-box}body{margin:0;background:var(--background);color:var(--foreground);font-family:Inter,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;font-size:14px;line-height:1.5}
main{min-height:100vh;min-height:100dvh;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:24px;padding:24px}
.brand{display:flex;align-items:center;gap:10px;font-size:20px;font-weight:600;letter-spacing:-.4px}.brand-mark{display:grid;place-items:center;width:32px;height:32px;border-radius:8px;background:oklch(.795 .184 86.047);color:#000}
.card{width:min(400px,100%%);padding:24px;background:var(--card);border:1px solid var(--border);border-radius:14px;box-shadow:0 1px 3px #0000001a,0 1px 2px -1px #0000001a;overflow-wrap:anywhere}
h1{margin:0 0 8px;font-size:24px;font-weight:600;letter-spacing:-.5px;line-height:1.25}h2{margin:0 0 8px;font-size:16px;font-weight:600}p{margin:0 0 16px}.muted{color:var(--muted)}.actions{display:grid;gap:10px;margin-top:24px}
button{width:100%%;min-height:40px;border:1px solid var(--border);border-radius:8px;padding:10px 12px;font:inherit;color:inherit;background:var(--card)}button{cursor:pointer;font-weight:500;line-height:1.4}button:hover{background:var(--hover)}.primary{background:oklch(.795 .184 86.047);border-color:transparent;color:#000}.primary:hover{background:oklch(.75 .184 86.047)}button:focus-visible{outline:2px solid var(--ring);outline-offset:3px}
.card>p:last-child{margin-bottom:0}
@media(max-width:420px){main{padding:20px 16px}.card{padding:24px 20px}}
</style></head><body><main><div class="brand"><span class="brand-mark" aria-hidden="true"><svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="16" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/></svg></span>Postbox</div><section class="card">`, html.EscapeString(title))
	fmt.Fprintf(w, `<p class="muted">Emails from <strong>%s</strong></p>`, html.EscapeString(brandName))
}

func End(w http.ResponseWriter) {
	fmt.Fprint(w, `</section></main></body></html>`)
}

func Confirmation(w http.ResponseWriter, title, message, brandName string) {
	Start(w, title, brandName)
	fmt.Fprintf(w, `<h1>%s</h1><p class="muted">%s</p>`, html.EscapeString(title), html.EscapeString(message))
	End(w)
}

// CurrentMailingLists resolves only included campaign lists the recipient belongs to.
func CurrentMailingLists(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, campaign, subscriber int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT l.id FROM campaign_targets t JOIN campaigns c ON c.id=t.campaign_id JOIN lists l ON l.id=t.target_id AND l.brand_id=c.brand_id WHERE t.campaign_id=? AND t.target_type='list' AND t.mode='include' AND (?=0 OR EXISTS(SELECT 1 FROM list_subscribers ls WHERE ls.list_id=l.id AND ls.subscriber_id=?)) ORDER BY l.id`, campaign, subscriber, subscriber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
