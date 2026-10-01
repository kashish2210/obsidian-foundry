from conftest import *

# ---------------------------------------------------------------- R147-R149 requests screen


def _seed_requests():
    reqs = [
        {"id": "rq_in_pending", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 1200, "note": "taxi", "status": "pending"},
        {"id": "rq_in_paid", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 300, "note": "", "status": "paid"},
        {"id": "rq_in_declined", "requester_id": "u_cy", "payer_id": "u_ada", "amount": 400, "note": "", "status": "declined"},
        {"id": "rq_in_cancelled", "requester_id": "u_cy", "payer_id": "u_ada", "amount": 500, "note": "", "status": "cancelled"},
        {"id": "rq_out_pending", "requester_id": "u_ada", "payer_id": "u_cy", "amount": 250, "note": "", "status": "pending"},
        {"id": "rq_out_paid", "requester_id": "u_ada", "payer_id": "u_bob", "amount": 10, "note": "", "status": "paid"},
        {"id": "rq_out_declined", "requester_id": "u_ada", "payer_id": "u_dee", "amount": 20, "note": "", "status": "declined"},
        {"id": "rq_out_cancelled", "requester_id": "u_ada", "payer_id": "u_dee", "amount": 30, "note": "", "status": "cancelled"},
        {"id": "rq_other", "requester_id": "u_bob", "payer_id": "u_cy", "amount": 1, "note": "", "status": "pending"},
    ]
    reset(fixture(requests=reqs))


def test_R147_R148_lists_statuses_and_buttons(page):
    _seed_requests()
    ui_login(page, "ada", then="/requests")
    expect(T(page, "incoming-list")).to_be_visible()
    expect(T(page, "outgoing-list")).to_be_visible()
    status = {"rq_in_pending": "pending", "rq_in_paid": "paid", "rq_in_declined": "declined", "rq_in_cancelled": "cancelled",
              "rq_out_pending": "pending", "rq_out_paid": "paid", "rq_out_declined": "declined", "rq_out_cancelled": "cancelled"}
    amounts = {"rq_in_pending": 1200, "rq_in_paid": 300, "rq_out_pending": 250, "rq_out_paid": 10}
    for rid, st in status.items():
        inc = rid.startswith("rq_in")
        container = "incoming-list" if inc else "outgoing-list"
        item = page.get_by_test_id(container).get_by_test_id("request-item-" + rid)
        expect(item).to_have_attribute("data-status", st)
        if rid in amounts:
            assert text_of(page, "request-amount-" + rid) == fmt(amounts[rid])
        pend = st == "pending"
        assert T(page, "request-pay-" + rid).count() == (1 if inc and pend else 0), rid
        assert T(page, "request-decline-" + rid).count() == (1 if inc and pend else 0), rid
        assert T(page, "request-cancel-" + rid).count() == (1 if (not inc) and pend else 0), rid
    assert T(page, "request-item-rq_other").count() == 0  # not a party
    expect(T(page, "empty-requests")).to_have_count(0)


def test_R149_empty_requests(page):
    reset()
    ui_login(page, "dee", then="/requests")
    expect(T(page, "empty-requests")).to_be_visible()
    expect(T(page, "request-error")).to_have_count(0)


def test_R147_R149_pay_decline_cancel_actions(page):
    _seed_requests()
    ui_login(page, "ada", then="/requests")
    T(page, "request-pay-rq_in_pending").click()
    expect(T(page, "request-item-rq_in_pending")).to_have_attribute("data-status", "paid")
    expect(T(page, "request-pay-rq_in_pending")).to_have_count(0)
    expect(T(page, "request-decline-rq_in_pending")).to_have_count(0)
    assert me(token("ada"))["balance"] == 10000 - 1200 and me(token("bob"))["balance"] == 2500 + 1200
    T(page, "request-cancel-rq_out_pending").click()
    expect(T(page, "request-item-rq_out_pending")).to_have_attribute("data-status", "cancelled")
    expect(T(page, "request-cancel-rq_out_pending")).to_have_count(0)
    # decline
    reset(fixture(requests=[{"id": "rq_d", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 77, "note": "", "status": "pending"}]))
    ui_login(page, "ada", then="/requests")
    T(page, "request-decline-rq_d").click()
    expect(T(page, "request-item-rq_d")).to_have_attribute("data-status", "declined")
    expect(T(page, "request-pay-rq_d")).to_have_count(0)
    assert me(token("ada"))["balance"] == 10000


def test_R149_request_error_when_payer_is_short(page):
    reset(fixture(requests=[{"id": "rq_big", "requester_id": "u_ada", "payer_id": "u_cy", "amount": 1200, "note": "", "status": "pending"}]))
    ui_login(page, "cy", then="/requests")
    expect(T(page, "request-error")).to_have_count(0)
    T(page, "request-pay-rq_big").click()
    expect(T(page, "request-error")).to_be_visible()
    assert text_of(page, "request-error").strip() != ""
    expect(T(page, "request-item-rq_big")).to_have_attribute("data-status", "pending")
    assert me(token("cy"))["balance"] == 500


def test_R156_request_cancelled_elsewhere(page):
    reset(fixture(requests=[{"id": "rq_x", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 100, "note": "", "status": "pending"}]))
    ui_login(page, "ada", then="/requests")
    expect(T(page, "request-pay-rq_x")).to_be_visible()
    s, b, _ = api("POST", "/requests/rq_x/cancel", token=token("bob"))
    assert s == 200, b
    T(page, "request-pay-rq_x").click()  # the stale button is still on screen
    expect(T(page, "request-error")).to_be_visible()
    expect(T(page, "request-item-rq_x")).to_have_attribute("data-status", "cancelled")
    expect(T(page, "request-pay-rq_x")).to_have_count(0)
    expect(T(page, "request-decline-rq_x")).to_have_count(0)
    assert me(token("ada"))["balance"] == 10000


def test_R156_decline_and_cancel_refused_elsewhere(page):
    reset(fixture(requests=[
        {"id": "rq_a", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 100, "note": "", "status": "pending"},
        {"id": "rq_b", "requester_id": "u_ada", "payer_id": "u_bob", "amount": 100, "note": "", "status": "pending"}]))
    ui_login(page, "ada", then="/requests")
    expect(T(page, "request-decline-rq_a")).to_be_visible()
    expect(T(page, "request-cancel-rq_b")).to_be_visible()
    api("POST", "/requests/rq_a/cancel", token=token("bob"))        # requester cancels
    api("POST", "/requests/rq_b/cancel", token=token("ada"))        # cancelled elsewhere by the same user
    api("POST", "/requests/rq_b/cancel", token=token("ada"))
    T(page, "request-decline-rq_a").click()
    expect(T(page, "request-error")).to_be_visible()
    expect(T(page, "request-item-rq_a")).to_have_attribute("data-status", "cancelled")
    expect(T(page, "request-decline-rq_a")).to_have_count(0)


# ---------------------------------------------------------------- R150/R151 split


def _preview(page, amount, handles):
    T(page, "split-amount").fill(amount)
    T(page, "split-handles").fill(handles)


@pytest.mark.parametrize("amount,handles,minor", [
    ("10.00", "ada,bob,cy", 1000),
    ("0.01", "ada,bob,cy", 1),
    ("9.99", " ada , bob,cy ", 999),
    ("0.05", "ada,bob,cy,dee,op", 5),
    ("10.01", "cy,bob,ada", 1001),
])
def test_R151_split_preview_matches_rule_and_server(page, amount, handles, minor):
    reset()
    ui_login(page, "ada", then="/split")
    seen = record_posts(page, "/splits")
    _preview(page, amount, handles)
    hs = [h.strip() for h in handles.split(",")]
    want = shares(minor, len(hs))
    expect(T(page, "split-preview")).to_be_visible()
    for h, w in zip(hs, want):
        expect(T(page, "split-share-" + h)).to_have_text(fmt(w))
    assert page.locator('[data-testid^="split-share-"]').count() == len(hs)
    page.wait_for_timeout(300)
    assert seen == [], "the preview must not post anything"
    # submitted shares are identical to the preview
    T(page, "split-note").fill("dinner")
    T(page, "split-submit").click()
    expect(T(page, "split-error")).to_have_count(0)
    page.wait_for_timeout(700)
    assert len(seen) == 1 and seen[0]["body"]["amount"] == minor
    assert seen[0]["body"]["participant_handles"] == hs
    out = api("GET", "/requests?direction=outgoing&limit=200", token=token("ada"))[1]["requests"]
    by = {r["payer_handle"]: r["amount"] for r in out}
    for h, w in zip(hs, want):
        if h != "ada":
            assert by[h] == w, (h, by)
    assert len(out) == len([h for h in hs if h != "ada"])


def test_R151_split_preview_updates_and_formats(page):
    reset(fixture([("ada", 10 ** 6), ("bob", 0), ("cy", 0)], cur="JPY", mu=0))
    ui_login(page, "ada", then="/split")
    _preview(page, "1000", "ada,bob,cy")
    for h, w in zip(["ada", "bob", "cy"], [334, 333, 333]):
        expect(T(page, "split-share-" + h)).to_have_text("%d JPY" % w)
    T(page, "split-handles").fill("cy,bob,ada")
    for h, w in zip(["cy", "bob", "ada"], [334, 333, 333]):
        expect(T(page, "split-share-" + h)).to_have_text("%d JPY" % w)
    T(page, "split-amount").fill("10")
    expect(T(page, "split-share-cy")).to_have_text("4 JPY")


def test_R150_split_errors_send_nothing_or_show_error(page):
    reset()
    ui_login(page, "ada", then="/split")
    expect(T(page, "split-error")).to_have_count(0)
    seen = record_posts(page, "/splits")
    for amount, handles in [("15.005", "bob,cy"), ("abc", "bob"), ("", "bob")]:
        _preview(page, amount, handles)
        T(page, "split-submit").click()
        expect(T(page, "split-error")).to_be_visible()
    page.wait_for_timeout(300)
    assert seen == []
    for amount, handles in [("10.00", "bob,ghost"), ("10.00", "bob,bob"), ("10.00", "")]:
        _preview(page, amount, handles)
        T(page, "split-submit").click()
        expect(T(page, "split-error")).to_be_visible()
    assert api("GET", "/requests?direction=outgoing", token=token("ada"))[1]["requests"] == []


def test_R150_split_only_caller_creates_no_requests(page):
    reset()
    ui_login(page, "ada", then="/split")
    _preview(page, "10.00", "ada")
    expect(T(page, "split-share-ada")).to_have_text("10.00 EUR")
    T(page, "split-submit").click()
    expect(T(page, "split-error")).to_have_count(0)
    page.wait_for_timeout(500)
    assert api("GET", "/requests", token=token("ada"))[1]["requests"] == []
