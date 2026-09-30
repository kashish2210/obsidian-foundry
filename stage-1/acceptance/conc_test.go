package acceptance

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func concUsers() []fu {
	return []fu{{"ada", 3000}, {"bob", 2000}, {"cy", 1000}, {"dee", 500}, {"eve", 0}, {"fay", 250}}
}

func handlesOf(us []fu) []string {
	var h []string
	for _, u := range us {
		h = append(h, u.h)
	}
	return h
}

// R1, R2, R50: 50 clients doing random payments; invariants over the final state.
func TestR1_R2_R50_ConcurrentPaymentsInvariants(t *testing.T) {
	us := concUsers()
	e := setupUsers(t, nil, us...)
	hs := handlesOf(us)
	expected := map[string]int64{}
	for _, u := range us {
		expected[u.h] = u.bal
	}
	var mu sync.Mutex
	var bad []string
	var created int64
	stop := make(chan struct{})

	// pollers watch for transient negative balances
	var pw sync.WaitGroup
	for i := 0; i < 4; i++ {
		pw.Add(1)
		go func(i int) {
			defer pw.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h := hs[rand.Intn(len(hs))]
				r, err := send("GET", "/me", e.tok[h], nil, nil)
				if err != nil {
					mu.Lock()
					bad = append(bad, err.Error())
					mu.Unlock()
					return
				}
				if r.Status == 200 {
					v, _ := decodeNum(r.Body)
					if n, _ := v.(map[string]any)["balance"].(interface{ Int64() (int64, error) }); n != nil {
						if b, _ := n.Int64(); b < 0 {
							mu.Lock()
							bad = append(bad, fmt.Sprintf("negative balance observed for %s: %d", h, b))
							mu.Unlock()
						}
					}
				}
			}
		}(i)
	}

	var wg sync.WaitGroup
	for w := 0; w < 50; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 1))
			for i := 0; i < 12; i++ {
				from := hs[rng.Intn(len(hs))]
				to := hs[rng.Intn(len(hs))]
				if to == from {
					continue
				}
				amt := int64(rng.Intn(900) + 1)
				r, err := send("POST", "/payments", e.tok[from], map[string]string{"Idempotency-Key": newKey()},
					map[string]any{"to_handle": to, "amount": amt})
				mu.Lock()
				switch {
				case err != nil:
					bad = append(bad, err.Error())
				case r.Status == 201:
					expected[from] -= amt
					expected[to] += amt
					atomic.AddInt64(&created, 1)
				case r.Status == 409:
				default:
					bad = append(bad, r.String())
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	pw.Wait()
	for _, b := range bad {
		t.Error(b)
	}
	if created == 0 {
		t.Fatal("no payment succeeded; test is vacuous")
	}
	var sum int64
	for _, u := range us {
		b := bal(t, e.tok[u.h])
		if b < 0 {
			t.Fatalf("%s negative: %d", u.h, b)
		}
		if b != expected[u.h] {
			t.Fatalf("%s balance %d, expected %d from accepted payments", u.h, b, expected[u.h])
		}
		sum += b
	}
	if sum != e.total {
		t.Fatalf("sum %d != seeded %d", sum, e.total)
	}
}

func TestR2_ConflictingSpendsOfOneWallet(t *testing.T) {
	for _, amt := range []int64{100, 250, 600, 1000} {
		us := []fu{{"ada", 1000}, {"bob", 0}, {"cy", 0}, {"dee", 0}}
		e := setupUsers(t, nil, us...)
		targets := []string{"bob", "cy", "dee"}
		rs := fanout(50, func(i int) (resp, error) {
			return send("POST", "/payments", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()},
				map[string]any{"to_handle": targets[i%3], "amount": amt})
		})
		ok := 0
		for _, x := range rs {
			if x.err != nil {
				t.Fatal(x.err)
			}
			switch x.r.Status {
			case 201:
				ok++
			case 409:
			default:
				t.Fatalf("amount %d: %s", amt, x.r)
			}
		}
		if int64(ok) != 1000/amt {
			t.Fatalf("amount %d: %d payments succeeded, want %d", amt, ok, 1000/amt)
		}
		if bal(t, e.tok["ada"]) != 1000-int64(ok)*amt || e.sum(t) != e.total {
			t.Fatalf("amount %d: final state inconsistent", amt)
		}
	}
}

func TestR2_OppositeDirectionsNoDeadlock(t *testing.T) {
	e := setupUsers(t, nil, fu{"ada", 5000}, fu{"bob", 5000})
	var okA, okB int64
	rs := fanout(50, func(i int) (resp, error) {
		from, to := "ada", "bob"
		if i%2 == 1 {
			from, to = "bob", "ada"
		}
		r, err := send("POST", "/payments", e.tok[from], map[string]string{"Idempotency-Key": newKey()},
			map[string]any{"to_handle": to, "amount": 100 + i})
		if err == nil && r.Status == 201 {
			if i%2 == 0 {
				atomic.AddInt64(&okA, int64(100+i))
			} else {
				atomic.AddInt64(&okB, int64(100+i))
			}
		}
		return r, err
	})
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		if x.r.Status != 201 {
			t.Fatalf("%s", x.r)
		}
	}
	if bal(t, e.tok["ada"]) != 5000-okA+okB || bal(t, e.tok["bob"]) != 5000+okA-okB {
		t.Fatal("balances inconsistent after crossing payments")
	}
}

// R3: a request moves money at most once.
func TestR3_ConcurrentPayOfOneRequestDistinctKeys(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 1000))
	rs := fanout(50, func(int) (resp, error) {
		return send("POST", "/requests/"+id+"/pay", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
	})
	n201, n409 := 0, 0
	var pids = map[string]bool{}
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			n201++
			pids[str(t, x.r.obj(t), "payment_id")] = true
		case 409:
			expectErr(t, x.r, 409, "request_not_pending")
			n409++
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if n201 != 1 || n409 != 49 {
		t.Fatalf("201s=%d 409s=%d", n201, n409)
	}
	if bal(t, e.tok["ada"]) != 9000 || bal(t, e.tok["bob"]) != 3500 {
		t.Fatal("money moved more than once")
	}
	a, _ := activity(t, e.tok["bob"], "")
	if len(a) != 1 {
		t.Fatalf("%d payments", len(a))
	}
}

func TestR3_PayVsDeclineVsCancelRace(t *testing.T) {
	for round := 0; round < 5; round++ {
		e := setup(t)
		id := rid(t, mustRequest(t, e.tok["bob"], "ada", 400))
		var paid int32
		rs := fanout(30, func(i int) (resp, error) {
			switch i % 3 {
			case 0:
				r, err := send("POST", "/requests/"+id+"/pay", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
				if err == nil && r.Status == 201 {
					atomic.AddInt32(&paid, 1)
				}
				return r, err
			case 1:
				return send("POST", "/requests/"+id+"/decline", e.tok["ada"], nil, nil)
			default:
				return send("POST", "/requests/"+id+"/cancel", e.tok["bob"], nil, nil)
			}
		})
		for _, x := range rs {
			if x.err != nil {
				t.Fatal(x.err)
			}
			if x.r.Status >= 500 {
				t.Fatalf("5xx: %s", x.r)
			}
		}
		out, _ := listReq(t, e.tok["ada"], "")
		if len(out) != 1 {
			t.Fatal("request count")
		}
		st := out[0]["status"]
		moved := bal(t, e.tok["ada"]) != 10000
		if paid > 1 {
			t.Fatalf("paid %d times", paid)
		}
		if (st == "paid") != moved || (st == "paid") != (paid == 1) {
			t.Fatalf("status %v inconsistent with money moved=%v paid=%d", st, moved, paid)
		}
		if moved && (bal(t, e.tok["ada"]) != 9600 || bal(t, e.tok["bob"]) != 2900) {
			t.Fatal("wrong amount moved")
		}
		// the terminal state is stable: a further pay fails
		expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["ada"], map[string]any{}), 409, func() string {
			if st == "paid" || st == "declined" || st == "cancelled" {
				return "request_not_pending"
			}
			return ""
		}())
	}
}

func TestR3_R2_ManyRequestsOnePayerLimitedFunds(t *testing.T) {
	e := setupUsers(t, nil, fu{"ada", 0}, fu{"bob", 0}, fu{"cy", 1000}, fu{"dee", 0})
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, rid(t, mustRequest(t, e.tok["bob"], "cy", 300)))
	}
	rs := fanout(20, func(i int) (resp, error) {
		return send("POST", "/requests/"+ids[i]+"/pay", e.tok["cy"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
	})
	ok := 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			ok++
		case 409:
			expectErr(t, x.r, 409, "insufficient_funds")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if ok != 3 {
		t.Fatalf("%d pays succeeded, want exactly 3 (1000/300)", ok)
	}
	if bal(t, e.tok["cy"]) != 100 || bal(t, e.tok["bob"]) != 900 {
		t.Fatal("balances")
	}
	paid, _ := listReq(t, e.tok["bob"], "?status=paid&limit=200")
	pend, _ := listReq(t, e.tok["bob"], "?status=pending&limit=200")
	if len(paid) != 3 || len(pend) != 17 {
		t.Fatalf("paid=%d pending=%d", len(paid), len(pend))
	}
}

func TestR1_R50_MixedLoad(t *testing.T) {
	us := concUsers()
	e := setupUsers(t, nil, us...)
	hs := handlesOf(us)
	var bad atomic.Value
	var wg sync.WaitGroup
	for w := 0; w < 50; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) * 7))
			for i := 0; i < 8; i++ {
				me := hs[rng.Intn(len(hs))]
				other := hs[rng.Intn(len(hs))]
				var r resp
				var err error
				switch rng.Intn(6) {
				case 0, 1:
					r, err = send("POST", "/payments", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"to_handle": other, "amount": rng.Intn(400) + 1})
				case 2:
					r, err = send("POST", "/requests", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"payer_handle": other, "amount": rng.Intn(400) + 1})
				case 3:
					r, err = send("POST", "/splits", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"amount": rng.Intn(400) + 1, "participant_handles": []string{me, other}})
				case 4:
					r, err = send("GET", "/requests?direction=incoming", e.tok[me], nil, nil)
					if err == nil && r.Status == 200 {
						v, _ := decodeNum(r.Body)
						if arr, ok := v.(map[string]any)["requests"].([]any); ok && len(arr) > 0 {
							q := arr[rng.Intn(len(arr))].(map[string]any)
							r, err = send("POST", "/requests/"+q["request_id"].(string)+"/pay", e.tok[me], map[string]string{"Idempotency-Key": newKey()}, map[string]any{})
						}
					}
				default:
					r, err = send("GET", "/activity", e.tok[me], nil, nil)
				}
				if err != nil {
					bad.Store(err.Error())
				} else if r.Status >= 500 {
					bad.Store(r.String())
				}
			}
		}(w)
	}
	wg.Wait()
	if v := bad.Load(); v != nil {
		t.Fatalf("%v", v)
	}
	if e.sum(t) != e.total {
		t.Fatalf("sum changed under mixed load")
	}
	for _, h := range hs {
		if bal(t, e.tok[h]) < 0 {
			t.Fatalf("%s negative", h)
		}
	}
}

func TestR8_FiftyInFlightWithinDeadline(t *testing.T) {
	e := setup(t)
	start := time.Now()
	rs := fanout(50, func(i int) (resp, error) {
		return send("GET", "/me", e.tok["ada"], nil, nil)
	})
	for _, x := range rs {
		if x.err != nil || x.r.Status != 200 {
			t.Fatalf("%v %s", x.err, x.r)
		}
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("50 concurrent requests took longer than 5 s")
	}
}

func TestR51_R53_R57_ConcurrentSignups(t *testing.T) {
	setup(t)
	// same email
	rs := fanout(20, func(int) (resp, error) {
		return send("POST", "/auth/signup", "", nil, map[string]any{"email": "race@example.com", "password": "longenough", "display_name": "R"})
	})
	n201, n409 := 0, 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			n201++
		case 409:
			expectErr(t, x.r, 409, "email_taken")
			n409++
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if n201 != 1 || n409 != 19 {
		t.Fatalf("same-email race: 201=%d 409=%d", n201, n409)
	}
	// same derived handle, different emails
	rs = fanout(20, func(i int) (resp, error) {
		return send("POST", "/auth/signup", "", nil, map[string]any{"email": fmt.Sprintf("dup.h@d%d.example.com", i), "password": "longenough", "display_name": "R"})
	})
	n201, n409 = 0, 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			n201++
		case 409:
			expectErr(t, x.r, 409, "handle_taken")
			n409++
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if n201 != 1 || n409 != 19 {
		t.Fatalf("same-handle race: 201=%d 409=%d", n201, n409)
	}
}

func TestR69_R1_ConcurrentSettlementsDistinctKeys(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 300}, fu{"bob", 0}, fu{"cy", 0}, fu{"op", 0})
	body := map[string]any{"transfers": []any{map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 100}}}
	rs := fanout(30, func(int) (resp, error) {
		return send("POST", "/settlements", e.tok["op"], map[string]string{"Idempotency-Key": newKey()}, body)
	})
	ok := 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			ok++
		case 409:
			expectErr(t, x.r, 409, "insufficient_funds")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if ok != 3 || bal(t, e.tok["ada"]) != 0 || bal(t, e.tok["bob"]) != 300 {
		t.Fatalf("ok=%d ada=%d bob=%d", ok, bal(t, e.tok["ada"]), bal(t, e.tok["bob"]))
	}
}
