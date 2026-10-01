"""Shared helpers for the pocketful browser acceptance suite (black-box, BASE_URL)."""
import json
import os
import re
import time
import urllib.error
import urllib.request

import pytest
from playwright.sync_api import expect, sync_playwright

BASE = os.environ.get("BASE_URL", "http://localhost:8080").rstrip("/")
PW = "correct horse"
expect.set_options(timeout=7000)


# ---------------------------------------------------------------- API helpers
def api(method, path, body=None, token=None, key=None, headers=None, raw=False):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    if key is not None:
        h["Idempotency-Key"] = key
    if headers:
        h.update(headers)
    data = None
    if body is not None:
        data = body if isinstance(body, (bytes, str)) and raw else json.dumps(body)
        data = data.encode() if isinstance(data, str) else data
    req = urllib.request.Request(BASE + path, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            status, payload, hdrs = r.status, r.read(), r.headers
    except urllib.error.HTTPError as e:
        status, payload, hdrs = e.code, e.read(), e.headers
    try:
        parsed = json.loads(payload) if payload else None
    except ValueError:
        parsed = payload.decode("utf-8", "replace")
    return status, parsed, hdrs


_k = [0]


def newkey():
    _k[0] += 1
    return "ui-%d-%d" % (time.time_ns(), _k[0])


def user(handle, balance):
    return {"id": "u_" + handle, "email": handle + "@example.com", "password": PW,
            "display_name": handle.capitalize(), "handle": handle, "balance": balance}


STD = [("ada", 10000), ("bob", 2500), ("cy", 500), ("dee", 0)]


def fixture(users=None, cur="EUR", mu=2, **extra):
    f = {"currency": cur, "minor_units": mu, "settlement_operator_ids": ["u_op"],
         "users": [user(h, b) for h, b in (users or STD)] + [user("op", 0)]}
    f.update(extra)
    return f


def reset(fx=None):
    s, b, _ = api("POST", "/_test/reset", fx if fx is not None else fixture())
    assert s == 204, (s, b)


def token(handle):
    s, b, _ = api("POST", "/auth/login", {"email": handle + "@example.com", "password": PW})
    assert s == 200, (s, b)
    return b["token"]


def pay(tok, to, amount, **kw):
    s, b, _ = api("POST", "/payments", dict(to_handle=to, amount=amount, **kw), tok, newkey())
    assert s == 201, (s, b)
    return b


def request_money(tok, payer, amount, note=""):
    s, b, _ = api("POST", "/requests", {"payer_handle": payer, "amount": amount, "note": note}, tok, newkey())
    assert s == 201, (s, b)
    return b


def authorize(tok, to, amount, **kw):
    s, b, _ = api("POST", "/authorizations", dict(to_handle=to, amount=amount, **kw), tok, newkey())
    assert s == 201, (s, b)
    return b


def me(tok):
    return api("GET", "/me", token=tok)[1]


def fmt(minor, mu=2, cur="EUR"):
    if mu == 0:
        return "%d %s" % (minor, cur)
    s = str(minor).rjust(mu + 1, "0")
    return "%s.%s %s" % (s[:-mu], s[-mu:], cur)


def iso_in(seconds):
    t = time.gmtime(time.time() + seconds)
    return time.strftime("%Y-%m-%dT%H:%M:%S", t) + "+00:00"


def seed_az(id_, frm, to, amount, status, expires, note=None):
    return {"id": id_, "from_user_id": "u_" + frm, "to_user_id": "u_" + to, "amount": amount,
            "note": note if note is not None else "seed " + id_, "visibility": "public",
            "status": status, "expires_at": expires}


def shares(amount, n):
    base, extra = divmod(amount, n)
    return [base + (1 if i < extra else 0) for i in range(n)]


# ------------------------------------------------------------ browser helpers
@pytest.fixture(scope="session")
def browser():
    with sync_playwright() as p:
        last = None
        kws = [{}, {"channel": "chrome"}, {"channel": "msedge"}]
        if os.environ.get("PW_CHANNEL"):
            kws.insert(0, {"channel": os.environ["PW_CHANNEL"]})
        for kw in kws:
            try:
                b = p.chromium.launch(**kw)
                break
            except Exception as e:  # browser binary missing; try the next channel
                last = e
        else:
            raise last
        yield b
        b.close()


@pytest.fixture
def ctx(browser):
    c = browser.new_context(viewport={"width": 1280, "height": 900})
    yield c
    c.close()


@pytest.fixture
def page(ctx):
    pg = ctx.new_page()
    pg.set_default_timeout(7000)
    return pg


def T(page, testid):
    return page.get_by_test_id(testid)


def text_of(page, testid):
    return T(page, testid).inner_text()


def goto(page, path):
    page.goto(BASE + path)


def ui_login(page, handle, then="/"):
    goto(page, "/login")
    T(page, "login-email").fill(handle + "@example.com")
    T(page, "login-password").fill(PW)
    T(page, "login-submit").click()
    expect(T(page, "current-user")).to_be_visible()
    if then and then != "/":
        goto(page, then)
    elif then == "/":
        goto(page, "/")
    expect(T(page, "current-user")).to_be_visible()


def login_on(page, handle, path="/"):
    ui_login(page, handle, then=path)


def fill_pay(page, handle=None, amount=None, note=None, vis=None):
    if handle is not None:
        T(page, "pay-handle").fill(handle)
    if amount is not None:
        T(page, "pay-amount").fill(amount)
    if note is not None:
        T(page, "pay-note").fill(note)
    if vis is not None:
        T(page, "pay-visibility").select_option(vis)


def record_posts(page, path_suffix):
    """Record bodies and headers of every POST whose path ends with path_suffix."""
    seen = []

    def on_req(req):
        if req.method == "POST" and req.url.split("?")[0].endswith(path_suffix):
            try:
                body = req.post_data_json
            except Exception:
                body = req.post_data
            seen.append({"body": body, "key": req.headers.get("idempotency-key"), "url": req.url})

    page.on("request", on_req)
    return seen


def amount_locator(page, testid):
    return T(page, testid)


def settle_ms(page, ms=600):
    page.wait_for_timeout(ms)


# Test modules do `from conftest import *`; never re-export the fixtures (that would register
# a second copy of every fixture per module and start Playwright twice).
__all__ = [n for n in list(globals()) if not n.startswith("_") and n not in ("browser", "ctx", "page")]
