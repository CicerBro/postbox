package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"postbox/internal/subscribers"
	"strings"
	"testing"
	"time"
)

func TestOverviewPeriodCountersAndRollups(t *testing.T) {
	_, m, db, c, _ := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Overview", FromEmail: "from@example.org"})
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
		{2, 200, 180, 80, 20, 10, 2, false}, {35, 100, 80, 20, 5, 10, 2, false}, {730, 300, 200, 30, 3, 2, 1, false}, {3, 20, 20, 10, 2, 0, 0, true},
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
	list, err := subscribers.New(db).CreateList(context.Background(), subscribers.List{BrandID: brand.ID, Name: "Daily", OptIn: "single"})
	if err != nil {
		t.Fatal(err)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	_, err = db.Writer.Exec("INSERT INTO list_active_history(list_id,at,active,subscribed,unsubscribed) VALUES(?,?,50,2,1)", list.ID, today.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Writer.Exec("INSERT INTO send_activity(brand_id,at,sent) VALUES(?,?,7)", brand.ID, today.AddDate(0, 0, -1).Add(3*time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	w := request(m, "GET", fmt.Sprintf("/api/v1/overview?brand_id=%d", brand.ID), "", &c, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Stats    []overviewKPI       `json:"stats"`
		Current  overviewKPI         `json:"current"`
		Previous overviewKPI         `json:"previous"`
		Change   map[string]*float64 `json:"change"`
		Growth   []struct {
			Active int64 `json:"active"`
		} `json:"growth"`
		Hourly     []map[string]int64 `json:"hourly"`
		Comparison []map[string]int64 `json:"hourly_comparison"`
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
	if len(response.Growth) != 60 || response.Growth[59].Active != 50 || response.Growth[0].Active != 0 {
		t.Fatal(response.Growth)
	}
	if len(response.Hourly) != 48 || response.Hourly[3]["sent"] != 7 || response.Hourly[27]["sent"] != 0 || len(response.Comparison) != 24 || response.Comparison[3]["yesterday"] != 7 {
		t.Fatal(response.Hourly, response.Comparison)
	}
	all := request(m, "GET", fmt.Sprintf("/api/v1/overview?brand_id=%d&days=all", brand.ID), "", &c, "")
	var history struct {
		Current  overviewKPI
		Previous overviewKPI
		Period   map[string]any
		Change   map[string]*float64
	}
	if all.Code != 200 {
		t.Fatal(all.Code, all.Body.String())
	}
	if err = json.Unmarshal(all.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.Current.Sent != 620 || history.Current.Opened != 140 || history.Previous.Sent != 0 {
		t.Fatal(all.Body.String())
	}
	if history.Period["days"] != nil || history.Period["current_start"] != nil || history.Period["previous_start"] != nil {
		t.Fatal(history.Period)
	}
	for name, change := range history.Change {
		if change != nil {
			t.Fatalf("all history has a %s comparison: %v", name, *change)
		}
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
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Empty", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/overview?brand_id=%d", brand.ID)
	for _, days := range []string{"0", "366", "abc", "", "1&days=2", "all&days=7"} {
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
	w = request(m, "GET", "/api/v1/openapi.json", "", &c, "")
	var spec map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	op := spec["paths"].(map[string]any)["/api/v1/overview"].(map[string]any)["get"].(map[string]any)
	if !strings.Contains(fmt.Sprint(op), "campaign_start") || !strings.Contains(fmt.Sprint(op), "days") {
		t.Fatal(op)
	}
}

func TestOverviewChartsLocalBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		name, zone, date           string
		todayHours, yesterdayHours int
	}{
		{"Amsterdam", "Europe/Amsterdam", "2026-10-14T23:30:00Z", 24, 24},
		{"spring", "Europe/Amsterdam", "2026-03-29T20:00:00Z", 23, 24},
		{"autumn", "Europe/Amsterdam", "2026-10-25T20:00:00Z", 25, 24},
		{"previous spring", "Europe/Amsterdam", "2026-03-30T20:00:00Z", 24, 23},
		{"fractional offset", "Asia/Kathmandu", "2026-10-14T20:00:00Z", 24, 24},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			api, _, db, _, _ := setup(t)
			ctx := context.Background()
			service := subscribers.New(db)
			brand, err := service.CreateBrand(ctx, subscribers.Brand{Name: fixture.name, FromEmail: "from@example.org"})
			if err != nil {
				t.Fatal(err)
			}
			list, err := service.CreateList(ctx, subscribers.List{BrandID: brand.ID, Name: "Daily", OptIn: "single"})
			if err != nil {
				t.Fatal(err)
			}
			location, err := time.LoadLocation(fixture.zone)
			if err != nil {
				t.Fatal(err)
			}
			now, err := time.Parse(time.RFC3339, fixture.date)
			if err != nil {
				t.Fatal(err)
			}
			today := localDay(now, location)
			yesterday := today.AddDate(0, 0, -1)
			tomorrow := today.AddDate(0, 0, 1)
			for _, snapshot := range []struct {
				at     time.Time
				active int
			}{
				{yesterday.Add(-time.Minute), 3},
				{today.Add(-time.Millisecond), 5},
				{today, 8},
				{tomorrow, 99},
			} {
				if _, err = db.Writer.Exec(`INSERT INTO list_active_history(list_id,at,active) VALUES(?,?,?)`, list.ID, snapshot.at.UnixMilli(), snapshot.active); err != nil {
					t.Fatal(err)
				}
			}
			// One send just before local midnight belongs to yesterday; one at midnight
			// belongs to today. These instants can fall within the same UTC hour.
			for _, event := range []struct {
				at   time.Time
				sent int
			}{
				{today.Add(-time.Millisecond), 7}, {today, 11}, {now.Add(time.Minute), 100},
			} {
				if _, err = db.Writer.Exec(`INSERT INTO send_activity(brand_id,at,sent) VALUES(?,?,?)`, brand.ID, event.at.UnixMilli(), event.sent); err != nil {
					t.Fatal(err)
				}
			}
			// Each elapsed hour on today's DST transition day contributes once.
			expectedByClock := map[int]int64{}
			for at := today.Add(time.Hour); at.Before(tomorrow) && !at.After(now); at = at.Add(time.Hour) {
				if _, err = db.Writer.Exec(`INSERT INTO send_activity(brand_id,at,sent) VALUES(?,?,1)`, brand.ID, at.UnixMilli()); err != nil {
					t.Fatal(err)
				}
				expectedByClock[at.In(location).Hour()]++
			}
			growth, hours, comparison, err := api.overviewCharts(ctx, brand.ID, now, location)
			if err != nil {
				t.Fatal(err)
			}
			if len(growth) != 60 || growth[58]["active"] != int64(5) || growth[59]["active"] != int64(8) || growth[59]["day"] != today.UnixMilli() || growth[59]["end"] != tomorrow.UnixMilli() {
				t.Fatal(growth[58:])
			}
			if len(hours) != fixture.todayHours+fixture.yesterdayHours || len(comparison) != 24 {
				t.Fatal(len(hours), len(comparison))
			}
			if comparison[0]["today"] != int64(11)+expectedByClock[0] || comparison[23]["yesterday"] != int64(7) {
				t.Fatal(comparison)
			}
			for hour, want := range expectedByClock {
				if hour == 0 {
					want += 11
				}
				if comparison[hour]["today"] != want {
					t.Fatalf("hour %d: %v, want %d", hour, comparison[hour], want)
				}
			}
			if fixture.todayHours == 23 && comparison[2]["today_start"] != nil {
				t.Fatal("skipped hour has an instant", comparison[2])
			}
			if fixture.todayHours == 25 && comparison[2]["today_end"].(int64)-comparison[2]["today_start"].(int64) != 7200000 {
				t.Fatal("repeated hour interval", comparison[2])
			}
		})
	}
}

func TestOverviewTimezoneValidation(t *testing.T) {
	_, m, db, c, _ := setup(t)
	brand, err := subscribers.New(db).CreateBrand(context.Background(), subscribers.Brand{Name: "Timezone", FromEmail: "from@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []string{"", "invalid", "Local", "UTC&timezone=Europe%2FAmsterdam"} {
		w := request(m, "GET", fmt.Sprintf("/api/v1/overview?brand_id=%d&timezone=%s", brand.ID, zone), "", &c, "")
		if w.Code != 400 {
			t.Fatalf("timezone %q: %d %s", zone, w.Code, w.Body.String())
		}
	}
	w := request(m, "GET", fmt.Sprintf("/api/v1/overview?brand_id=%d&timezone=Europe%%2FAmsterdam", brand.ID), "", &c, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"chart_timezone":"Europe/Amsterdam"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
