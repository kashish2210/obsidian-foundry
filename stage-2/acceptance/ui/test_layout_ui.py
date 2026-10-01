import re
import urllib.request

from conftest import *

ROUTES = ["/", "/requests", "/split", "/authorizations"]


# ---------------------------------------------------------------- R124 content negotiation
def _get(path, accept=None, token=None):
    h = {}
    if accept is not None:
        h["Accept"] = accept
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, headers=h)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, r.headers.get("Content-Type", ""), r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.headers.get("Content-Type", ""), e.read().decode("utf-8", "replace")


@pytest.mark.parametrize("path", ["/requests", "/authorizations"])
def test_R124_content_negotiation(path):
    reset()
    tok = token("ada")
    for accept in ["text/html", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"]:
        status, ctype, body = _get(path, accept)
        assert status == 200 and "text/html" in ctype, (accept, status, ctype)
        assert "<html" in body.lower()
    # API clients (no Accept, Accept JSON, */*) get JSON
    for accept in [None, "application/json", "*/*"]:
        status, ctype, body = _get(path, accept, tok)
        assert status == 200 and "application/json" in ctype, (accept, status, ctype)
        doc = json.loads(body)
        assert isinstance(doc, dict) and ("requests" in doc or "authorizations" in doc) and "has_more" in doc
    # an API call without a token is still a JSON 401
    status, ctype, body = _get(path, "application/json")
    assert status == 401 and "application/json" in ctype
    assert json.loads(body)["error"]["code"] == "unauthenticated"
    # other API paths keep JSON even for a browser Accept header
    status, ctype, _ = _get("/me", "text/html", tok)
    assert "application/json" in ctype


def test_R124_static_routes_serve_html():
    reset()
    for path in ["/", "/split", "/signup", "/login"]:
        status, ctype, body = _get(path, "text/html")
        assert status == 200 and "text/html" in ctype, path


# ---------------------------------------------------------------- R126 embedded assets
def test_R126_no_external_requests(page):
    reset(fixture(authorizations=[seed_az("a_1", "ada", "bob", 500, "open", iso_in(7200))]))
    origins = set()

    def on_req(req):
        u = req.url
        if not u.startswith("data:") and not u.startswith("blob:") and not u.startswith("about:"):
            origins.add(re.match(r"^[a-z]+://[^/]+", u).group(0))

    page.on("request", on_req)
    ui_login(page, "ada")
    for p in ["/", "/requests", "/split", "/authorizations", "/login", "/signup"]:
        goto(page, p)
        page.wait_for_load_state("networkidle")
    assert origins <= {re.match(r"^[a-z]+://[^/]+", BASE).group(0)}, origins
    # fonts/scripts/styles declared in the served HTML are same-origin paths
    status, ctype, body = _get("/", "text/html")
    for ref in re.findall(r'(?:src|href)="(https?://[^"]+)"', body):
        assert ref.startswith(BASE), ref


# ---------------------------------------------------------------- R130 layout
@pytest.mark.parametrize("width", [375, 768, 1280])
def test_R130_no_horizontal_scroll(browser, width):
    reset(fixture(
        [("ada", 123456789), ("bob", 2500), ("averylonghandle12345", 0)],
        requests=[{"id": "rq_1", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 1200,
                   "note": "a very long note " * 10, "status": "pending"}],
        authorizations=[seed_az("a_1", "ada", "bob", 2000, "open", iso_in(7200), note="long " * 40),
                        seed_az("a_2", "bob", "ada", 100, "open", iso_in(7200))]))
    ctx = browser.new_context(viewport={"width": width, "height": 800})
    page = ctx.new_page()
    page.set_default_timeout(7000)
    try:
        ta = token("ada")
        pay(ta, "bob", 1000, note="word " * 40)
        pay(ta, "averylonghandle12345", 5, note="x" * 150)
        for path in ["/login", "/signup"]:
            goto(page, path)
            page.wait_for_load_state("networkidle")
            sw = page.evaluate("document.documentElement.scrollWidth")
            assert sw <= width, (path, sw)
        ui_login(page, "ada")
        for path in ROUTES:
            goto(page, path)
            expect(T(page, "current-user")).to_be_visible()
            page.wait_for_load_state("networkidle")
            sw = page.evaluate("document.documentElement.scrollWidth")
            assert sw <= width, (path, width, sw)
        # the required controls are usable (visible) at this width
        goto(page, "/")
        for tid in ["pay-handle", "pay-amount", "pay-note", "pay-visibility", "pay-submit", "request-submit", "wallet-balance"]:
            expect(T(page, tid)).to_be_visible()
    finally:
        ctx.close()


def test_R130_form_error_states_do_not_overflow_at_375(browser):
    reset()
    ctx = browser.new_context(viewport={"width": 375, "height": 800})
    page = ctx.new_page()
    try:
        ui_login(page, "ada")
        fill_pay(page, "bob", "99999999999999999.00", "n" * 200)
        T(page, "pay-submit").click()
        page.wait_for_timeout(500)
        assert page.evaluate("document.documentElement.scrollWidth") <= 375
        T(page, "pay-amount").fill("abc")
        T(page, "pay-submit").click()
        expect(T(page, "pay-error")).to_be_visible()
        assert page.evaluate("document.documentElement.scrollWidth") <= 375
    finally:
        ctx.close()


# ---------------------------------------------------------------- R131 labels / focus (measurable parts)
INPUTS = {
    "/": ["pay-handle", "pay-amount", "pay-note", "pay-visibility", "request-handle", "request-amount", "request-note",
          "authorize-handle", "authorize-amount", "authorize-note", "authorize-visibility"],
    "/split": ["split-amount", "split-handles", "split-note"],
}


def test_R131_inputs_have_visible_labels(page):
    reset()
    ui_login(page, "ada")
    missing = []
    for path, ids in list(INPUTS.items()) + [("/login", []), ("/signup", [])]:
        if path in ("/login", "/signup"):
            page.context.clear_cookies()
            page.evaluate("try { localStorage.clear(); sessionStorage.clear(); } catch (e) {}")
            goto(page, path)
            ids = ["login-email", "login-password"] if path == "/login" else ["signup-email", "signup-password", "signup-display-name"]
        else:
            goto(page, path)
        for tid in ids:
            loc = T(page, tid)
            if loc.count() == 0:
                missing.append(tid + " (absent)")
                continue
            ok = loc.evaluate("""e => {
                const labelled = e.labels && e.labels.length > 0 &&
                    Array.from(e.labels).some(l => l.innerText.trim().length > 0 && getComputedStyle(l).display !== 'none' &&
                                                   getComputedStyle(l).visibility !== 'hidden');
                return labelled;
            }""")
            if not ok:
                missing.append(tid)
    assert not missing, "inputs without a visible <label>: %s" % missing


def test_R131_keyboard_focus_is_visible(page):
    reset()
    ui_login(page, "ada")
    goto(page, "/")
    expect(T(page, "pay-handle")).to_be_visible()
    bad = []
    seen_ids = set()
    for _ in range(60):
        page.keyboard.press("Tab")
        info = page.evaluate("""() => {
            const e = document.activeElement;
            if (!e || e === document.body) return null;
            const cs = getComputedStyle(e);
            const hasOutline = cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0;
            const hasShadow = cs.boxShadow && cs.boxShadow !== 'none';
            return {id: e.getAttribute('data-testid') || e.tagName, ok: hasOutline || hasShadow, tag: e.tagName};
        }""")
        if info is None:
            continue
        seen_ids.add(info["id"])
        if not info["ok"]:
            bad.append(info["id"])
    assert len(seen_ids) > 5
    assert not bad, "focused controls without a visible focus style: %s" % sorted(set(bad))


def test_R131_consistent_navigation(page):
    reset()
    ui_login(page, "ada")
    sets = []
    for path in ROUTES:
        goto(page, path)
        expect(T(page, "current-user")).to_be_visible()
        hrefs = page.eval_on_selector_all("nav a, header a", "els => els.map(e => e.getAttribute('href'))")
        sets.append(sorted(h for h in hrefs if h and h.startswith("/")))
        assert T(page, "logout-button").count() == 1
    assert all(s == sets[0] for s in sets), sets
    for target in ["/requests", "/split", "/authorizations"]:
        assert target in sets[0]


def test_R129_human_readable_states(page):
    _ = reset(fixture(requests=[{"id": "rq_1", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 1200, "note": "taxi", "status": "pending"}]))
    ta = token("ada")
    p = pay(ta, "bob", 1000, note="dinner", visibility="private")
    ui_login(page, "ada")
    body = page.inner_text("body")
    # no raw API data in the page: no JSON blobs and no timestamps with raw offsets in the feed row
    item = T(page, "activity-item-" + p["payment_id"]).inner_text()
    assert "{" not in item and '"' not in item, item
    assert not re.search(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d[+-]\d\d:\d\d", item), "raw RFC 3339 timestamp in the feed row: " + item
    assert "u_ada" not in item and "u_bob" not in item
