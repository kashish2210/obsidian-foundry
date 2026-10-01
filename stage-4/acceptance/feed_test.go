package acceptance

import (
	"testing"
)

func TestR29_R32_FeedVisibilityRule(t *testing.T) {
	e := setup(t)
	pub := str(t, mustPay(t, e.tok["ada"], "bob", 10, nil), "payment_id")                                      // ada->bob public
	priv := str(t, mustPay(t, e.tok["ada"], "bob", 11, map[string]any{"visibility": "private"}), "payment_id") // ada->bob private
	priv2 := str(t, mustPay(t, e.tok["bob"], "cy", 12, map[string]any{"visibility": "private"}), "payment_id") // bob->cy private
	pub2 := str(t, mustPay(t, e.tok["bob"], "cy", 13, map[string]any{"visibility": "public"}), "payment_id")   // bob->cy public
	want := map[string]map[string]bool{
		"ada": {pub: true, priv: true, priv2: false, pub2: true}, // sender of priv; not party to priv2
		"bob": {pub: true, priv: true, priv2: true, pub2: true},  // receiver of priv, sender of priv2
		"cy":  {pub: true, priv: false, priv2: true, pub2: true}, // receiver of priv2
		"dee": {pub: true, priv: false, priv2: false, pub2: true},
		"op":  {pub: true, priv: false, priv2: false, pub2: true},
	}
	for h, w := range want {
		items, _ := activity(t, e.tok[h], "?limit=200")
		got := idSet(t, items, "payment_id")
		for id, vis := range w {
			if got[id] != vis {
				t.Fatalf("%s: payment %s visible=%v want %v", h, id, got[id], vis)
			}
		}
		if len(got) != len(items) {
			t.Fatalf("%s: duplicate items in feed", h)
		}
		n := 0
		for _, v := range w {
			if v {
				n++
			}
		}
		if len(items) != n {
			t.Fatalf("%s: feed has %d items, want %d", h, len(items), n)
		}
		// R32: visibility is the same single value to everyone who can see it
		for _, it := range items {
			id := str(t, it, "payment_id")
			if (id == priv || id == priv2) && it["visibility"] != "private" {
				t.Fatalf("%s sees visibility %v for private payment", h, it["visibility"])
			}
			if (id == pub || id == pub2) && it["visibility"] != "public" {
				t.Fatalf("%s sees visibility %v for public payment", h, it["visibility"])
			}
			if !isNull(it, "settlement_id") {
				t.Fatalf("ordinary payment has settlement_id %v", it["settlement_id"])
			}
		}
	}
}

func TestR28_R30_RequestsNeverInFeedVisibilityFromPayer(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	for _, h := range []string{"ada", "bob", "cy", "op"} {
		a, _ := activity(t, e.tok[h], "")
		if len(a) != 0 {
			t.Fatalf("%s sees request in activity: %v", h, a)
		}
	}
	// requests carry no visibility field of their own
	out, _ := listReq(t, e.tok["bob"], "")
	if _, has := out[0]["visibility"]; has {
		t.Fatalf("request exposes visibility: %v", out[0])
	}
	// the payer chooses the visibility of the resulting payment
	p := postK(t, "/requests/"+id+"/pay", e.tok["ada"], map[string]any{"visibility": "private"})
	expect(t, p, 201)
	pid := str(t, p.obj(t), "payment_id")
	got := map[string]bool{}
	for _, h := range []string{"ada", "bob", "cy", "dee"} {
		a, _ := activity(t, e.tok[h], "")
		got[h] = idSet(t, a, "payment_id")[pid]
		for _, it := range a {
			if it["payment_id"] == pid && it["request_id"] != id {
				t.Fatalf("payment lacks request_id: %v", it)
			}
		}
	}
	if !got["ada"] || !got["bob"] || got["cy"] || got["dee"] {
		t.Fatalf("private request payment visibility: %v", got)
	}
	id2 := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	expect(t, postK(t, "/requests/"+id2+"/pay", e.tok["ada"], map[string]any{"visibility": "public"}), 201)
	a, _ := activity(t, e.tok["cy"], "")
	if len(a) != 1 {
		t.Fatalf("cy should see exactly the public payment: %v", a)
	}
	// requests still only for their two parties
	if out, _ := listReq(t, e.tok["cy"], ""); len(out) != 0 {
		t.Fatal("cy sees others' requests")
	}
}

func TestR100_ActivityOrderShapeAndPaging(t *testing.T) {
	e := setup(t)
	ids := []string{}
	for i := 0; i < 3; i++ {
		ids = append(ids, str(t, mustPay(t, e.tok["ada"], "bob", int64(i+1), nil), "payment_id"))
		tick() // the order of payments created in the same second is unspecified
	}
	out, more := activity(t, e.tok["cy"], "") // public -> visible to a third party
	if more || len(out) != 3 {
		t.Fatalf("%d %v", len(out), more)
	}
	for i, it := range out {
		if it["payment_id"] != ids[2-i] {
			t.Fatalf("newest first violated at %d: %v", i, it["payment_id"])
		}
	}
	for i := 1; i < len(out); i++ {
		a, b := checkTS(t, str(t, out[i-1], "created_at")), checkTS(t, str(t, out[i], "created_at"))
		if a.Before(b) {
			t.Fatal("created_at not descending")
		}
	}
	p, more := activity(t, e.tok["cy"], "?limit=1")
	if len(p) != 1 || !more || p[0]["payment_id"] != ids[2] {
		t.Fatalf("limit=1: %v %v", p, more)
	}
	p, more = activity(t, e.tok["cy"], "?limit=1&offset=2")
	if len(p) != 1 || more || p[0]["payment_id"] != ids[0] {
		t.Fatalf("offset=2: %v %v", p, more)
	}
	p, more = activity(t, e.tok["cy"], "?limit=3")
	if len(p) != 3 || more {
		t.Fatalf("exact fit: %d %v", len(p), more)
	}
	p, more = activity(t, e.tok["cy"], "?offset=3")
	if len(p) != 0 || more {
		t.Fatalf("offset at end: %d %v", len(p), more)
	}
	p, more = activity(t, e.tok["cy"], "?offset=99")
	if len(p) != 0 || more {
		t.Fatalf("offset past end: %d %v", len(p), more)
	}
	// full payment shape in the feed
	it := out[0]
	for _, k := range []string{"payment_id", "from_user_id", "from_handle", "to_user_id", "to_handle", "currency", "note", "visibility", "created_at"} {
		if _, ok := it[k].(string); !ok {
			t.Fatalf("feed item lacks string %s: %v", k, it)
		}
	}
	if _, ok := it["amount"]; !ok || !isNull(it, "request_id") || !isNull(it, "settlement_id") {
		t.Fatalf("feed item: %v", it)
	}
}

func TestR100_R49_R46_ActivityParamValidation(t *testing.T) {
	e := setup(t)
	for _, q := range []string{"limit=0", "limit=201", "limit=-1", "limit=abc", "limit=", "limit=1e2", "limit=+4", "limit=4.0", "limit=1e9",
		"offset=-1", "offset=abc", "offset=", "offset=1e1", "offset=+1", "offset=1.0", "offset=0.5", "limit=%204"} {
		expectErr(t, get(t, "/activity?"+q, e.tok["ada"]), 422, "validation_failed")
	}
	for _, q := range []string{"limit=1", "limit=200", "offset=0", "limit=50&offset=0", "limit=10&offset=3"} {
		expect(t, get(t, "/activity?"+q, e.tok["ada"]), 200)
	}
}

func TestR100_DefaultLimit50AndMax(t *testing.T) {
	e := setupUsers(t, nil, fu{"ada", 100000}, fu{"bob", 0})
	for i := 0; i < 55; i++ {
		mustPay(t, e.tok["ada"], "bob", 1, nil)
	}
	out, more := activity(t, e.tok["bob"], "")
	if len(out) != 50 || !more {
		t.Fatalf("default limit: %d %v", len(out), more)
	}
	out, more = activity(t, e.tok["bob"], "?limit=200")
	if len(out) != 55 || more {
		t.Fatalf("limit 200: %d %v", len(out), more)
	}
	seen := map[string]bool{}
	for off := 0; off < 55; off += 20 {
		pg, _ := activity(t, e.tok["bob"], "?limit=20&offset="+itoa(off))
		for _, it := range pg {
			seen[str(t, it, "payment_id")] = true
		}
	}
	if len(seen) != 55 {
		t.Fatalf("paging covered %d of 55", len(seen))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
