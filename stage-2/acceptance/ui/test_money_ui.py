import re

from conftest import *


# ---------------------------------------------------------------- R137/R138 formatting
@pytest.mark.parametrize("cur,mu,bal,want", [
    ("EUR", 2, 10000, "100.00 EUR"), ("EUR", 2, 5, "0.05 EUR"), ("EUR", 2, 0, "0.00 EUR"),
    ("EUR", 2, 123456789, "1234567.89 EUR"),
    ("JPY", 0, 1200, "1200 JPY"), ("JPY", 0, 0, "0 JPY"), ("JPY", 0, 7, "7 JPY"),
    ("BHD", 3, 1500, "1.500 BHD"), ("BHD", 3, 7, "0.007 BHD"), ("BHD", 3, 12345678, "12345.678 BHD"),
])
def test_R137_R138_balance_format(page, cur, mu, bal, want):
    reset(fixture([("ada", bal), ("bob", 0)], cur=cur, mu=mu))
    ui_login(page, "ada")
    loc = T(page, "wallet-balance")
    expect(loc).to_have_text(want)
    assert loc.inner_text() == want
    assert loc.get_attribute("data-amount") == str(bal)
    pattern = r"[0-9]+(\.[0-9]{%d})? [A-Z]{3}" % mu if mu else r"[0-9]+ [A-Z]{3}"
    assert re.fullmatch(pattern, loc.inner_text())


@pytest.mark.parametrize("cur,mu", [("EUR", 2), ("JPY", 0), ("BHD", 3)])
def test_R138_format_everywhere(page, cur, mu):
    reset(fixture([("ada", 10 ** 6), ("bob", 10 ** 6), ("cy", 0)], cur=cur, mu=mu))
    ta, tb = token("ada"), token("bob")
    p = pay(ta, "bob", 1234)
    rq = request_money(tb, "ada", 4321)["request_id"]
    a = authorize(ta, "bob", 2345)
    ui_login(page, "ada")
    assert text_of(page, "activity-amount-" + p["payment_id"]) == fmt(1234, mu, cur)
    goto(page, "/requests")
    assert text_of(page, "request-amount-" + rq) == fmt(4321, mu, cur)
    goto(page, "/authorizations")
    assert text_of(page, "authorization-amount-" + a["authorization_id"]) == fmt(2345, mu, cur)
    goto(page, "/split")
    T(page, "split-amount").fill(fmt(1000, mu, cur).split(" ")[0])
    T(page, "split-handles").fill("ada,bob,cy")
    want = shares(1000, 3)
    for h, w in zip(["ada", "bob", "cy"], want):
        expect(T(page, "split-share-" + h)).to_have_text(fmt(w, mu, cur))


# ---------------------------------------------------------------- R139-R142 pay form
def test_R140_decimal_conversion_exact(page):
    cases = [("15.00", 1500), ("15", 1500), ("15.5", 1550), ("15.50", 1550), ("0.05", 5), ("0.1", 10), ("1234.56", 123456)]
    for text, minor in cases:
        reset(fixture([("ada", 10 ** 7), ("bob", 2500)]))
        ui_login(page, "ada")
        seen = record_posts(page, "/payments")
        fill_pay(page, "bob", text)
        T(page, "pay-submit").click()
        expect(T(page, "wallet-balance")).to_have_text(fmt(10 ** 7 - minor))
        assert [b["body"]["amount"] for b in seen] == [minor], (text, seen)
        assert me(token("bob"))["balance"] == 2500 + minor


@pytest.mark.parametrize("cur,mu,ok,okmin,bad", [
    ("JPY", 0, "15", 15, ["15.5", "15.0x"]),
    ("BHD", 3, "1.234", 1234, ["1.2345", "1.23456"]),
])
def test_R140_units_zero_and_three(page, cur, mu, ok, okmin, bad):
    for text in bad:
        reset(fixture([("ada", 100000), ("bob", 0)], cur=cur, mu=mu))
        ui_login(page, "ada")
        seen = record_posts(page, "/payments")
        fill_pay(page, "bob", text)
        T(page, "pay-submit").click()
        expect(T(page, "pay-error")).to_be_visible()
        page.wait_for_timeout(300)
        assert seen == []
        assert me(token("ada"))["balance"] == 100000
    reset(fixture([("ada", 100000), ("bob", 0)], cur=cur, mu=mu))
    ui_login(page, "ada")
    seen = record_posts(page, "/payments")
    fill_pay(page, "bob", ok)
    T(page, "pay-submit").click()
    expect(T(page, "wallet-balance")).to_have_text(fmt(100000 - okmin, mu, cur))
    assert seen[0]["body"]["amount"] == okmin


@pytest.mark.parametrize("text", ["15.005", "abc", "", "1,5", "--5", "1.2.3", "NaN", "0x10"])
def test_R140_rejected_inputs_send_nothing(page, text):
    reset()
    ui_login(page, "ada")
    seen = record_posts(page, "/payments")
    fill_pay(page, "bob", text)
    T(page, "pay-submit").click()
    expect(T(page, "pay-error")).to_be_visible()
    page.wait_for_timeout(300)
    assert seen == [], seen
    expect(T(page, "wallet-balance")).to_have_text("100.00 EUR")
    assert me(token("ada"))["balance"] == 10000


def test_R141_pay_error_on_refusal_and_absent_otherwise(page):
    reset()
    ui_login(page, "ada")
    expect(T(page, "pay-error")).to_have_count(0)
    fill_pay(page, "bob", "1000.00")  # insufficient
    T(page, "pay-submit").click()
    expect(T(page, "pay-error")).to_be_visible()
    assert text_of(page, "pay-error").strip() != ""
    expect(T(page, "wallet-balance")).to_have_text("100.00 EUR")
    fill_pay(page, "nobody", "1.00")  # unknown handle
    T(page, "pay-submit").click()
    expect(T(page, "pay-error")).to_be_visible()
    fill_pay(page, "ada", "1.00")  # self payment
    T(page, "pay-submit").click()
    expect(T(page, "pay-error")).to_be_visible()
    fill_pay(page, "bob", "1.00")
    T(page, "pay-submit").click()
    expect(T(page, "wallet-balance")).to_have_text("99.00 EUR")
    expect(T(page, "pay-error")).to_have_count(0)


def test_R142_unchanged_resubmit_pays_once(page):
    reset()
    ui_login(page, "ada")
    seen = record_posts(page, "/payments")
    fill_pay(page, "bob", "15.00", "lunch", "private")
    T(page, "pay-submit").click()
    expect(T(page, "wallet-balance")).to_have_text("85.00 EUR")
    # values are kept after success
    assert T(page, "pay-handle").input_value() == "bob"
    assert T(page, "pay-amount").input_value() in ("15.00", "15")
    assert T(page, "pay-note").input_value() == "lunch"
    assert T(page, "pay-visibility").input_value() == "private"
    # resubmit unchanged, several times: no second payment
    for _ in range(3):
        T(page, "pay-submit").click()
        page.wait_for_timeout(400)
    expect(T(page, "wallet-balance")).to_have_text("85.00 EUR")
    expect(T(page, "pay-error")).to_have_count(0)
    assert page.locator('[data-testid^="activity-item-"]').count() == 1
    assert me(token("ada"))["balance"] == 8500 and me(token("bob"))["balance"] == 4000
    # changing any field makes the next submission a new payment with a new key
    T(page, "pay-note").fill("lunch 2")
    T(page, "pay-submit").click()
    expect(T(page, "wallet-balance")).to_have_text("70.00 EUR")
    assert page.locator('[data-testid^="activity-item-"]').count() == 2
    keys = [s["key"] for s in seen]
    assert len(keys) >= 2 and keys[0] != keys[-1]
    T(page, "pay-visibility").select_option("public")
    T(page, "pay-submit").click()
    expect(T(page, "wallet-balance")).to_have_text("55.00 EUR")
    assert me(token("bob"))["balance"] == 2500 + 4500


# ---------------------------------------------------------------- R143 request form
def test_R143_request_form(page):
    reset()
    ui_login(page, "bob")
    expect(T(page, "request-error")).to_have_count(0)
    T(page, "request-handle").fill("ada")
    T(page, "request-amount").fill("12.00")
    T(page, "request-note").fill("taxi")
    T(page, "request-submit").click()
    expect(T(page, "request-error")).to_have_count(0)
    page.wait_for_timeout(500)
    out = api("GET", "/requests?direction=outgoing", token=token("bob"))[1]["requests"]
    assert len(out) == 1 and out[0]["amount"] == 1200 and out[0]["note"] == "taxi" and out[0]["payer_handle"] == "ada"
    # refusals show request-error
    for handle, amount in [("ghost", "1.00"), ("bob", "1.00")]:
        T(page, "request-handle").fill(handle)
        T(page, "request-amount").fill(amount)
        T(page, "request-submit").click()
        expect(T(page, "request-error")).to_be_visible()
    seen = record_posts(page, "/requests")
    for bad in ["15.005", "abc"]:
        T(page, "request-handle").fill("ada")
        T(page, "request-amount").fill(bad)
        T(page, "request-submit").click()
        expect(T(page, "request-error")).to_be_visible()
    page.wait_for_timeout(300)
    assert seen == []
    assert len(api("GET", "/requests?direction=outgoing", token=token("bob"))[1]["requests"]) == 1


# ---------------------------------------------------------------- R144-R146 feed
def test_R144_R145_feed_items(page):
    reset()
    ta, tb = token("ada"), token("bob")
    p1 = pay(ta, "bob", 1000, note="first <b>bold</b> & \"q\" 🎉")
    time.sleep(1.1)
    p2 = pay(tb, "cy", 250, note="", visibility="private")
    time.sleep(1.1)
    p3 = pay(ta, "cy", 5, note="third", visibility="public")
    time.sleep(1.1)
    p4 = pay(tb, "ada", 75, note="é  two spaces")
    ui_login(page, "bob")
    ids = [x["payment_id"] for x in (p4, p3, p2, p1)]
    expect(T(page, "activity-list")).to_be_visible()
    order = page.eval_on_selector_all('[data-testid="activity-list"] [data-testid^="activity-item-"]',
                                      "els => els.map(e => e.getAttribute('data-testid').replace('activity-item-',''))")
    assert order == [i for i in ids if i in order]
    assert set(order) >= {p1["payment_id"], p2["payment_id"], p4["payment_id"]}
    for x in (p1, p2, p3, p4):
        pid = x["payment_id"]
        if pid not in order:
            continue
        item = T(page, "activity-item-" + pid)
        assert item.get_attribute("data-visibility") == x["visibility"]
        parties = text_of(page, "activity-parties-" + pid)
        assert x["from_handle"] in parties and x["to_handle"] in parties
        assert text_of(page, "activity-amount-" + pid) == fmt(x["amount"])
        assert T(page, "activity-note-" + pid).count() == 1
        assert T(page, "activity-note-" + pid).text_content() == x["note"], (pid, x["note"])
    # newest first across all four (bob sees p1, p2, p4 privately or publicly; p3 is public so visible too)
    assert order == ids, order
    expect(T(page, "empty-activity")).to_have_count(0)


def test_R144_feed_visibility_rule_in_ui(page, ctx):
    reset()
    ta = token("ada")
    pub = pay(ta, "bob", 10)["payment_id"]
    priv = pay(ta, "bob", 11, visibility="private")["payment_id"]
    ui_login(page, "dee")  # third party
    expect(T(page, "activity-item-" + pub)).to_be_visible()
    assert T(page, "activity-item-" + priv).count() == 0
    p2 = ctx.browser.new_context().new_page()
    ui_login(p2, "bob")
    expect(T(p2, "activity-item-" + priv)).to_be_visible()
    assert T(p2, "activity-item-" + priv).get_attribute("data-visibility") == "private"


def test_R146_empty_activity(page):
    reset()
    ui_login(page, "dee")
    expect(T(page, "empty-activity")).to_be_visible()
    assert page.locator('[data-testid^="activity-item-"]').count() == 0
    expect(T(page, "activity-list")).to_have_count(0)
    # the first payment replaces the empty state without a reload
    pay(token("ada"), "dee", 100)
    T(page, "wallet-refresh").click()
    expect(T(page, "empty-activity")).to_have_count(0)
    assert page.locator('[data-testid^="activity-item-"]').count() == 1


# ---------------------------------------------------------------- R152/R153 refresh
def test_R152_R159_state_refreshes_after_actions(page):
    reset(fixture(authorizations=[seed_az("a_1", "ada", "bob", 2000, "open", iso_in(7200))]))
    ui_login(page, "ada")
    expect(T(page, "wallet-available")).to_have_text("80.00 EUR")
    fill_pay(page, "cy", "5.00", "x")
    T(page, "pay-submit").click()
    expect(T(page, "wallet-balance")).to_have_text("95.00 EUR")
    expect(T(page, "wallet-available")).to_have_text("75.00 EUR")
    expect(T(page, "wallet-held")).to_have_text("20.00 EUR")
    assert page.locator('[data-testid^="activity-item-"]').count() == 1


def test_R153_wallet_refresh_keeps_pay_form(page):
    reset()
    ui_login(page, "ada")
    fill_pay(page, "cy", "3.21", "draft note", "private")
    pay(token("bob"), "ada", 500)
    expect(T(page, "wallet-balance")).to_have_text("100.00 EUR")  # no polling
    T(page, "wallet-refresh").click()
    expect(T(page, "wallet-balance")).to_have_text("105.00 EUR")
    assert page.locator('[data-testid^="activity-item-"]').count() == 1
    assert T(page, "pay-handle").input_value() == "cy"
    assert T(page, "pay-amount").input_value() == "3.21"
    assert T(page, "pay-note").input_value() == "draft note"
    assert T(page, "pay-visibility").input_value() == "private"


def _is_read(route):
    req = route.request
    path = req.url.split("?")[0].replace(BASE, "")
    return req.method == "GET" and path in ("/me", "/activity")


def test_R154_latest_refresh_wins(page):
    reset()
    ui_login(page, "ada")
    expect(T(page, "wallet-balance")).to_have_text("100.00 EUR")
    state = {"hold": True}
    held = []

    def handler(route):
        if state["hold"] and _is_read(route):
            held.append((route, route.fetch()))  # capture the OLD state now, deliver it later
        else:
            route.continue_()

    page.route("**/*", handler)
    T(page, "wallet-refresh").click()  # refresh A: its response is delayed
    page.wait_for_timeout(500)
    assert held, "the refresh did not issue a read"
    new = pay(token("bob"), "ada", 700)  # state changes after A read it
    state["hold"] = False
    T(page, "wallet-refresh").click()  # refresh B answers immediately with the new state
    expect(T(page, "wallet-balance")).to_have_text("107.00 EUR")
    expect(T(page, "activity-item-" + new["payment_id"])).to_be_visible()
    for route, resp in held:  # now the stale responses arrive
        route.fulfill(response=resp)
    page.wait_for_timeout(800)
    expect(T(page, "wallet-balance")).to_have_text("107.00 EUR")
    expect(T(page, "activity-item-" + new["payment_id"])).to_be_visible()
    assert T(page, "wallet-balance").get_attribute("data-amount") == "10700"


def test_R154_out_of_order_three_refreshes(page):
    reset()
    ui_login(page, "ada")
    held = []
    state = {"n": 0}

    def handler(route):
        if _is_read(route) and route.request.url.split("?")[0].endswith("/me"):
            state["n"] += 1
            if state["n"] <= 2:  # the first two refreshes are delayed
                held.append((route, route.fetch()))
                return
        route.continue_()

    page.route("**/*", handler)
    T(page, "wallet-refresh").click()
    page.wait_for_timeout(300)
    pay(token("bob"), "ada", 100)
    T(page, "wallet-refresh").click()
    page.wait_for_timeout(300)
    pay(token("bob"), "ada", 200)
    T(page, "wallet-refresh").click()
    expect(T(page, "wallet-balance")).to_have_text("103.00 EUR")
    for route, resp in reversed(held):
        route.fulfill(response=resp)
    page.wait_for_timeout(800)
    expect(T(page, "wallet-balance")).to_have_text("103.00 EUR")


# ---------------------------------------------------------------- R155 refused payment
def test_R155_refused_payment_refreshes_and_keeps_inputs(page):
    reset()
    ui_login(page, "ada")
    expect(T(page, "wallet-balance")).to_have_text("100.00 EUR")
    other = pay(token("ada"), "bob", 9900)  # another client spends the balance
    fill_pay(page, "cy", "50.00", "keep me", "private")
    T(page, "pay-submit").click()
    expect(T(page, "pay-error")).to_be_visible()
    expect(T(page, "wallet-balance")).to_have_text("1.00 EUR")
    expect(T(page, "activity-item-" + other["payment_id"])).to_be_visible()
    assert T(page, "pay-handle").input_value() == "cy"
    assert T(page, "pay-amount").input_value() in ("50.00", "50")
    assert T(page, "pay-note").input_value() == "keep me"
    assert T(page, "pay-visibility").input_value() == "private"
    expect(T(page, "pay-uncertain")).to_have_count(0)
    assert me(token("cy"))["balance"] == 500


# ---------------------------------------------------------------- R157/R158 lost response
def _lose_first_payment(page, commit):
    state = {"n": 0}

    def handler(route):
        req = route.request
        if req.method == "POST" and req.url.split("?")[0].endswith("/payments"):
            state["n"] += 1
            if state["n"] == 1:
                if commit:
                    route.fetch()  # the request reaches the server and commits
                route.abort()
                return
        route.continue_()

    page.route("**/*", handler)
    return state


@pytest.mark.parametrize("commit", [True, False])
def test_R157_R158_lost_response_then_retry_once(page, commit):
    reset()
    ui_login(page, "ada")
    seen = record_posts(page, "/payments")
    _lose_first_payment(page, commit)
    fill_pay(page, "bob", "10.00", "retry me", "private")
    T(page, "pay-submit").click()
    expect(T(page, "pay-uncertain")).to_be_visible()
    assert text_of(page, "pay-uncertain").strip() != ""
    expect(T(page, "pay-error")).to_have_count(0)
    assert me(token("ada"))["balance"] == (9000 if commit else 10000)
    # the unchanged form is retryable with the SAME key and body
    T(page, "pay-submit").click()
    expect(T(page, "pay-uncertain")).to_have_count(0)
    expect(T(page, "pay-error")).to_have_count(0)
    expect(T(page, "wallet-balance")).to_have_text("90.00 EUR")
    assert page.locator('[data-testid^="activity-item-"]').count() == 1
    assert len(seen) == 2 and seen[0]["key"] == seen[1]["key"] and seen[0]["key"], seen
    assert seen[0]["body"] == seen[1]["body"]
    assert me(token("ada"))["balance"] == 9000 and me(token("bob"))["balance"] == 3500
    # a further unchanged submit does not pay again
    T(page, "pay-submit").click()
    page.wait_for_timeout(400)
    assert me(token("ada"))["balance"] == 9000


def test_R157_changed_form_after_uncertain_is_a_new_payment(page):
    reset()
    ui_login(page, "ada")
    seen = record_posts(page, "/payments")
    _lose_first_payment(page, True)
    fill_pay(page, "bob", "10.00")
    T(page, "pay-submit").click()
    expect(T(page, "pay-uncertain")).to_be_visible()
    T(page, "pay-amount").fill("11.00")
    T(page, "pay-submit").click()
    expect(T(page, "pay-uncertain")).to_have_count(0)
    assert seen[0]["key"] != seen[1]["key"]
    # the lost 10.00 committed and the changed 11.00 is a separate payment
    assert me(token("ada"))["balance"] == 10000 - 1000 - 1100


# ---------------------------------------------------------------- R161/R162 upgrade flow
def _export():
    s, b, _ = api("GET", "/_test/export")
    assert s == 200
    return b


def _import(doc):
    s, b, _ = api("POST", "/_test/import", doc)
    assert s == 204, (s, b)


def test_R162_lost_payment_recovered_after_import_without_reload(page):
    reset()
    ui_login(page, "ada")
    page.evaluate("window.__same_page = 'yes'")
    seen = record_posts(page, "/payments")
    _lose_first_payment(page, True)
    fill_pay(page, "bob", "10.00", "across the upgrade")
    T(page, "pay-submit").click()
    expect(T(page, "pay-uncertain")).to_be_visible()
    doc = _export()
    reset(fixture([("zz", 1)]))  # the destination holds something else entirely
    _import(doc)  # replaces it, tokens preserved
    T(page, "pay-submit").click()  # retry with the same key and body
    expect(T(page, "pay-uncertain")).to_have_count(0)
    expect(T(page, "pay-error")).to_have_count(0)
    expect(T(page, "wallet-balance")).to_have_text("90.00 EUR")
    assert page.locator('[data-testid^="activity-item-"]').count() == 1
    assert page.evaluate("window.__same_page") == "yes", "the page was reloaded"
    assert seen[0]["key"] == seen[1]["key"] and seen[0]["body"] == seen[1]["body"]
    assert me(token("ada"))["balance"] == 9000 and me(token("bob"))["balance"] == 3500
    assert T(page, "pay-handle").input_value() == "bob"


def test_R161_signed_in_browser_survives_import_and_pays_pending_request(page):
    fx = fixture(requests=[{"id": "rq_1", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 500, "note": "taxi", "status": "pending"}])
    reset(fx)
    ui_login(page, "ada", then="/requests")
    expect(T(page, "request-pay-rq_1")).to_be_visible()
    doc = _export()
    reset(fixture([("zz", 1)]))
    _import(doc)
    T(page, "request-pay-rq_1").click()
    expect(T(page, "request-item-rq_1")).to_have_attribute("data-status", "paid")
    expect(T(page, "request-error")).to_have_count(0)
    assert me(token("ada"))["balance"] == 9500 and me(token("bob"))["balance"] == 3000
    expect(T(page, "current-user")).to_contain_text("Ada")  # still signed in
    goto(page, "/")
    expect(T(page, "current-user")).to_contain_text("Ada")
    expect(T(page, "wallet-balance")).to_have_text("95.00 EUR")
