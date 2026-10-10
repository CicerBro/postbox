package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"postbox/internal/contacts"
	"strings"
	"testing"
	"time"
)

func TestOverviewPeriodCountersAndRollups(t *testing.T) {
	_, m, db, c, _ := setup(t)
	brand, err := contacts.New(db).CreateBrand(context.Background(), contacts.Brand{Name: "Overview", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// No recipients or raw events are needed: reports use durable campaign counters.
	for i, fixture := range []struct {
		age                                                   int
		sent, delivered, opened, clicked, bounced, complaints int
		fallback                                              bool
	}{
		{2, 200, 180, 80, 20, 10, 2, false}, {35, 100, 80, 20, 5, 10, 2, false}, {90, 300, 200, 30, 3, 2, 1, false}, {3, 20, 20, 10, 2, 0, 0, true},
	} {
		at := now.AddDate(0, 0, -fixture.age).UnixMilli()
		var started any = at
		if fixture.fallback {
			started = nil
		}
		_, err = db.Writer.Exec("INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,started_at,created_at,updated_at) VALUES(?,?,?,'sent','test','<p>test</p>',?,?,?)", i+1, brand.ID, fmt.Sprintf("overview-%d", i), started, at, at)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Writer.Exec("INSERT INTO campaign_stats(campaign_id,sent,delivered,opens_unique,clicks_unique,bounces_hard,complaints) VALUES(?,?,?,?,?,?,?)", i+1, fixture.sent, fixture.delivered, fixture.opened, fixture.clicked, fixture.bounced, fixture.complaints)
		if err != nil {
			t.Fatal(err)
		}
	}
	list, err := contacts.New(db).CreateList(context.Background(), contacts.List{BrandID: brand.ID, Name: "Daily", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	_, err = db.Writer.Exec("INSERT INTO list_daily_stats(list_id,day,active,subscribed,unsubscribed) VALUES(?,?,50,2,1)", list.ID, today.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Writer.Exec("INSERT INTO send_hourly(brand_id,hour,sent) VALUES(?,?,7)", brand.ID, today.AddDate(0, 0, -1).Add(3*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	w := request(m, "GET", fmt.Sprintf("/api/v1/overview?brand_id=%d", brand.ID), "", &c, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Stats      []overviewKPI       `json:"stats"`
		Current    overviewKPI         `json:"current"`
		Previous   overviewKPI         `json:"previous"`
		Change     map[string]*float64 `json:"change"`
		Growth     []map[string]int64  `json:"growth"`
		Hourly     []map[string]int64  `json:"hourly"`
		Comparison []map[string]int64  `json:"hourly_comparison"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Current.Sent != 220 || response.Previous.Sent != 100 || response.Stats[0].Sent != 620 {
		t.Fatal(w.Body.String())
	}
	if response.Change["sent_percent"] == nil || *response.Change["sent_percent"] != 120 {
		t.Fatal(response.Change)
	}
	wantPoints := 100*float64(200)/220 - 80
	if response.Change["delivered_points"] == nil || math.Abs(*response.Change["delivered_points"]-wantPoints) > 1e-8 {
		t.Fatal(response.Change)
	}
	if len(response.Growth) != 60 || response.Growth[59]["active"] != 50 || response.Growth[0]["active"] != 0 {
		t.Fatal(response.Growth)
	}
	if len(response.Hourly) != 48 || response.Hourly[3]["sent"] != 7 || response.Hourly[27]["sent"] != 0 || len(response.Comparison) != 24 || response.Comparison[3]["yesterday"] != 7 {
		t.Fatal(response.Hourly, response.Comparison)
	}
	rows, err := db.Reader.Query("EXPLAIN QUERY PLAN "+overviewPeriodSQL, brand.ID, now.AddDate(0, 0, -60).UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"subscribers", "list_subscribers", "campaign_recipients", "opens", "clicks", "ses_events"} {
			if strings.Contains(detail, "SCAN "+table) {
				t.Fatal("large table scan", detail)
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestOverviewEmptyAndDaysValidation(t *testing.T) {
	_, m, db, c, _ := setup(t)
	brand, err := contacts.New(db).CreateBrand(context.Background(), contacts.Brand{Name: "Empty", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/overview?brand_id=%d", brand.ID)
	for _, days := range []string{"0", "366", "abc", "", "1&days=2"} {
		w := request(m, "GET", path+"&days="+days, "", &c, "")
		if w.Code != 400 {
			t.Fatalf("days %q: %d %s", days, w.Code, w.Body.String())
		}
	}
	w := request(m, "GET", path+"&days=7", "", &c, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Current overviewKPI
		Change  map[string]*float64
		Period  map[string]any
	}
	if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Current.Sent != 0 || v.Current.OpenedPercent != nil || v.Change["sent_percent"] != nil || v.Change["opened_points"] != nil || v.Period["days"] != float64(7) {
		t.Fatal(w.Body.String())
	}
	w = request(m, "GET", "/api/v1/openapi.json", "", nil, "")
	var spec map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	op := spec["paths"].(map[string]any)["/api/v1/overview"].(map[string]any)["get"].(map[string]any)
	if !strings.Contains(fmt.Sprint(op), "campaign_start") || !strings.Contains(fmt.Sprint(op), "days") {
		t.Fatal(op)
	}
}
