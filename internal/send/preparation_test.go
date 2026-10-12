package send

import (
	"context"
	"strings"
	"testing"
	"time"

	"postbox/internal/render"
)

func TestPreparationValidatesTemplateBeforeAcquiringWriter(t *testing.T) {
	db, engine, _ := fixture(t, 0)
	if _, err := db.Writer.Exec(`UPDATE campaigns SET html='<p>[UnknownField]</p>',inline_css=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Writer.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = engine.prepare(ctx, 1, true)
	if err == nil || !strings.Contains(err.Error(), "UnknownField") {
		t.Fatalf("template validation waited for busy writer: %v", err)
	}
}

func TestPreparationRejectsSourceChangedWhileWaitingForWriter(t *testing.T) {
	for _, change := range []string{
		`UPDATE campaigns SET subject='Changed' WHERE id=1`,
		`UPDATE brands SET from_email='changed@example.org' WHERE id=1`,
		`INSERT INTO custom_fields(brand_id,key,label,type) VALUES(1,'new_field','New','text')`,
	} {
		t.Run(change, func(t *testing.T) {
			db, engine, _ := fixture(t, 0)
			conn, err := db.Writer.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			waiting := db.Writer.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				_, err := engine.prepare(ctx, 1, true)
				done <- err
			}()
			for db.Writer.Stats().WaitCount == waiting {
				if ctx.Err() != nil {
					t.Fatal("preparation did not finish rendering")
				}
				time.Sleep(time.Millisecond)
			}
			if _, err := conn.ExecContext(ctx, change); err != nil {
				t.Fatal(err)
			}
			conn.Close()
			if err := <-done; err == nil || !strings.Contains(err.Error(), "changed during preparation") {
				t.Fatalf("stale source persisted: %v", err)
			}
			for _, table := range []string{"campaign_preparations", "links"} {
				var count int
				if err := db.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("stale %s rows=%d error=%v", table, count, err)
				}
			}
		})
	}
}

func TestConcurrentPreparationUsesOneDurableLinkMapping(t *testing.T) {
	db, engine, _ := fixture(t, 0)
	for _, query := range []string{
		`INSERT INTO campaigns(id,brand_id,uuid,status,subject,html,created_at,updated_at) VALUES(2,1,'other','draft','Other','<p>Other</p>',1,1)`,
		`INSERT INTO links(id,campaign_id,url) VALUES(1,2,'https://unrelated.example.org')`,
		`UPDATE campaigns SET html='<a href="https://first.example.org">First</a><a href="https://second.example.org">Second</a>',inline_css=1 WHERE id=1`,
	} {
		if _, err := db.Writer.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	type answer struct {
		campaign campaign
		err      error
	}
	done := make(chan answer, 8)
	for i := 0; i < cap(done); i++ {
		go func() {
			c, err := engine.prepare(context.Background(), 1, true)
			done <- answer{c, err}
		}()
	}
	var first string
	for i := 0; i < cap(done); i++ {
		result := <-done
		if result.err != nil {
			t.Fatal(result.err)
		}
		_, body, _, err := result.campaign.prepared.Render(render.Recipient{Token: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, "/c/2/test") || !strings.Contains(body, "/c/3/test") || strings.Contains(body, "/c/1/test") {
			t.Fatalf("incorrect bound links: %s; prepared %s links %+v", body, result.campaign.prepared.HTML, result.campaign.prepared.Links)
		}
		if first == "" {
			first = body
		} else if body != first {
			t.Fatalf("concurrent preparations returned different snapshots")
		}
	}
	var count int
	if err := db.Reader.QueryRow(`SELECT count(*) FROM links WHERE campaign_id=1`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("link count=%d error=%v", count, err)
	}
}
