import re

from conftest import *

RFC3339 = re.compile(r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?([+-]\d\d:\d\d|Z)$")


def _seeded():
    azs = [
        seed_az("a_open", "ada", "bob", 2000, "open", iso_in(7200)),
        seed_az("a_past", "ada", "bob", 1500, "open", iso_in(-7200)),
        seed_az("a_cap", "ada", "bob", 500, "captured", iso_in(7200)),
        seed_az("a_void", "ada", "cy", 700, "voided", iso_in(7200)),
        seed_az("a_in", "bob", "ada", 900, "open", iso_in(7200)),
    ]
    reset(fixture(authorizations=azs))


def test_R189_wallet_numbers(page):
    _seeded()
    ui_login(page, "ada")
    bal, av, hd = T(page, "wallet-balance"), T(page, "wallet-available"), T(page, "wallet-held")
    expect(bal).to_have_text("100.00 EUR")
    expect(av).to_have_text("80.00 EUR")
    expect(hd).to_have_text("20.00 EUR")
    assert (bal.get_attribute("data-amount"), av.get_attribute("data-amount"), hd.get_attribute("data-amount")) == ("10000", "8000", "2000")
    # R128: available is the headline value, total and held are secondary
    size = lambda loc: float(loc.evaluate("e => parseFloat(getComputedStyle(e).fontSize)"))
    assert size(av) > size(bal) and size(av) > size(hd), (size(av), size(bal), size(hd))


def test_R189_held_absent_when_zero_and_appears_without_reload(page):
    reset()
    ui_login(page, "cy")
    expect(T(page, "wallet-available")).to_have_text("5.00 EUR")
    expect(T(page, "wallet-held")).to_have_count(0)
    authorize(token("cy"), "bob", 200)
    T(page, "wallet-refresh").click()
    expect(T(page, "wallet-held")).to_have_text("2.00 EUR")
    expect(T(page, "wallet-available")).to_have_text("3.00 EUR")
    expect(T(page, "wallet-balance")).to_have_text("5.00 EUR")


def test_R194_seeded_holds_shown_immediately(page):
    _seeded()
    ui_login(page, "bob")
    expect(T(page, "wallet-held")).to_have_text("9.00 EUR")  # bob holds 900 for ada
    expect(T(page, "wallet-available")).to_have_text("16.00 EUR")
    expect(T(page, "wallet-balance")).to_have_text("25.00 EUR")


def test_R190_authorize_form(page):
    reset()
    ui_login(page, "ada")
    expect(T(page, "authorize-error")).to_have_count(0)
    seen = record_posts(page, "/authorizations")
    T(page, "authorize-handle").fill("bob")
    T(page, "authorize-amount").fill("30.00")
    T(page, "authorize-note").fill("deposit")
    T(page, "authorize-visibility").select_option("private")
    T(page, "authorize-submit").click()
    expect(T(page, "wallet-held")).to_have_text("30.00 EUR")
    expect(T(page, "wallet-available")).to_have_text("70.00 EUR")
    expect(T(page, "authorize-error")).to_have_count(0)
    assert seen[0]["body"]["amount"] == 3000 and seen[0]["body"]["visibility"] == "private" and seen[0]["body"]["note"] == "deposit"
    assert T(page, "authorize-handle").input_value() == "bob"
    # an unchanged resubmit does not authorise twice (R142 rule)
    for _ in range(2):
        T(page, "authorize-submit").click()
        page.wait_for_timeout(400)
    assert me(token("ada"))["held"] == 3000
    # changing a field makes it a new authorization
    T(page, "authorize-note").fill("deposit 2")
    T(page, "authorize-submit").click()
    expect(T(page, "wallet-held")).to_have_text("60.00 EUR")
    # refusals
    T(page, "authorize-amount").fill("500.00")
    T(page, "authorize-submit").click()
    expect(T(page, "authorize-error")).to_be_visible()  # insufficient available
    n = len(seen)
    for bad in ["15.005", "abc"]:
        T(page, "authorize-amount").fill(bad)
        T(page, "authorize-submit").click()
        expect(T(page, "authorize-error")).to_be_visible()
    page.wait_for_timeout(300)
    assert len(seen) == n
    assert me(token("ada"))["held"] == 6000
    # the hold does not appear in the activity feed
    assert page.locator('[data-testid^="activity-item-"]').count() == 0


def test_R190_authorize_insufficient_counts_held_funds(page):
    reset(fixture(authorizations=[seed_az("a_1", "ada", "bob", 9500, "open", iso_in(7200))]))
    ui_login(page, "ada")
    expect(T(page, "wallet-available")).to_have_text("5.00 EUR")
    T(page, "authorize-handle").fill("cy")
    T(page, "authorize-amount").fill("6.00")
    T(page, "authorize-submit").click()
    expect(T(page, "authorize-error")).to_be_visible()
    fill_pay(page, "cy", "6.00")
    T(page, "pay-submit").click()
    expect(T(page, "pay-error")).to_be_visible()
    fill_pay(page, "cy", "5.00")
    T(page, "pay-submit").click()
    expect(T(page, "wallet-available")).to_have_text("0.00 EUR")


def test_R191_authorization_list(page):
    _seeded()
    ui_login(page, "ada", then="/authorizations")
    expect(T(page, "authorization-list")).to_be_visible()
    want = {"a_open": "open", "a_past": "expired", "a_cap": "captured", "a_void": "voided", "a_in": "open"}
    for aid, st in want.items():
        expect(T(page, "authorization-item-" + aid)).to_have_attribute("data-status", st)
    assert text_of(page, "authorization-amount-a_open") == "20.00 EUR"
    assert text_of(page, "authorization-amount-a_past") == "15.00 EUR"
    # captured amount only when captured
    assert T(page, "authorization-captured-a_open").count() == 0
    assert T(page, "authorization-captured-a_past").count() == 0
    assert T(page, "authorization-captured-a_void").count() == 0
    assert T(page, "authorization-captured-a_cap").count() == 1
    # expires_at is shown as RFC 3339
    s, b, _ = api("GET", "/authorizations?limit=200", token=token("ada"))
    api_by = {a["authorization_id"]: a for a in b["authorizations"]}
    for aid in want:
        txt = text_of(page, "authorization-expires-" + aid).strip()
        assert RFC3339.match(txt), txt
        assert txt == api_by[aid]["expires_at"]


def test_R191_order_newest_first(page):
    reset(fixture([("ada", 100000), ("bob", 0)]))
    ta = token("ada")
    ids = []
    for i in range(3):
        ids.append(authorize(ta, "bob", 100 + i)["authorization_id"])
        time.sleep(1.1)
    ui_login(page, "ada", then="/authorizations")
    expect(T(page, "authorization-list")).to_be_visible()
    order = page.eval_on_selector_all('[data-testid="authorization-list"] [data-testid^="authorization-item-"]',
                                      "els => els.map(e => e.getAttribute('data-testid').replace('authorization-item-',''))")
    assert order == list(reversed(ids)), order


def test_R192_buttons_only_where_allowed(page):
    _seeded()
    ui_login(page, "ada", then="/authorizations")
    expect(T(page, "authorization-item-a_open")).to_be_visible()
    # outgoing open: void only
    assert T(page, "authorization-void-a_open").count() == 1
    assert T(page, "authorization-capture-a_open").count() == 0
    assert T(page, "authorization-capture-amount-a_open").count() == 0
    # incoming open: capture (+ prefilled amount) only
    assert T(page, "authorization-capture-a_in").count() == 1
    assert T(page, "authorization-capture-amount-a_in").count() == 1
    assert T(page, "authorization-void-a_in").count() == 0
    # closed or expired: nothing
    for aid in ["a_past", "a_cap", "a_void"]:
        for kind in ["void", "capture", "capture-amount"]:
            assert T(page, "authorization-%s-%s" % (kind, aid)).count() == 0, (aid, kind)
    v = T(page, "authorization-capture-amount-a_in").input_value()
    assert re.fullmatch(r"9(\.0{1,2})?", v), v  # pre-filled with the remaining 9.00
    # the receiver's view is mirrored
    page2 = page.context.browser.new_context().new_page()
    ui_login(page2, "bob", then="/authorizations")
    expect(T(page2, "authorization-item-a_open")).to_be_visible()
    assert T(page2, "authorization-capture-a_open").count() == 1
    assert T(page2, "authorization-void-a_open").count() == 0
    assert T(page2, "authorization-void-a_in").count() == 1
    assert T(page2, "authorization-capture-a_in").count() == 0
    assert T(page2, "authorization-capture-a_past").count() == 0


def test_R192_capture_prefill_follows_remaining(page):
    reset(fixture(authorizations=[seed_az("a_1", "ada", "bob", 2000, "open", iso_in(7200))]))
    s, b, _ = api("POST", "/authorizations/a_1/capture", {"amount": 500, "final": False}, token("bob"), newkey())
    assert s == 201
    ui_login(page, "bob", then="/authorizations")
    expect(T(page, "authorization-capture-amount-a_1")).to_be_visible()
    assert float(T(page, "authorization-capture-amount-a_1").input_value()) == 15.0


def test_R193_capture_flow_and_errors(page):
    _seeded()
    ui_login(page, "bob", then="/authorizations")
    expect(T(page, "authorization-error")).to_have_count(0)
    seen = record_posts(page, "/capture")
    # an amount above the remainder is refused
    T(page, "authorization-capture-amount-a_open").fill("30.00")
    T(page, "authorization-capture-a_open").click()
    expect(T(page, "authorization-error")).to_be_visible()
    assert me(token("bob"))["balance"] == 2500
    # invalid decimal: nothing sent
    n = len(seen)
    T(page, "authorization-capture-amount-a_open").fill("1.005")
    T(page, "authorization-capture-a_open").click()
    expect(T(page, "authorization-error")).to_be_visible()
    page.wait_for_timeout(300)
    assert len(seen) == n
    # a valid capture: status and captured amount update in place
    T(page, "authorization-capture-amount-a_open").fill("7.50")
    T(page, "authorization-capture-a_open").click()
    expect(T(page, "authorization-item-a_open")).to_have_attribute("data-status", "captured")
    expect(T(page, "authorization-captured-a_open")).to_have_text("7.50 EUR")
    expect(T(page, "authorization-capture-a_open")).to_have_count(0)
    expect(T(page, "authorization-error")).to_have_count(0)
    assert seen[-1]["body"]["amount"] == 750
    assert me(token("bob"))["balance"] == 2500 + 750
    a = me(token("ada"))
    assert a["total"] == 10000 - 750 and a["held"] == 0 and a["available"] == 10000 - 750  # remainder released
    # the capture payment is in the feed
    goto(page, "/")
    assert page.locator('[data-testid^="activity-item-"]').count() == 1


def test_R193_capture_refused_after_void_elsewhere(page):
    _seeded()
    ui_login(page, "bob", then="/authorizations")
    expect(T(page, "authorization-capture-a_open")).to_be_visible()
    s, b, _ = api("POST", "/authorizations/a_open/void", token=token("ada"))
    assert s == 200
    T(page, "authorization-capture-a_open").click()
    expect(T(page, "authorization-error")).to_be_visible()
    expect(T(page, "authorization-item-a_open")).to_have_attribute("data-status", "voided")
    expect(T(page, "authorization-capture-a_open")).to_have_count(0)
    assert me(token("bob"))["balance"] == 2500


def test_R193_void_flow(page):
    _seeded()
    ui_login(page, "ada", then="/authorizations")
    T(page, "authorization-void-a_open").click()
    expect(T(page, "authorization-item-a_open")).to_have_attribute("data-status", "voided")
    expect(T(page, "authorization-void-a_open")).to_have_count(0)
    assert me(token("ada"))["held"] == 0
    # void refused after the receiver captured elsewhere
    expect(T(page, "authorization-void-a_in")).to_have_count(0)  # ada is the receiver there
    reset(fixture(authorizations=[seed_az("a_1", "ada", "bob", 2000, "open", iso_in(7200))]))
    ui_login(page, "ada", then="/authorizations")
    expect(T(page, "authorization-void-a_1")).to_be_visible()
    api("POST", "/authorizations/a_1/capture", {}, token("bob"), newkey())
    T(page, "authorization-void-a_1").click()
    expect(T(page, "authorization-error")).to_be_visible()
    expect(T(page, "authorization-item-a_1")).to_have_attribute("data-status", "captured")


def test_R193_empty_authorizations(page):
    reset()
    ui_login(page, "dee", then="/authorizations")
    expect(T(page, "empty-authorizations")).to_be_visible()
    assert page.locator('[data-testid^="authorization-item-"]').count() == 0
    expect(T(page, "authorization-error")).to_have_count(0)
    authorize(token("ada"), "dee", 100)
    goto(page, "/authorizations")
    expect(T(page, "empty-authorizations")).to_have_count(0)
    assert page.locator('[data-testid^="authorization-item-"]').count() == 1
