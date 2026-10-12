package render

import (
	"strings"
	"testing"
)

func TestBindLinkIDsUsesCanonicalTagsAndSimultaneousMapping(t *testing.T) {
	const base = "https://track.example/path?a=1&b=2"
	p, err := Prepare(Options{HTML: `<a href="https://first.example">First</a><a href="https://second.example">Second</a>`, TrackingURL: base})
	if err != nil {
		t.Fatal(err)
	}
	body, text, links, err := p.BindLinkIDs(base, map[int64]int64{1: 2, 2: 3})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := Restore(p.Subject, body, text, links, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, rendered, _, err := bound.Render(Recipient{Token: "test"})
	if err != nil || !strings.Contains(rendered, `a=1&amp;b=2/c/2/test`) || !strings.Contains(rendered, `a=1&amp;b=2/c/3/test`) || strings.Contains(rendered, `/c/1/test`) {
		t.Fatalf("incorrect link remapping: %s error=%v", rendered, err)
	}
	if _, _, _, err := p.BindLinkIDs(base, map[int64]int64{1: 2}); err == nil {
		t.Fatal("accepted incomplete link mapping")
	}
}
