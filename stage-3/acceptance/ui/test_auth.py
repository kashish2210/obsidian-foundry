from conftest import *


def test_R123_routes_reachable_and_navigable(page):
    reset()
    for path in ["/", "/requests", "/split", "/signup", "/login", "/authorizations"]:
        r = page.goto(BASE + path)
        assert r.status == 200, path
        assert "text/html" in r.headers.get("content-type", ""), path
    ui_login(page, "ada")
    # other screens are reachable through the UI (links), starting from /
    for target in ["/requests", "/split", "/authorizations"]:
        goto(page, "/")
        link = page.locator('a[href="%s"]' % target)
        assert link.count() >= 1, "no link to %s from /" % target
        link.first.click()
        page.wait_for_url(BASE + target)
        expect(T(page, "current-user")).to_be_visible()


def test_R132_R134_signup_signs_in(page):
    reset()
    goto(page, "/signup")
    expect(T(page, "auth-error")).to_have_count(0)
    T(page, "signup-email").fill("zed@example.com")
    T(page, "signup-password").fill("longenough")
    T(page, "signup-display-name").fill("Zed Zebra")
    T(page, "signup-submit").click()
    expect(T(page, "current-user")).to_contain_text("Zed Zebra")
    assert text_of(page, "current-handle") == "zed"
    goto(page, "/")
    expect(T(page, "current-user")).to_contain_text("Zed Zebra")
    expect(T(page, "wallet-balance")).to_have_text("0.00 EUR")


def test_R132_R133_login_and_auth_error(page):
    reset()
    goto(page, "/login")
    expect(T(page, "auth-error")).to_have_count(0)
    T(page, "login-email").fill("ada@example.com")
    T(page, "login-password").fill("wrong password")
    T(page, "login-submit").click()
    expect(T(page, "auth-error")).to_be_visible()
    assert text_of(page, "auth-error").strip() != ""
    expect(T(page, "current-user")).to_have_count(0)
    T(page, "login-password").fill(PW)
    T(page, "login-submit").click()
    expect(T(page, "current-user")).to_contain_text("Ada")
    expect(T(page, "auth-error")).to_have_count(0)


def test_R133_signup_errors(page):
    reset()
    cases = [("ada@example.com", "longenough"),   # email taken
             ("ada@other.org", "longenough"),     # derived handle taken
             ("new@example.com", "short"),        # password too short
             ("not-an-email", "longenough")]      # bad email
    for email, password in cases:
        goto(page, "/signup")
        expect(T(page, "auth-error")).to_have_count(0)
        T(page, "signup-email").fill(email)
        T(page, "signup-password").fill(password)
        T(page, "signup-display-name").fill("Somebody")
        T(page, "signup-submit").click()
        expect(T(page, "auth-error")).to_be_visible()
        expect(T(page, "current-user")).to_have_count(0)


def test_R134_R136_current_user_on_every_screen_and_session_persists(page, ctx):
    reset()
    ui_login(page, "ada")
    for path in ["/", "/requests", "/split", "/authorizations"]:
        goto(page, path)  # a direct (full) navigation: the session must persist
        expect(T(page, "current-user")).to_contain_text("Ada")
        assert text_of(page, "current-handle") == "ada", path
    # a second tab in the same browser context is signed in as well
    p2 = ctx.new_page()
    goto(p2, "/requests")
    expect(T(p2, "current-user")).to_contain_text("Ada")


def test_R135_logout(page):
    reset()
    ui_login(page, "bob")
    T(page, "logout-button").click()
    expect(T(page, "current-user")).to_have_count(0)
    goto(page, "/")
    expect(T(page, "current-user")).to_have_count(0)
    expect(T(page, "wallet-balance")).to_have_count(0)
    goto(page, "/requests")
    expect(T(page, "current-user")).to_have_count(0)
    # logging in again works
    ui_login(page, "bob")
    expect(T(page, "current-user")).to_contain_text("Bob")
