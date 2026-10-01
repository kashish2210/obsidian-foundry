package acceptance

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- R176: the two new idempotent write paths follow every stage-1 idempotency rule ----------

func azOps() []op {
	return []op{
		{"authorizations", func(e *env) string { return e.tok["ada"] }, func(t testing.TB, e *env) string { return "/authorizations" },
			func() any { return map[string]any{"to_handle": "bob", "amount": 100, "note": "x"} }},
		{"capture", func(e *env) string { return e.tok["bob"] }, func(t testing.TB, e *env) string {
			return "/authorizations/" + aid(t, mustAuthorize(t, e.tok["ada"], "bob", 500, nil)) + "/capture"
		}, func() any { return map[string]any{"amount": 200, "final": false} }},
	}
}

func TestR176_NewPathsIdempotency(t *testing.T) {
	for _, o := range azOps() {
		e := azSetup(t)
		p, tk := o.path(t, e), o.tok(e)
		// R61 missing/empty key
		expectErr(t, call(t, "POST", p, tk, nil, o.body()), 400, "missing_idempotency_key")
		expectErr(t, call(t, "POST", p, tk, map[string]string{"Idempotency-Key": ""}, o.body()), 400, "missing_idempotency_key")
		// R48 key length
		expectErr(t, post(t, p, tk, fmt.Sprintf("%0256d", 1), o.body()), 422, "validation_failed")
		// R62/R63 first use then replay
		k := newKey()
		first := post(t, p, tk, k, o.body())
		expect(t, first, 201)
		for i := 0; i < 3; i++ {
			rep := post(t, p, tk, k, o.body())
			expect(t, rep, 200)
			requireSameJSON(t, first, rep)
		}
		if e.sum(t) != e.total || total(t, e.tok["ada"]) < 0 {
			t.Fatalf("%s: sum", o.name)
		}
		// R64 different body
		var alt any
		if o.name == "capture" {
			alt = map[string]any{"amount": 201, "final": false}
		} else {
			alt = map[string]any{"to_handle": "bob", "amount": 101, "note": "x"}
		}
		expectErr(t, post(t, p, tk, k, alt), 409, "idempotency_key_reuse")
		// R68 key order / whitespace / numeric spelling
		var spelled string
		if o.name == "capture" {
			spelled = "{ \"final\" : false,\n \"amount\": 2e2 }"
		} else {
			spelled = `{"note":"x", "amount": 1e2 ,"to_handle":"bob"}`
		}
		rep := post(t, p, tk, k, spelled)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
		// R71 claimed key resolved before validation
		expectErr(t, post(t, p, tk, k, map[string]any{}), 409, "idempotency_key_reuse")
		expectErr(t, post(t, p, tk, k, map[string]any{"amount": -1, "to_handle": "ghost"}), 409, "idempotency_key_reuse")
		expectErr(t, post(t, p, tk, k, `{`), 400, "malformed_request")
		// R65 failed attempt does not claim the key
		k2 := newKey()
		expectErr(t, post(t, p, tk, k2, map[string]any{"amount": -1}), 422, "validation_failed")
		expect(t, post(t, p, tk, k2, o.body()), 201)
		// R66 keys scoped per user
		other := e.tok["cy"]
		if o.name == "capture" {
			expectErr(t, post(t, p, other, k, o.body()), 403, "forbidden") // not the receiver; does not interact with bob's key
		} else {
			expect(t, post(t, p, other, k, map[string]any{"to_handle": "bob", "amount": 100, "note": "x"}), 201)
			rep = post(t, p, tk, k, o.body())
			expect(t, rep, 200)
			requireSameJSON(t, first, rep)
		}
	}
}

func TestR176_SameKeyOnDifferentNewPaths(t *testing.T) {
	e := azSetup(t)
	a := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 500, nil))
	b := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 500, nil))
	k := newKey()
	body := map[string]any{"amount": 100, "final": false}
	r1 := post(t, "/authorizations/"+a+"/capture", e.tok["bob"], k, body)
	r2 := post(t, "/authorizations/"+b+"/capture", e.tok["bob"], k, body)
	expect(t, r1, 201)
	expect(t, r2, 201) // different path: not a replay
	if r1.obj(t)["payment_id"] == r2.obj(t)["payment_id"] {
		t.Fatal("same payment")
	}
	// the same key and body on /payments and /authorizations are independent too
	k2 := newKey()
	body2 := map[string]any{"to_handle": "cy", "amount": 10}
	expect(t, post(t, "/payments", e.tok["ada"], k2, body2), 201)
	expect(t, post(t, "/authorizations", e.tok["ada"], k2, body2), 201)
	expect(t, post(t, "/payments", e.tok["ada"], k2, body2), 200)
	expect(t, post(t, "/authorizations", e.tok["ada"], k2, body2), 200)
}

func TestR176_R70_ReplayAfterChange(t *testing.T) {
	e := azSetup(t)
	k := newKey()
	body := map[string]any{"to_handle": "bob", "amount": 400}
	first := post(t, "/authorizations", e.tok["ada"], k, body)
	expect(t, first, 201)
	id := aid(t, first.obj(t))
	expect(t, voidA(t, e.tok["ada"], id), 200)
	rep := post(t, "/authorizations", e.tok["ada"], k, body)
	expect(t, rep, 200)
	requireSameJSON(t, first, rep) // the original response, still "open"
	if rep.obj(t)["status"] != "open" {
		t.Fatal("replay must return the original body")
	}
	if held(t, e.tok["ada"]) != 0 {
		t.Fatal("replay re-created the hold")
	}
	if o, _ := listAuthz(t, e.tok["ada"], ""); len(o) != 1 {
		t.Fatalf("%d authorizations", len(o))
	}
}

func TestR176_ConcurrentIdenticalAuthorizeAndCapture(t *testing.T) {
	e := azSetup(t)
	k := newKey()
	rs := fanout(40, func(int) (resp, error) {
		return send("POST", "/authorizations", e.tok["ada"], map[string]string{"Idempotency-Key": k}, map[string]any{"to_handle": "bob", "amount": 1000})
	})
	created := onlyOneCreated(t, rs)
	if held(t, e.tok["ada"]) != 1000 {
		t.Fatalf("hold placed %d times", held(t, e.tok["ada"])/1000)
	}
	id := aid(t, created.obj(t))
	kc := newKey()
	rs = fanout(40, func(int) (resp, error) {
		return send("POST", "/authorizations/"+id+"/capture", e.tok["bob"], map[string]string{"Idempotency-Key": kc}, map[string]any{"amount": 300, "final": false})
	})
	onlyOneCreated(t, rs)
	if total(t, e.tok["bob"]) != 2800 || held(t, e.tok["ada"]) != 700 {
		t.Fatalf("capture effect not once: bob=%d held=%d", total(t, e.tok["bob"]), held(t, e.tok["ada"]))
	}
}

// ---------- R163-R166 concurrency ----------

func TestR164_R165_ConcurrentFinalCapturesDistinctKeys(t *testing.T) {
	e := azSetup(t)
	id := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 1000, nil))
	rs := fanout(50, func(int) (resp, error) {
		return send("POST", "/authorizations/"+id+"/capture", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
	})
	n201 := 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			n201++
		case 409:
			expectErr(t, x.r, 409, "authorization_not_open")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if n201 != 1 {
		t.Fatalf("%d captures succeeded", n201)
	}
	if total(t, e.tok["ada"]) != 9000 || total(t, e.tok["bob"]) != 3500 || held(t, e.tok["ada"]) != 0 {
		t.Fatal("money moved more than once")
	}
}

func TestR165_ConcurrentPartialCapturesNeverExceed(t *testing.T) {
	e := azSetup(t)
	id := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 1000, nil))
	var ok int64
	rs := fanout(50, func(int) (resp, error) {
		r, err := send("POST", "/authorizations/"+id+"/capture", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"amount": 100, "final": false})
		if err == nil && r.Status == 201 {
			atomic.AddInt64(&ok, 1)
		}
		return r, err
	})
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
		case 409:
			expectErr(t, x.r, 409, "authorization_not_open")
		case 422:
			expectErr(t, x.r, 422, "capture_exceeds_authorization")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if ok != 10 {
		t.Fatalf("%d captures succeeded, want exactly 10", ok)
	}
	g := getAuthz(t, e.tok["ada"], id)
	if num(t, g, "captured_amount") != 1000 || g["status"] != "captured" || len(ints(g["payment_ids"])) != 10 || num(t, g, "remaining_amount") != 0 {
		t.Fatalf("final state %v", g)
	}
	if total(t, e.tok["ada"]) != 9000 || total(t, e.tok["bob"]) != 3500 || held(t, e.tok["ada"]) != 0 {
		t.Fatal("balances")
	}
}

func TestR164_PaymentsAndCapturesAgainstHeldFunds(t *testing.T) {
	e := setupAZ(t, nil, nil, fu{"ada", 1000}, fu{"bob", 0}, fu{"cy", 0})
	id := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 600, nil)) // available 400
	var payOK, capOK int64
	rs := fanout(51, func(i int) (resp, error) {
		if i == 0 {
			r, err := send("POST", "/authorizations/"+id+"/capture", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
			if err == nil && r.Status == 201 {
				atomic.AddInt64(&capOK, 1)
			}
			return r, err
		}
		r, err := send("POST", "/payments", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"to_handle": "cy", "amount": 100})
		if err == nil && r.Status == 201 {
			atomic.AddInt64(&payOK, 1)
		}
		return r, err
	})
	for i, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		if x.r.Status != 201 && x.r.Status != 409 {
			t.Fatalf("%d: %s", i, x.r)
		}
		if i == 0 && x.r.Status != 201 {
			t.Fatalf("the capture must always succeed: %s", x.r)
		}
		if i > 0 && x.r.Status == 409 {
			expectErr(t, x.r, 409, "insufficient_funds")
		}
	}
	if capOK != 1 || payOK != 4 {
		t.Fatalf("capture=%d payments=%d (want 1 and 4)", capOK, payOK)
	}
	if total(t, e.tok["ada"]) != 0 || total(t, e.tok["bob"]) != 600 || total(t, e.tok["cy"]) != 400 || held(t, e.tok["ada"]) != 0 {
		t.Fatalf("final: ada=%d bob=%d cy=%d", total(t, e.tok["ada"]), total(t, e.tok["bob"]), total(t, e.tok["cy"]))
	}
}

func TestR164_ConcurrentAuthorizationsLimitedByAvailable(t *testing.T) {
	e := setupAZ(t, nil, nil, fu{"ada", 1000}, fu{"bob", 0}, fu{"cy", 0})
	var ok int64
	rs := fanout(50, func(i int) (resp, error) {
		to := []string{"bob", "cy"}[i%2]
		r, err := send("POST", "/authorizations", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"to_handle": to, "amount": 100})
		if err == nil && r.Status == 201 {
			atomic.AddInt64(&ok, 1)
		}
		return r, err
	})
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		if x.r.Status != 201 {
			expectErr(t, x.r, 409, "insufficient_funds")
		}
	}
	m := meObj(t, e.tok["ada"])
	if ok != 10 || num(t, m, "held") != 1000 || num(t, m, "available") != 0 || num(t, m, "total") != 1000 {
		t.Fatalf("ok=%d %v", ok, m)
	}
}

func TestR164_VoidVersusCaptureRace(t *testing.T) {
	for round := 0; round < 6; round++ {
		e := azSetup(t)
		id := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 800, nil))
		var capOK, voidOK int64
		rs := fanout(20, func(i int) (resp, error) {
			if i%2 == 0 {
				r, err := send("POST", "/authorizations/"+id+"/capture", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
				if err == nil && r.Status == 201 {
					atomic.AddInt64(&capOK, 1)
				}
				return r, err
			}
			r, err := send("POST", "/authorizations/"+id+"/void", e.tok["ada"], nil, nil)
			if err == nil && r.Status == 200 {
				atomic.AddInt64(&voidOK, 1)
			}
			return r, err
		})
		for _, x := range rs {
			if x.err != nil {
				t.Fatal(x.err)
			}
			if x.r.Status >= 500 {
				t.Fatalf("%s", x.r)
			}
		}
		g := getAuthz(t, e.tok["ada"], id)
		moved := total(t, e.tok["ada"]) != 10000
		if capOK > 1 {
			t.Fatal("captured more than once")
		}
		switch g["status"] {
		case "captured":
			if !moved || capOK != 1 || total(t, e.tok["bob"]) != 3300 {
				t.Fatalf("captured but moved=%v capOK=%d", moved, capOK)
			}
		case "voided":
			if moved || capOK != 0 || total(t, e.tok["bob"]) != 2500 {
				t.Fatalf("voided but money moved (moved=%v capOK=%d)", moved, capOK)
			}
		default:
			t.Fatalf("status %v", g["status"])
		}
		if held(t, e.tok["ada"]) != 0 || e.sum(t) != e.total {
			t.Fatal("hold or sum")
		}
	}
}

func TestR163_R166_ConcurrentMixedInvariants(t *testing.T) {
	us := []fu{{"ada", 3000}, {"bob", 2000}, {"cy", 1000}, {"dee", 500}, {"eve", 0}}
	e := setupUsers(t, nil, us...)
	hs := handlesOf(us)
	var bad atomic.Value
	note := func(s string) { bad.CompareAndSwap(nil, s) }
	stop := make(chan struct{})
	var pw sync.WaitGroup
	for i := 0; i < 4; i++ {
		pw.Add(1)
		go func() {
			defer pw.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h := hs[rand.Intn(len(hs))]
				r, err := send("GET", "/me", e.tok[h], nil, nil)
				if err != nil || r.Status != 200 {
					note(fmt.Sprintf("poll: %v %v", err, r))
					return
				}
				v, _ := decodeNum(r.Body)
				m := v.(map[string]any)
				tot, _ := m["total"].(json.Number).Int64()
				av, _ := m["available"].(json.Number).Int64()
				hd, _ := m["held"].(json.Number).Int64()
				b, _ := m["balance"].(json.Number).Int64()
				if tot != b || av != tot-hd || av < 0 || hd < 0 || tot < 0 {
					note(fmt.Sprintf("invariant broken at read: %s", r))
				}
			}
		}()
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var authIDs []string
	for w := 0; w < 50; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)*13 + 1))
			for i := 0; i < 8; i++ {
				me, other := hs[rng.Intn(len(hs))], hs[rng.Intn(len(hs))]
				if me == other {
					continue
				}
				var r resp
				var err error
				switch rng.Intn(5) {
				case 0:
					r, err = send("POST", "/payments", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"to_handle": other, "amount": rng.Intn(300) + 1})
				case 1, 2:
					r, err = send("POST", "/authorizations", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"to_handle": other, "amount": rng.Intn(400) + 1})
					if err == nil && r.Status == 201 {
						v, _ := decodeNum(r.Body)
						mu.Lock()
						authIDs = append(authIDs, v.(map[string]any)["authorization_id"].(string))
						mu.Unlock()
					}
				case 3:
					mu.Lock()
					var id string
					if len(authIDs) > 0 {
						id = authIDs[rng.Intn(len(authIDs))]
					}
					mu.Unlock()
					if id == "" {
						continue
					}
					body := map[string]any{"amount": rng.Intn(200) + 1, "final": rng.Intn(2) == 0}
					// whoever the receiver is will succeed; everyone else is 403. Try everyone.
					r, err = send("POST", "/authorizations/"+id+"/capture", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, body)
				default:
					mu.Lock()
					var id string
					if len(authIDs) > 0 {
						id = authIDs[rng.Intn(len(authIDs))]
					}
					mu.Unlock()
					if id == "" {
						continue
					}
					r, err = send("POST", "/authorizations/"+id+"/void", e.tok[me], nil, nil)
				}
				if err != nil {
					note(err.Error())
				} else if r.Status >= 500 {
					note(r.String())
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	pw.Wait()
	if v := bad.Load(); v != nil {
		t.Fatalf("%v", v)
	}
	var sum int64
	for _, h := range hs {
		m := meObj(t, e.tok[h])
		checkMe(t, m)
		sum += num(t, m, "total")
	}
	if sum != e.total {
		t.Fatalf("sum of totals %d != seeded %d", sum, e.total)
	}
	// held must equal the sum of open remainders, per payer
	for _, h := range hs {
		items, _ := listAuthz(t, e.tok[h], "?direction=outgoing&limit=200")
		var open int64
		for _, it := range items {
			if it["status"] == "open" {
				open += num(t, it, "remaining_amount")
			}
			if num(t, it, "captured_amount") > num(t, it, "amount") {
				t.Fatalf("over-captured: %v", it)
			}
		}
		if open != held(t, e.tok[h]) {
			t.Fatalf("%s held %d != sum of open remainders %d", h, held(t, e.tok[h]), open)
		}
	}
}

// ---------- R195 export/import of authorizations ----------

func TestR195_ExportImportAuthorizations(t *testing.T) {
	e := setupAZ(t, 3600, []any{seedAZ("a_seed", "ada", "bob", 150, "open", isoIn(3*time.Hour))}, azUsers...)
	type rec struct {
		path, tok, key string
		body           any
		orig           resp
	}
	var recs []rec
	do := func(path, tok string, body any, want int) resp {
		k := newKey()
		r := post(t, path, tok, k, body)
		expect(t, r, want)
		recs = append(recs, rec{path, tok, k, body, r})
		return r
	}
	_ = do("/authorizations", e.tok["ada"], map[string]any{"to_handle": "bob", "amount": 2000, "note": "keep", "visibility": "private"}, 201)
	part := do("/authorizations", e.tok["ada"], map[string]any{"to_handle": "cy", "amount": 1000}, 201)
	do("/authorizations/"+aid(t, part.obj(t))+"/capture", e.tok["cy"], map[string]any{"amount": 300, "final": false}, 201)
	done := do("/authorizations", e.tok["ada"], map[string]any{"to_handle": "bob", "amount": 500}, 201)
	do("/authorizations/"+aid(t, done.obj(t))+"/capture", e.tok["bob"], map[string]any{}, 201)
	vd := do("/authorizations", e.tok["bob"], map[string]any{"to_handle": "ada", "amount": 100}, 201)
	expect(t, voidA(t, e.tok["bob"], aid(t, vd.obj(t))), 200)
	// a failed key stays reusable
	failKey := newKey()
	failBody := map[string]any{"to_handle": "ada", "amount": 99999999}
	expectErr(t, post(t, "/authorizations", e.tok["dee"], failKey, failBody), 409, "insufficient_funds")

	toks := map[string]string{}
	for k, v := range e.tok {
		toks[k] = v
	}
	snap := func() map[string]any {
		out := map[string]any{}
		for h, tk := range toks {
			me := get(t, "/me", tk).obj(t)
			as, _ := listAuthz(t, tk, "?limit=200")
			act, _ := activity(t, tk, "?limit=200")
			out[h] = map[string]any{"me": me, "authz": as, "activity": act}
		}
		return out
	}
	before := snap()
	exp := doExport(t)

	reset(t, fixtureAZ(nil, nil, fu{"xavier", 5}))
	expect(t, doImport(t, exp.Body), 204)
	if after := snap(); !reflect.DeepEqual(before, after) {
		t.Fatalf("authorization state differs after import.\nbefore=%v\nafter=%v", before, after)
	}
	// R195 + R110: retries return the original responses
	for i, r := range recs {
		rep := post(t, r.path, r.tok, r.key, r.body)
		if rep.Status != 200 {
			t.Fatalf("replay %d %s after import: %s", i, r.path, rep)
		}
		requireSameJSON(t, r.orig, rep)
	}
	if !reflect.DeepEqual(before, snap()) {
		t.Fatal("replays changed the state")
	}
	// the failed key is reusable
	mustPay(t, e.tok["ada"], "dee", 100, nil)
	expect(t, post(t, "/authorizations", e.tok["dee"], failKey, map[string]any{"to_handle": "ada", "amount": 50}), 201)
	// the open hold survives and is usable: partial capture continues, void releases only the remainder
	pid := aid(t, part.obj(t))
	p := mustCapture(t, e.tok["cy"], pid, map[string]any{"amount": 200, "final": false})
	if p["authorization_id"] != pid {
		t.Fatal("capture after import")
	}
	g := getAuthz(t, e.tok["ada"], pid)
	if num(t, g, "captured_amount") != 500 || len(ints(g["payment_ids"])) != 2 {
		t.Fatalf("records after import: %v", g)
	}
	expect(t, voidA(t, e.tok["ada"], pid), 200)
	// the ttl survives: a new authorization lives 3600 s
	n := mustAuthorize(t, e.tok["ada"], "bob", 10, nil)
	if d := checkTS(t, str(t, n, "expires_at")).Sub(checkTS(t, str(t, n, "created_at"))); d != time.Hour {
		t.Fatalf("ttl after import: %v", d)
	}
	// new ids do not collide
	seen := map[string]bool{}
	for _, tk := range toks {
		as, _ := listAuthz(t, tk, "?limit=200")
		for _, a := range as {
			seen[aid(t, a)] = true
		}
	}
	if !seen[aid(t, n)] || !seen["a_seed"] {
		t.Fatal("ids")
	}
	var sum int64
	for _, tk := range toks {
		sum += total(t, tk)
	}
	if sum != e.total {
		t.Fatalf("sum %d != %d", sum, e.total)
	}
	// import is replacement: re-import restores the earlier state
	expect(t, doImport(t, exp.Body), 204)
	if !reflect.DeepEqual(before, snap()) {
		t.Fatal("re-import did not restore")
	}
	expect(t, doImport(t, exp.Body), 204)
	if !reflect.DeepEqual(before, snap()) {
		t.Fatal("repeated import duplicated something")
	}
	// reset clears imported authorizations and their idempotency records
	reset(t, fixtureAZ(nil, nil, azUsers...))
	ta := login(t, "ada@example.com")
	if o, _ := listAuthz(t, ta, ""); len(o) != 0 {
		t.Fatal("authorizations survived reset")
	}
	r0 := recs[0]
	expect(t, post(t, r0.path, ta, r0.key, r0.body), 201)
}

func TestR195_ClockExpiryAcrossImport(t *testing.T) {
	e := setupAZ(t, 2, nil, azUsers...)
	id := aid(t, mustAuthorize(t, e.tok["ada"], "bob", 500, nil))
	exp := doExport(t)
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	expect(t, doImport(t, exp.Body), 204)
	if held(t, e.tok["ada"]) != 500 {
		t.Fatalf("hold lost by import")
	}
	time.Sleep(3 * time.Second)
	if held(t, e.tok["ada"]) != 0 || getAuthz(t, e.tok["ada"], id)["status"] != "expired" {
		t.Fatal("imported hold did not expire")
	}
}

// ---------- R160: a stage-1 export imports into stage 2 ----------

type s1Meta struct {
	Tokens map[string]string `json:"tokens"`
	Pay    struct {
		Key      string         `json:"key"`
		Body     map[string]any `json:"body"`
		Response map[string]any `json:"response"`
	} `json:"pay"`
	Settlement struct {
		Key      string         `json:"key"`
		Body     map[string]any `json:"body"`
		Response map[string]any `json:"response"`
	} `json:"settlement"`
	Failed struct {
		Key  string         `json:"key"`
		Body map[string]any `json:"body"`
	} `json:"failed"`
}

func TestR160_R161_Stage1ExportImports(t *testing.T) {
	raw, err := os.ReadFile("testdata/stage1_export.json")
	if err != nil {
		t.Fatal(err)
	}
	mraw, _ := os.ReadFile("testdata/stage1_meta.json")
	var meta s1Meta
	if err := json.Unmarshal(mraw, &meta); err != nil {
		t.Fatal(err)
	}
	var ex map[string]any
	if err := json.Unmarshal(raw, &ex); err != nil || ex["track"] != "pocketful" {
		t.Fatalf("bad fixture file: %v", err)
	}
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	r := doImport(t, raw)
	expect(t, r, 204)
	// tokens issued by the stage-1 service still work (R161)
	ada := meObj(t, meta.Tokens["ada"])
	if ada["handle"] != "ada" || num(t, ada, "total") != num(t, ada, "balance") || num(t, ada, "available") != num(t, ada, "total") || num(t, ada, "held") != 0 {
		t.Fatalf("defaults for stage-2 fields: %v", ada)
	}
	if num(t, ada, "balance") != 10000-100-50 {
		t.Fatalf("balance not preserved: %v", ada)
	}
	for h, tk := range meta.Tokens {
		m := meObj(t, tk)
		if m["handle"] != h {
			t.Fatalf("token of %s", h)
		}
	}
	// no authorizations, ttl 600
	if o, _ := listAuthz(t, meta.Tokens["ada"], ""); len(o) != 0 {
		t.Fatal("imported authorizations")
	}
	a := mustAuthorize(t, meta.Tokens["ada"], "bob", 100, nil)
	if d := checkTS(t, str(t, a, "expires_at")).Sub(checkTS(t, str(t, a, "created_at"))); d != 600*time.Second {
		t.Fatalf("default ttl after stage-1 import: %v", d)
	}
	if held(t, meta.Tokens["ada"]) != 100 {
		t.Fatal("hold")
	}
	expect(t, voidA(t, meta.Tokens["ada"], aid(t, a)), 200)
	// logins
	expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": pw}), 200)
	// the lost-response payment is retryable with the same key and body: 200 with the original body
	rep := post(t, "/payments", meta.Tokens["ada"], meta.Pay.Key, meta.Pay.Body)
	expect(t, rep, 200)
	orig, _ := json.Marshal(meta.Pay.Response)
	if !sameJSON(orig, rep.Body) {
		// the stage-2 payment shape adds authorization_id; every stage-1 field must be unchanged
		got := rep.obj(t)
		for k, v := range meta.Pay.Response {
			gv, _ := json.Marshal(got[k])
			wv, _ := json.Marshal(v)
			if string(gv) != string(wv) {
				t.Fatalf("replayed field %s: %s != %s", k, gv, wv)
			}
		}
	}
	if meObj(t, meta.Tokens["ada"])["balance"] == nil || total(t, meta.Tokens["ada"]) != 10000-100-50 {
		t.Fatal("replay moved money")
	}
	// the settlement retry
	rs := post(t, "/settlements", meta.Tokens["op"], meta.Settlement.Key, meta.Settlement.Body)
	expect(t, rs, 200)
	if rs.obj(t)["settlement_id"] != meta.Settlement.Response["settlement_id"] {
		t.Fatal("settlement replay")
	}
	// the failed key is reusable
	expect(t, post(t, "/payments", meta.Tokens["cy"], meta.Failed.Key, map[string]any{"to_handle": "ada", "amount": 10}), 201)
	// the pending seeded request is payable
	out, _ := listReq(t, meta.Tokens["ada"], "?status=pending")
	if len(out) != 1 || out[0]["request_id"] != "rq_1" {
		t.Fatalf("pending request: %v", out)
	}
	pr := postK(t, "/requests/rq_1/pay", meta.Tokens["ada"], map[string]any{})
	expect(t, pr, 201)
	if !isNull(pr.obj(t), "authorization_id") {
		t.Fatal("authorization_id on request payment")
	}
	// the seeded private payment stays private
	act, _ := activity(t, meta.Tokens["op"], "?limit=200")
	for _, it := range act {
		if it["payment_id"] == "p_seed" {
			t.Fatal("private seeded payment visible to a third party")
		}
	}
	// new ids do not collide with imported ones
	ids := map[string]bool{}
	for _, tk := range meta.Tokens {
		as, _ := activity(t, tk, "?limit=200")
		for _, p := range as {
			ids[str(t, p, "payment_id")] = true
		}
	}
	np := mustPay(t, meta.Tokens["ada"], "bob", 1, nil)
	if ids[str(t, np, "payment_id")] {
		t.Fatal("payment id collision after stage-1 import")
	}
	// the whole money supply is unchanged
	var sum int64
	for _, tk := range meta.Tokens {
		sum += total(t, tk)
	}
	if sum != 13000 {
		t.Fatalf("sum %d", sum)
	}
}
