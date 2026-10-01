# Pocketful requirement ledger

Source specs: `K:/hackathons/dark-factory-wearedevs/pocketful/spec/stage-1.md` (R1-R122), `stage-2.md` (R123-R197), `stage-3.md` (R198-R239) and `stage-4.md` (R240+).
One line per normative statement. `[deep]` marks behaviour the shipped checks are
unlikely to probe. Ids are stable across stages; later stages append.

## Stage 1

### §1 Scope / invariants
- R1 (§1) [deep] Sum of all wallet balances always equals the total seeded by the last `POST /_test/reset` (also across concurrent requests and retries).
- R2 (§1) [deep] No wallet balance is ever negative, not even transiently (under concurrency).
- R3 (§1) [deep] A payment request moves money at most once (concurrent/retried pay).
- R4 (§1) All amounts are exact integers in minor units; money moves only between existing wallets.

### §2 Delivery
- R5 (§2) Deliver HTTP service, `Dockerfile`, `RUN.md` with one command that builds and starts the service with no manual setup.
- R6 (§2) Image runs alone with `-e PORT=<port>` and a port mapping; no compose; all deps/seed inside the image; no outbound network at runtime.
- R7 (§2) [deep] Works within 2 vCPU / 2 GiB; first healthy response within 60 s of start.
- R8 (§2) [deep] Handles up to 50 concurrent in-flight requests; each request answers within 5 s (`/_test/reset` within 10 s).
- R9 (§2) State need not survive restart; all runtime assets inside the image.

### §3 Runtime contract
- R10 (§3.1) Listen on `0.0.0.0:$PORT`, default port `8080` when PORT unset.
- R11 (§3.2) `GET /health` → 200 `{"status":"ok"}` once ready; no auth.
- R12 (§3.3) `POST /_test/reset` with fixture body → 204; replaces ALL state (users, tokens, payments, requests, splits, settlements, idempotency records); no auth.
- R13 (§3.3) [deep] After reset returns 204 subsequent requests see only the fixture; repeated resets work; old tokens become invalid (401).
- R14 (§3.4) Requests/responses are `application/json; charset=utf-8` (response Content-Type header).
- R15 (§3.4) Timestamps are RFC 3339 with explicit offset (e.g. `+00:00` or `Z`-free offset form like `2026-09-24T11:04:03+00:00`).
- R16 (§3.4) Unknown body fields are ignored, never an error.
- R17 (§3.4) Unknown query parameters are ignored.
- R18 (§3.4) All generated ids are strings ≤ 64 chars.

### §4 Model
- R19 (§4) One currency declared in fixture (`currency`, `minor_units` ∈ {0,2,3}); every amount is integer minor units; responses echo `currency`.
- R20 (§4) [deep] An amount is valid if its JSON numeric value is integral: `1000`, `1000.0`, `1e3` are all the valid amount 1000. Booleans and strings are not numbers (→ 422 validation_failed for `amount`).
- R21 (§4) Handle unique, matches `^[a-z0-9_]{1,20}$`, never changes.
- R22 (§4) Seeded users take handle from fixture.
- R23 (§4) [deep] Signup derives handle from email: local part, lowercase, every char outside `[a-z0-9_]` → `_`, truncate to 20 chars.
- R24 (§4) New users start with balance 0 and can immediately receive money and be requested from.
- R25 (§4) Payment moves money wallet-to-wallet immediately and atomically; created directly or by paying a request.
- R26 (§4) Request status `pending` → exactly one of `paid`/`declined`/`cancelled`. Only payer may pay/decline; only requester may cancel.
- R27 (§4) [deep] A request may exceed payer balance: creation succeeds; pay while short → 409 `insufficient_funds` changing nothing; request stays pending and becomes payable once money arrives.
- R28 (§4) Visibility belongs to the payment (chosen by payer at pay time); requests have no visibility and never appear in any feed.
- R29 (§4) [deep] Feed: `GET /activity` returns payments only; a payment appears for caller iff `visibility == public` OR caller is sender OR receiver. No other rule.
- R30 (§4) `GET /requests` returns only requests where caller is requester or payer.
- R31 (§4) A split is not a feed item; its requests are visible only to their two parties; payments fulfilling them follow the feed rule.
- R32 (§4) Visibility is one value seen identically by both parties and everyone; a private payment is visible to its own receiver and sender.
- R33 (§4) `amount` max 1000000000 on any single request; balances stay within ±2^53; exact integer arithmetic (use int64).
- R34 (§4 fixture) Fixture format: `currency`, `minor_units`, `users[{id,email,password,display_name,handle,balance}]`, `payments[{id,from_user_id,to_user_id,amount,note,visibility}]`, `requests[{id,requester_id,payer_id,amount,note,status}]`, optional `settlement_operator_ids` (default []).
- R35 (§4 fixture) Seeded users can log in with given password immediately.
- R36 (§4 fixture) [deep] Fixture `balance` is final balance; seeded payments are NOT replayed against balances; seeded payments and requests are visible via /activity and /requests with their fixture ids; seeded requests with any status are honoured.
- R37 (§4 fixture) [deep] A negative `balance` in a fixture → reset returns 422 `validation_failed` and changes nothing (previous state intact).
- R38 (§4) Administrative balance endpoint is out of scope (don't need it).

### §5 Errors
- R39 (§5) Every 4xx/5xx body is `{"error":{"code":"...","message":"..."}}`.
- R40 (§5) 400 `malformed_request`: unparseable body or a field of wrong JSON type (except the amount/note/visibility rules in R45).
- R41 (§5) 400 `missing_idempotency_key`: required header absent or empty.
- R42 (§5) 401 `unauthenticated`: missing, malformed, or unknown bearer token.
- R43 (§5) 403 `forbidden`; 404 `not_found` (no such resource or not visible to caller); 409 `idempotency_key_reuse`; 422 `validation_failed` (missing required field/query param, or rule violation with no more specific code).
- R44 (§5) Correct JSON type but invalid format/out-of-range value → 422 `validation_failed` (invalid dates, negative counts, over max or length).
- R45 (§5) [deep] Invalid `amount` (incl. strings, booleans, non-integral, null) → 422; non-string `note` (incl. `null`) → 422; `visibility` other than `public`/`private` (incl. non-string) → 422. Omission selects defaults.
- R46 (§5) [deep] Integer query params must be plain decimal digits: `1e9`, `4.0`, `+4` → 422 `validation_failed`.
- R47 (§5) Body that is not a JSON object / does not parse → 400 `malformed_request`.
- R48 (§5) `Idempotency-Key` length 1..255 else (length > 255) 422 `validation_failed`.
- R49 (§5) `limit` integer 1..200 else 422; `offset` integer ≥0 else 422.
- R50 (§5) [deep] No 5xx responses ever, including under concurrent load.

### §6 Authentication
- R51 (§6) `POST /auth/signup {email,password,display_name}` → 201 `{user_id, display_name, token}`.
- R52 (§6) `POST /auth/login {email,password}` → 200 `{user_id, display_name, token}`.
- R53 (§6) Signup with registered email → 409 `email_taken`.
- R54 (§6) Password shorter than 8 characters → 422 `validation_failed`.
- R55 (§6) Email not of form `local@domain` → 422 `validation_failed`.
- R56 (§6) Wrong password or unknown email on login → 401 `unauthenticated`.
- R57 (§6) [deep] Derived handle already taken → 409 `handle_taken` and no account is created (email stays free).
- R58 (§6) All other endpoints except `/health`, `/_test/*`, `/auth/signup`, `/auth/login` require `Authorization: Bearer <token>`.
- R59 (§6) Tokens never expire; multiple valid tokens per account; concurrent sessions (login twice → both tokens work).
- R60 (§6) Passwords stored with bcrypt/scrypt/Argon2 or equivalent; never plaintext.

### §7 Idempotency (POST /payments, /requests, /requests/{id}/pay, /splits, /settlements)
- R61 (§7) Header absent or empty → 400 `missing_idempotency_key`.
- R62 (§7) First use → normal response 201.
- R63 (§7) Replay (same user, method, path, same JSON body value) → 200 with body identical (as JSON value) to original.
- R64 (§7) Same key, different body → 409 `idempotency_key_reuse`.
- R65 (§7) [deep] Key reused after original failed with 4xx → treated as first use.
- R66 (§7) [deep] Keys scoped per authenticated user: two users with same key do not interact.
- R67 (§7) [deep] Same key + same body on a different path is a different request and succeeds normally.
- R68 (§7) [deep] "Same body" = same JSON value after parsing (key order/whitespace irrelevant; `1000` vs `1e3`? treat by parsed value).
- R69 (§7) [deep] Concurrent identical requests with unused key: exactly one 201, others 200 with same body; effect once.
- R70 (§7) [deep] Replay returns the original response even after resource changed/cancelled; no further state change.
- R71 (§7) [deep] Order of checks: body parses as JSON object + caller authenticated → then claimed-key resolution → then field validation / resource checks. A successful key replayed with an invalid body → 409 `idempotency_key_reuse`.

### §8 API
- R72 (§8) `GET /me` → `{user_id, display_name, handle, balance, currency, minor_units}`.
- R73 (§8 payments) `POST /payments {to_handle, amount, note?, visibility?}` → 201 payment `{payment_id, from_user_id, from_handle, to_user_id, to_handle, amount, currency, note, visibility, request_id:null, created_at}` (+ `settlement_id: null`, see R113). note default `""`, visibility default `"public"`.
- R74 (§8 payments) Caller balance below amount → 409 `insufficient_funds`.
- R75 (§8 payments) amount <1, >1000000000, or non-integer → 422 `validation_failed`.
- R76 (§8 payments) `to_handle` is caller's own → 422 `self_payment`.
- R77 (§8 payments) note > 200 characters → 422 `validation_failed` (count Unicode characters, not bytes).
- R78 (§8 payments) Unknown `to_handle` → 404 `not_found`. Missing `to_handle` → 422.
- R79 (§8 payments) [deep] Debit+credit atomic; failed payment leaves no trace (no feed item, balances unchanged).
- R80 (§8 payments) [deep] `note` stored and returned verbatim: no trimming/escaping/normalisation; Unicode and emoji round-trip byte for byte.
- R81 (§8 requests) `POST /requests {payer_handle, amount, note?}` → 201 request `{request_id, requester_id, requester_handle, payer_id, payer_handle, amount, currency, note, status:"pending", payment_id:null, created_at}`; caller is requester.
- R82 (§8 requests) amount invalid → 422; `payer_handle` is self → 422 `self_request`; note >200 → 422; unknown handle → 404.
- R83 (§8 requests) Payer balance not checked on create.
- R84 (§8 pay) `POST /requests/{id}/pay {visibility?}` payer only; → 201 payment (same shape as /payments) with `request_id` set; request becomes `paid` with `payment_id`.
- R85 (§8 pay) Not pending → 409 `request_not_pending`; payer balance short → 409 `insufficient_funds`; caller not payer → 403 `forbidden`; unknown request → 404 `not_found`.
- R86 (§8 pay) [deep] Decision: the endpoint table wins over the generic 404 rule. Any authenticated non-payer (the requester or a third party) calling pay/decline on an existing request → 403 `forbidden`; non-requester calling cancel → 403. Only a nonexistent id → 404.
- R87 (§8 pay) [deep] `{}` and `{"visibility":"public"}` are different bodies → reusing key across them → 409 `idempotency_key_reuse`.
- R88 (§8 pay) [deep] Replay of a successful pay → 200 original payment body even though request is now `paid`; no money moves; never 409 `request_not_pending`.
- R89 (§8 decline) `POST /requests/{id}/decline` payer only, no key → 200 request with `status:"declined"`; declining an already-declined request → 200 current state; paid/cancelled → 409 `request_not_pending`; not payer → 403; unknown → 404.
- R90 (§8 cancel) `POST /requests/{id}/cancel` requester only, no key → 200 request `status:"cancelled"`; already cancelled → 200; paid/declined → 409 `request_not_pending`; not requester → 403; unknown → 404.
- R91 (§8 list) `GET /requests` → `{requests:[...], has_more}`; only caller's requests; newest first by `created_at`.
- R92 (§8 list) `direction` = `incoming` (caller is payer) | `outgoing` (caller is requester) | absent (both); unknown value → 422.
- R93 (§8 list) `status` = pending|paid|declined|cancelled|absent; unknown → 422.
- R94 (§8 list) `limit` default 50 range 1..200; `offset` default 0 ≥0; out of range → 422; `has_more` true iff items exist beyond the last returned.
- R95 (§8 splits) `POST /splits {amount, participant_handles, note?}` → 201 `{split_id, amount, currency, note, shares:[{handle,amount}], requests:[...], created_at}`.
- R96 (§8 splits) Caller may or may not be in participant_handles; shares cover every participant (incl. caller if listed) in given order, sum to amount.
- R97 (§8 splits) One pending request per participant except caller, for that share, caller as requester; `requests` in same order.
- R98 (§8 splits) amount invalid → 422; participant_handles empty or with duplicate → 422; note >200 → 422; any unknown handle → 404. Non-array/non-string entries → 400 malformed_request (wrong type).
- R99 (§8 splits) [deep] Split whose only participant is the caller is valid: one share, zero requests, `"requests": []`. No balance is checked by a split.
- R100 (§8 activity) `GET /activity?limit&offset` → `{payments:[...], has_more}`, feed rule R29, newest first by `created_at`; limit/offset as R94.

### §9 Money and rounding
- R101 (§9) Shares whole minor units, sum exactly to amount, differ by ≤1; extra units go to first participants in order. Table: 1000/3 → 334,333,333; 1/3 → 1,0,0; 10/3 → 4,3,3; 999/3 → 333×3; 5/5 → 1×5.
- R102 (§9) [deep] Different order → extra unit to a different person; share 0 is legal and still produces a request (for amount 0 — pay of a 0 request must work and move 0).
- R103 (§9) [deep] Splits independent; after any number of splits paid in full balances still sum to the seeded total.

### §10 Export / import
- R104 (§10) `GET /_test/export` (no auth) → 200 `{"track":"pocketful","format_version":1,"state":{...}}`.
- R105 (§10) `POST /_test/import` with the entire export object → 204; atomically replaces state; accepts unchanged export from this service.
- R106 (§10) [deep] Import is replacement not merge; repeating it restores state without duplicates; no dependency on source process/files/port.
- R107 (§10) [deep] Invalid JSON → 400 malformed_request; missing fields, wrong track/version, invalid state → 422 `validation_failed` and destination unchanged.
- R108 (§10) [deep] Export is atomic read-only snapshot; later writes do not change an export already taken.
- R109 (§10) [deep] Import preserves accounts + hashed-password login, existing bearer tokens, currency/minor_units, balances, payments, requests, settlement operator permissions, splits, all completed idempotency records (bodies + original responses). Ids and timestamps not regenerated; nothing replayed against balances.
- R110 (§10) [deep] Failed-request keys remain reusable after import; replays after import return the original 200 body; tokens valid after import.
- R111 (§10) [deep] Import removes all previous destination data and credentials (old tokens not in export → 401). Reset clears all state including imported state. Id counters must not collide with imported ids for newly created resources.

### §11 Settlements
- R112 (§11) Fixture may include `settlement_operator_ids` (default []). Operators may settle across any wallets; this grants no access to others' requests or private activity.
- R113 (§11) Every payment (in all responses: /payments, pay, /activity, settlement) exposes `settlement_id`: null for non-members, the batch id for members.
- R114 (§11) `POST /settlements` needs operator + idempotency key; no token → 401; authenticated non-operator → 403 `forbidden`.
- R115 (§11) Body `{"transfers":[{from_handle,to_handle,amount,note?,visibility?}]}`; 1..32 transfers; ordinary payment amount/note/visibility rules; defaults note "" and visibility public.
- R116 (§11) [deep] Malformed batch shape (transfers missing, not an array, empty, >32, entry not an object) → 422 `validation_failed`; unknown handle → 404; self-transfer → 422 `self_payment`; entry errors reported for the first failing entry in input order; entry errors take precedence over insufficient funds; unknown fields ignored.
- R117 (§11) [deep] Affordable iff every wallet's balance after all incoming and outgoing transfers of the batch is ≥0 (netted; order within batch doesn't matter). Otherwise 409 `insufficient_funds`.
- R118 (§11) [deep] All-or-nothing commit; failed validation claims no idempotency key and creates no payment.
- R119 (§11) 201 `{settlement_id, committed_at, payments:[...in input order]}`; members have null `request_id`, `settlement_id` set, and identical `created_at` == `committed_at`.
- R120 (§11) Constituent payments follow ordinary feed visibility (operator sees private ones only if party).
- R121 (§11) Replay → 200 with the original complete response.
- R122 (§11) [deep] Reset/import preserve operator permissions, original payments, requests, settlement membership, retry responses.

## Stage 2

All stage-1 ids stay in force. Where stage 2 changes a stage-1 rule, the stage-2 id overrides it: R172 overrides the balance checks in R74/R85/R117, and R173 overrides the fields in R72.

### Routes and content negotiation
- R123 (S2 routes) Browser screens reachable by URL: `/` (balance, pay form, request form, activity feed), `/requests` (incoming and outgoing, with pay/decline/cancel), `/split`, `/signup`, `/login`, `/authorizations`. Other screens reachable through the UI.
- R124 (S2 routes) [deep] `/requests` and `/authorizations` serve HTML when `Accept` includes `text/html`, JSON otherwise (stage-1 API unchanged). All other API paths keep JSON behaviour. `/`, `/split`, `/signup`, `/login` serve HTML.
- R125 (S2 routes) Every element listed below is found by an exact `data-testid`; extra elements are allowed.
- R126 (S2 assets) [deep] All HTML/CSS/JS/fonts are embedded in the Go binary and served by the service: no CDN, no external request at runtime (network is blocked).

### Product quality (judged by humans and graded UI checks)
- R127 (S2 visual) Coherent, presentation-ready consumer finance product with a calm, trustworthy character. One visual system (typography, spacing, colour, controls, feedback); primary actions obvious.
- R128 (S2 visual) Once holds exist, available funds is the most prominent money value; total and held are visibly secondary.
- R129 (S2 visual) Status, direction (in/out), privacy and money movement are understandable without raw API data. Available, held, pending, loading, success, refused and uncertain states are visually distinct. People, amounts and timestamps are human-formatted; technical ids only where helpful.
- R130 (S2 visual) [deep] Required flows are usable at a 375 CSS-px viewport and at desktop widths with no horizontal page scroll.
- R131 (S2 visual) [deep] Visible input labels, visible keyboard focus, sufficient contrast, designed empty/loading/error states, consistent navigation across all routes.

### Signup and login
- R132 (S2 auth) `/signup`: `signup-email`, `signup-password`, `signup-display-name`, `signup-submit`. `/login`: `login-email`, `login-password`, `login-submit`. Success signs the user in.
- R133 (S2 auth) `auth-error` appears only when there is an error (wrong password, email taken, handle taken, validation), and is absent otherwise.
- R134 (S2 auth) While signed in, `current-user` is visible on every screen and contains the display name; `current-handle` text is exactly the handle (no `@`, no other words).
- R135 (S2 auth) `logout-button` signs out.
- R136 (S2 auth) [deep] The session (token) persists in the browser across navigations to every route, so opening a route directly while signed in works.

### Balance and pay (`/`)
- R137 (S2 pay) `wallet-balance` text is exactly the formatted amount and carries `data-amount="<minor units>"`.
- R138 (S2 pay) [deep] Formatted amount = the decimal with exactly `minor_units` decimal places, one space, the currency code: `100.00 EUR`, `1200 JPY` (no decimal point when units = 0), `1.500 BHD`. No sign. This format applies to every formatted amount on every screen.
- R139 (S2 pay) Pay form: `pay-handle`, `pay-amount` (decimal text), `pay-note`, `pay-visibility` (select with option values `public` and `private`), `pay-submit`, `pay-error`.
- R140 (S2 pay) [deep] Decimal input converts to minor units exactly (no floats): with 2 units, `15.00` and `15` → 1500, `15.5` → 1550. Non-numeric input, or more decimal places than `minor_units` (`15.005`, or `15.5` with 0 units), shows the form's error element and sends no request.
- R141 (S2 pay) `pay-error` shows when a payment is refused (including insufficient funds) and is absent otherwise.
- R142 (S2 pay) [deep] Form values are kept after success. Resubmitting unchanged values sends no new payment: balance falls once, the feed has one payment, `pay-error` absent. Either replay the same key and body (200, no effect) or skip the send. Changing any field makes the next submission a new payment with a new key.
- R143 (S2 pay) Request form: `request-handle`, `request-amount`, `request-note`, `request-submit`, `request-error` (on refusal).

### Activity feed (`/`)
- R144 (S2 feed) `activity-list` children newest first in the DOM; one `activity-item-{payment_id}` per visible payment, with `data-visibility="public|private"`.
- R145 (S2 feed) `activity-parties-{id}` contains both handles; `activity-amount-{id}` is exactly the formatted amount; `activity-note-{id}` is exactly the note and is present even when the note is empty.
- R146 (S2 feed) `empty-activity` is shown instead of the list when nothing is visible.

### Requests (`/requests`)
- R147 (S2 req) Containers `incoming-list` and `outgoing-list`; one `request-item-{id}` per request with `data-status`; `request-amount-{id}` is exactly the formatted amount.
- R148 (S2 req) [deep] `request-pay-{id}` and `request-decline-{id}` appear only on pending incoming requests; `request-cancel-{id}` only on pending outgoing requests.
- R149 (S2 req) `request-error` shows when a pay, decline or cancel is refused; `empty-requests` shows when both lists are empty.

### Split (`/split`)
- R150 (S2 split) `split-amount` (decimal, rules of R140), `split-handles` (comma-separated, in order, whitespace trimmed), `split-note`, `split-submit`, `split-error`.
- R151 (S2 split) [deep] Before posting, `split-preview` holds one `split-share-{handle}` per participant with exactly the formatted share, computed by the §9 rule (R101). The preview matches the server's submitted shares exactly.

### Refresh and uncertain outcomes
- R152 (S2 refresh) After any successful action, the balance, feed and request lists on the same page show the new state without a manual reload. Refresh only after the write succeeds.
- R153 (S2 refresh) The `wallet-refresh` button on `/` refreshes balance and feed without clearing the pay form.
- R154 (S2 refresh) [deep] Latest refresh wins: a delayed earlier read never overwrites a later refresh, even when responses arrive out of order (use a sequence number).
- R155 (S2 conflict) [deep] A refused payment (e.g. another client spent the funds) shows `pay-error`, refreshes the balance and feed, and keeps every pay input.
- R156 (S2 conflict) [deep] Paying a request cancelled elsewhere shows `request-error` and refreshes the request list so the stale pay button disappears.
- R157 (S2 uncertain) [deep] A lost payment response (network error/abort, including after the server committed) shows `pay-uncertain` with non-empty text, not `pay-error`. The unchanged form stays retryable with the SAME Idempotency-Key and body.
- R158 (S2 uncertain) [deep] A successful retry removes `pay-uncertain` and `pay-error`, refreshes balance and feed, and money moves exactly once. An unknown outcome is never shown as a refusal.
- R159 (S2 refresh) The balance refresh rules also apply to available and held. No polling, live sync, or recovery across page reloads required.

### Upgrade from stage 1
- R160 (S2 upgrade) [deep] `POST /_test/import` accepts an export produced by the stage-1 service (format_version 1, the stage-1 state shape without authorization/ttl fields). Missing stage-2 fields default: no authorizations, ttl 600, available = total.
- R161 (S2 upgrade) [deep] A browser signed in before the export/import upgrade stays signed in afterwards (tokens preserved); pending requests stay payable from the request screen.
- R162 (S2 upgrade) [deep] A payment whose response was lost before export stays retryable after import with the same key and body. The UI recovers the original payment (200 replay) and refreshes the imported balance with no page reload. The form and the pending retry identity (key and body) survive in page memory.

### Authorizations: invariants
- R163 (S2 auth-inv) [deep] The sum of wallet `total`s always equals the total seeded by the last reset; a hold moves no money.
- R164 (S2 auth-inv) [deep] `available = total - held` is never negative. Held funds cannot fund new payments, authorizations, request pays or settlement net debits. Captures may spend the money held for them.
- R165 (S2 auth-inv) [deep] Cumulative captures never exceed the authorized amount; each idempotent capture moves money once; a closed hold cannot be captured again.
- R166 (S2 concurrency) [deep] Concurrent requests produce the same results as some serial order, and every invariant holds at every read.

### Authorizations: model and fixture
- R167 (S2 fixture) Fixture `authorization_ttl_seconds` defaults to 600 when omitted; if present it must be a positive integer (otherwise reset → 422 validation_failed, state unchanged). It applies to every API-created authorization: `expires_at` = `created_at` + ttl.
- R168 (S2 fixture) Fixture `authorizations[]` entries: `{id, from_user_id, to_user_id, amount, note, visibility, status, expires_at}` (captured fields optional). Status is open, captured, voided or expired; only open, unexpired entries hold funds. Omitting `authorizations` means an empty list.
- R169 (S2 fixture) [deep] Seeded `balance` is total; available is derived by subtracting seeded, unexpired open holds. If a user's unexpired open holds exceed their balance, reset → 422 `validation_failed` with no state change.
- R170 (S2 expiry) [deep] An authorization with `expires_at` ≤ now is `expired` and holds nothing. Expiry is evaluated lazily on every read and write (no background job needed): `GET /authorizations` shows `status:"expired"` and `GET /me` `available` includes the released remainder. A seeded `open` entry with a past `expires_at` shows as expired on the first read.
- R171 (S2 expiry) [deep] Expiry of a partially captured authorization releases only the remainder and keeps the capture records.

### Changed stage-1 API
- R172 (S2 api) [deep] Every stage-1 `409 insufficient_funds` check (POST /payments, request pay, settlements) now uses `available`; with no holds the result is unchanged. Settlement affordability: for each wallet, available + incoming - outgoing ≥ 0.
- R173 (S2 api) `GET /me` adds `total` (always equal to `balance`), `available` and `held` (the sum of open, unexpired remainders).
- R174 (S2 api) POST /payments stays an immediate transfer with no hold; request pay stays immediate; POST /splits is unchanged.
- R175 (S2 api) Every payment object gains `authorization_id`: null unless the payment was created by a capture. `request_id` and `settlement_id` semantics are unchanged.
- R176 (S2 idem) [deep] POST /authorizations and POST /authorizations/{id}/capture are idempotent write paths (7 in total), each following every §7 rule (R61-R71) independently. New response fields do not change body-equality semantics.

### POST /authorizations
- R177 (S2 authz) The caller is the payer. Body `{to_handle, amount, note?, visibility?}` with the payment defaults. 201 returns the authorization `{authorization_id, from_user_id, from_handle, to_user_id, to_handle, amount, captured_amount, remaining_amount, currency, note, visibility, status, expires_at, payment_id, payment_ids, created_at}`.
- R178 (S2 authz) `available` below amount → 409 `insufficient_funds`; invalid amount → 422; `to_handle` is the caller → 422 `self_payment`; note over 200 chars or bad visibility → 422; unknown handle → 404.
- R179 (S2 authz) An authorization (open, or the hold itself) is never a feed item; only capture payments appear in `/activity`.

### POST /authorizations/{id}/capture
- R180 (S2 capture) Receiver only. Body `{amount?, final?}`: amount defaults to the remaining amount, final is a boolean defaulting to true (a non-boolean `final` → 400 malformed_request, wrong JSON type). 201 returns a payment (payment shape) with `authorization_id` set, `request_id` null, amount = captured amount, and note and visibility copied from the authorization; it follows the feed rule.
- R181 (S2 capture) [deep] A final capture (the default) sets status `captured`, updates `captured_amount` and `payment_id`, and releases the uncaptured remainder in the same step. A second capture → 409 `authorization_not_open`.
- R182 (S2 capture) [deep] With `final:false` and remainder left, status stays `open` and the remainder stays held. Capturing the entire remainder closes it (`captured`) even with `final:false`. `captured_amount` is cumulative, `payment_id` is the latest capture, and `payment_ids` lists every capture in order. `remaining_amount` is the amount still held (0 when closed).
- R183 (S2 capture) [deep] Errors: not open → 409 `authorization_not_open`; `expires_at` ≤ now → 409 `authorization_expired`; amount > remaining → 422 `capture_exceeds_authorization`; amount < 1 or not an integer → 422 `validation_failed`; caller not the receiver (third parties included) → 403; unknown → 404.
- R184 (S2 capture) [deep] A replay must send the identical body: `{}` and `{"amount":2000}` are different, so reusing a key across them → 409 `idempotency_key_reuse`. A replay returns 200 with the original payment even after the authorization closed.

### POST /authorizations/{id}/void
- R185 (S2 void) Payer only, no idempotency key. 200 returns the authorization with `status:"voided"` and the remainder released. Voiding an already-voided authorization → 200 with the current state; a captured or expired one → 409 `authorization_not_open`. Caller not the payer (third parties included) → 403; unknown → 404.
- R186 (S2 void) [deep] Voiding a partially captured (open, final:false) authorization releases only the remainder and keeps the capture records (captured_amount, payment_ids).

### GET /authorizations
- R187 (S2 list) Returns `{authorizations:[...], has_more}` with only the caller's authorizations (as payer or receiver), newest first by created_at.
- R188 (S2 list) `direction`: outgoing (caller is payer), incoming (caller is receiver), or absent. `status`: open, captured, voided or expired. Unknown values → 422. An authorization expired by the clock matches `expired`, never `open`. limit, offset and has_more follow R94 and R46.

### Authorizations UI (`/authorizations` and `/`)
- R189 (S2 ui-auth) `wallet-balance` = formatted total with `data-amount`; `wallet-available` = formatted available with `data-amount`, and it is the headline number; `wallet-held` = formatted held with `data-amount`, absent when held is 0.
- R190 (S2 ui-auth) Authorize form: `authorize-handle`, `authorize-amount`, `authorize-note`, `authorize-visibility`, `authorize-submit`, with the pay-form input rules (R140, R142). `authorize-error` on refusal, including insufficient available funds.
- R191 (S2 ui-auth) `authorization-list` children newest first; `authorization-item-{id}` with `data-status`; `authorization-amount-{id}` exactly the formatted authorized amount; `authorization-captured-{id}` present only when status is captured; `authorization-expires-{id}` text is the RFC 3339 `expires_at`.
- R192 (S2 ui-auth) [deep] `authorization-capture-amount-{id}` (a decimal input pre-filled with the remaining amount) and the `authorization-capture-{id}` button appear only on incoming open authorizations; `authorization-void-{id}` only on outgoing open authorizations.
- R193 (S2 ui-auth) `authorization-error` shows when a capture or void is refused; `empty-authorizations` shows when the list is empty.
- R194 (S2 ui-auth) [deep] The UI reflects seeded and newly created holds and shows available as the spending balance, including immediately after a reset with open holds.

### Export/import extension
- R195 (S2 export) [deep] Export/import also carries authorizations (with capture records), the ttl, and idempotency records for the new paths; R104-R111 apply to all of them.

### Stage-1 reviewer advisories carried forward
- R196 (S2 hardening) [deep] A reset or import racing an in-flight request must not apply a write, or issue a token, for a different user than the one authenticated. Resolve the token (or re-check the user) inside the same critical section as the write; login issues its token atomically with the password check.
- R197 (S2 hardening) Errors from fixture password hashing are returned, not swallowed.

## Stage 3

Every stage-1 and stage-2 id stays in force. Stage 3 adds time-travel reads, statements and corrections. Where a stage-3 id refines an earlier one, the stage-3 id wins: R200 refines R36 and R168; R217 refines R72 and R173 (`balance` is now the current corrected balance).

Glossary for this stage:
- Each payment has revisions r1..rn.
- r1 is `{amount: original amount, effective_at = recorded_at = created_at}`.
- For a `known_at` K, the selected revision of a payment is its latest revision with `recorded_at` ≤ K. If none qualifies, the payment contributes nothing.
- The balance of user U at time T is U's opening balance plus the deltas of every selected revision with `effective_at` ≤ T.

### Payment timestamps and seeding
- R198 (S3 ts) Every payment has `created_at`: an RFC 3339 instant with an offset, the moment it moved money. Every endpoint that returns a payment includes it. `GET /activity` keeps ordering by `created_at` (original).
- R199 (S3 seed) A seeded payment may supply `created_at`. If omitted, it is the reset time, which is earlier than any later API-created payment.
- R200 (S3 seed) [deep] A seeded `created_at` in the future → reset returns 422 `validation_failed` and changes nothing. A `created_at` that is not a valid RFC 3339 instant with an offset → 422.
- R201 (S3 seed) [deep] Fixture `balance` is still the balance after all seeded payments, and loading those payments never changes it. Opening balance = seeded balance − net effect of seeded original payments (received − sent). Corrections never change opening balances. A new account opens at 0.

### GET /me as of an instant
- R202 (S3 me) [deep] `as_of` is optional and must be an RFC 3339 instant with an offset. A naive local time, a bare date, an empty value (`as_of=`) or garbage → 422 `validation_failed`. `known_at` follows the same rule.
- R203 (S3 me) Without `as_of` and `known_at`, /me keeps its current fields and values (current corrected values). The `as_of` key is absent from the response.
- R204 (S3 me) [deep] With `as_of`, `balance` is the balance after every payment with effective time ≤ `as_of` (inclusive) and before every later one. `as_of` at or after the latest payment → the current balance. `as_of` before the earliest payment → the opening balance.
- R205 (S3 me) [deep] The response echoes `as_of` (and `known_at`) exactly as given, character for character, not reformatted.

### GET /statement
- R206 (S3 stmt) `GET /statement?from&to&limit&offset` returns `{opening_balance, entries:[{payment, delta, balance_after, revision, effective_at, recorded_at}], closing_balance, has_more, snapshot}` for the caller's payments in the half-open window [from, to). `from` defaults to the wallet's opening (before everything) and `to` to now. limit and offset follow R94 and R46. `from` and `to` use the R202 instant rules, so invalid or empty → 422.
- R207 (S3 stmt) [deep] Entries are oldest first: by selected `effective_at` ascending, then payment id ascending on ties. Compare ids in a deterministic, documented order and apply it consistently.
- R208 (S3 stmt) [deep] `opening_balance` is the balance immediately before `from`, and `closing_balance` is the balance immediately before `to`. opening + the sum of every delta in the full window = closing. A sent payment's delta is negative, a received one's positive.
- R209 (S3 stmt) [deep] Pagination never changes `balance_after`, opening or closing. They describe the full window regardless of limit and offset (for example, the second page's opening_balance equals the first page's).
- R210 (S3 stmt) [deep] Only payments the caller sent or received appear. Public payments between other users never appear, because feed visibility rules do not apply to statements.
- R211 (S3 stmt) [deep] Each entry's `payment.amount` is the selected revision's amount. Zero-amount revisions still appear as entries with delta 0. A correction is never counted alongside the revision it replaces, because each payment contributes exactly one entry. With no corrections and no `known_at`, behaviour is identical to the uncorrected history.
- R212 (S3 stmt) [deep] `from` > `to` or an empty window gives an empty entry list with opening = closing (no error). Instants in the future are allowed.

### Corrections
- R213 (S3 corr) `POST /payments/{id}/corrections` is an idempotent write path (8 in total). It requires an `Idempotency-Key` and the original sender. An authenticated non-sender → 403 `forbidden`. Unknown payment → 404. No token → 401.
- R214 (S3 corr) [deep] Body `{expected_revision, amount, effective_at, reason}`, every field required:
  - `expected_revision` is an integer ≥ 1.
  - `amount` is an integer from 0 to 1000000000, where 0 reverses the whole payment.
  - `reason` is a string of 1 to 200 characters.
  - `effective_at` is an RFC 3339 instant with an offset, not later than now.
  - A missing or invalid field → 422 `validation_failed`, including a wrong JSON type, by analogy with the amount rule. Follow R45 for amount types.
- R215 (S3 corr) A correction never changes parties or visibility. It appends an immutable revision and returns 201 `{payment_id, revision, amount, effective_at, recorded_at, reason}`. `recorded_at` is assigned by the server, and recorded times for one payment strictly increase (bump by 1 µs or more if the clock ties).
- R216 (S3 corr) [deep] A stale `expected_revision` (not equal to the current latest revision) → 409 `stale_revision`. A replay → 200 with the original revision body, even after newer revisions exist. The same key with a different body → 409 `idempotency_key_reuse`. R71 ordering applies.
- R217 (S3 corr) [deep] The difference from the previous amount moves between the same two wallets in one atomic step. An increase debits the sender; a decrease debits the receiver. Current balances (/me, settlement and payment checks) reflect it immediately.
- R218 (S3 corr) [deep] If the debit is currently unaffordable (against `available`) → 409 `insufficient_funds`. Otherwise, if any user's corrected balance (total, or available — see R231) would be negative at any effective-time boundary under the latest known revisions → 409 `historical_overdraft`. Boundary balances include every movement at that same instant combined. `insufficient_funds` takes precedence.
- R219 (S3 corr) [deep] Any failure preserves balances, revision history, statements and idempotency state (no key is claimed). The sum of balances equals the seeded total in every historical view (any as_of and known_at).
- R220 (S3 corr) [deep] The original payment object and every original idempotent response stay unchanged: a replay of the original POST /payments returns the original amount. `GET /activity` still shows the original payment, and corrections never create feed items.
- R221 (S3 corr) [deep] Concurrent corrections with the same `expected_revision` cannot both succeed: exactly one 201, and the other gets 409 `stale_revision` (or 200 if it is a replay with the same key and body).

### Revisions
- R222 (S3 rev) `GET /payments/{id}/revisions` → `{"revisions":[...]}` in revision order, including r1 (`reason: ""`, amount = original, effective_at = recorded_at = created_at). Each revision has `{payment_id, revision, amount, effective_at, recorded_at, reason}`.
- R223 (S3 rev) [deep] Only the two parties may read revisions. A third party → 404, even for a public payment. Unknown id → 404. No token → 401.

### known_at (bitemporal reads)
- R224 (S3 known) [deep] `GET /me` and `GET /statement` accept optional `known_at`. For each payment, select its latest revision recorded at or before `known_at`. If none exists yet, the payment contributes nothing: no delta and no entry. If omitted, `known_at` means everything known when the read begins. Selected revisions are then applied by their effective times. `as_of` stays inclusive, and the statement window stays half-open. Both instants may be in the future.
- R225 (S3 known) [deep] Statement ordering, entry fields and `payment.amount` use the selected revision (R207, R211). A correction can move a payment into or out of a window.

### Stable statement pagination
- R226 (S3 snap) [deep] Every first `GET /statement` (no snapshot param) returns an opaque `snapshot` token. The token freezes the caller's selected revisions, window, defaulted `to`, balances and entries at that read. `GET /statement?snapshot=T&limit&offset` pages that exact result, even after later payments, corrections or lifecycle events.
- R227 (S3 snap) [deep] Only limit and offset may accompany a snapshot. `from`, `to` or `known_at` with it → 422 `validation_failed`, even when its value is empty. Unknown query parameters are still ignored.
- R228 (S3 snap) [deep] Another user's token, an unknown token, or a token from before a reset → 404 `not_found`. Tokens last until reset; whether they survive import is unspecified (decide: a token is valid after import only if it was exported with the state, and is otherwise 404). The final partial page and offsets past the end report `has_more` correctly (false). Paging never changes balances or entries.

### Settlement and linked payments
- R229 (S3 settle) [deep] Settlement members keep their original receipts and privacy. Each member's r1 uses the shared `committed_at` as both effective_at and recorded_at. A correction of a settlement member → 422 `linked_payment_immutable`. A correction of a capture payment (`authorization_id` set) → 422 `linked_payment_immutable`. Payments created by paying a request are ordinary, so they can be corrected.
- R230 (S3 import) [deep] A stage-3 service accepts exports from this team's stage-1 and stage-2 services. Payments get r1 from created_at; authorizations and captures are accounted for in the ledger and in history; tokens and retries keep working. Opening balances are derived per R201 from the imported state, without being replayed against balances.

### Historical holds
- R231 (S3 holds) [deep] `GET /me?as_of=T&known_at=K` returns all four money fields for that same view: balance = total, available = total − held, and held = the holds open at T as known at K.
- R232 (S3 holds) [deep] Hold lifecycle in time:
  - A hold starts when the authorization is created.
  - A nonfinal capture reduces it at the capture time.
  - A final capture, void or expiry releases the remainder at that event's time. Expiry takes effect at `expires_at`.
  - Events other than clock expiry are known at their server event time. Once creation is known, the expiry deadline is known too.
  - For T beyond now, an open hold expires at its deadline.
  - Without `as_of`, use the instant the request began.
- R233 (S3 holds) Every authorization object exposes `closed_at`: null while open, the close event time when closed (for a clock expiry, `expires_at`).
- R234 (S3 holds) [deep] A correction is rejected with 409 `historical_overdraft` if it makes either total or available negative at any past effective or event boundary, under the latest known revisions. A currently unaffordable debit still gives `insufficient_funds` first.
- R235 (S3 holds) Seeded open holds are assumed created at reset unless the fixture supplies `created_at`, which must not be in the future. Seeded closed holds need not reconstruct a prior lifecycle.
- R236 (S3 holds) [deep] `GET /statement` contains money movements only. Authorization, release and expiry are not entries. A capture appears exactly once, with its links (`authorization_id`). Old snapshots stay unchanged after any lifecycle action or correction.

### Export/import extension
- R237 (S3 export) [deep] Export and import also carry revisions, opening balances, snapshots, closed_at and lifecycle event times, and correction idempotency records. R104-R111 apply to all of them.

### Stage-2 reviewer nits carried forward (non-normative quality)
- R238 (S3 ui) The split preview does not wrap awkwardly at 375 px. The raw RFC 3339 `expires_at` text in authorization rows is visually muted but keeps its `data-testid`.
- R239 (S3 suite) The expired-capture acceptance test requires exactly 409 `authorization_expired`.

## Stage 4

Every id from stages 1-3 stays in force. Where stage 4 refines an earlier id, the stage-4 id wins:
- R250 refines R229: a single correction may target direct and request payments; captures, refunds and settlement members get 422 `linked_payment_immutable`.
- R251 adds the refund floor to R214 and R218.
- R242 adds the `refund_of` field to every payment object (R73).

There are now ten idempotent write paths.

### Refunds
- R240 (S4 refund) `POST /payments/{id}/refunds` with body `{"amount": N}` is an idempotent write path (key required, R61-R71 apply, path 9 of 10).
  - Only the original receiver may refund. Any other authenticated caller → 403 `forbidden`, including the sender and third parties.
  - An unknown payment → 404. No token → 401.
- R241 (S4 refund) [deep] Validation:
  - An invalid amount (below 1, above 1000000000, not an integer, wrong type per R45) → 422 `validation_failed`.
  - A target that is itself a refund → 422 `invalid_refund_target`.
  - Cumulative refunds (existing ones plus this one) exceeding the payment's current corrected amount (its latest revision) → 422 `refund_exceeds_payment`.
  - Valid targets are a direct payment, a request payment, a capture and a settlement member.
  - Precedence: amount validation, then 404/403, then `invalid_refund_target`, then `refund_exceeds_payment`, then `insufficient_funds`.
- R242 (S4 refund) A refund is a new payment in the opposite direction: from the original receiver to the original sender.
  - It has `refund_of` set to the target id, `request_id: null`, `authorization_id: null` and `settlement_id: null`, and copies the original's note and visibility.
  - It returns 201 with the payment (the ordinary payment shape). A replay returns 200 with the identical original body.
  - Every other payment, in every response, carries `refund_of: null`. This includes seeded payments and payments created in stages 1-3.
- R243 (S4 refund) [deep] A refund moves existing money out of the receiver's `available` funds atomically. If available is too low → 409 `insufficient_funds` and nothing changes, with no key claimed.
- R244 (S4 refund) [deep] Refunds never reopen a request or authorization, never restore a released hold, and never change settlement membership. Refunding a settlement member is allowed under the ordinary rules.
- R245 (S4 refund) [deep] A refund is an ordinary payment everywhere else:
  - It is a feed item under the visibility rule.
  - It appears in both parties' statements with r1 (effective_at = recorded_at = created_at).
  - It counts in as_of/known_at reads and in overdraft boundaries.
  - It has revisions, readable by its parties.
  - The sum invariant holds.
- R246 (S4 refund) [deep] Concurrent refunds of one payment can never together exceed its corrected amount: under the one lock at most the remaining amount succeeds, and the others get 422 `refund_exceeds_payment`.

### Corrections with refunds
- R250 (S4 corr) Single corrections (R213-R221) stay available for ordinary direct and request payments. A capture, a refund payment, or a settlement member → 422 `linked_payment_immutable`.
- R251 (S4 corr) [deep] A correction (single or batch item) that would reduce a payment's amount below its cumulative refunded amount → 422 `refund_exceeds_payment`. Equal to the refunded amount is allowed. Correction debits are checked against `available`.

### Batch corrections
- R252 (S4 batch) `POST /correction-batches` is an idempotent write path (path 10 of 10). It requires a settlement operator: no token → 401, a non-operator → 403 `forbidden`. Unknown fields are ignored.
- R253 (S4 batch) [deep] Body `{"corrections":[...]}` must hold 1 to 32 objects with distinct `payment_id`s. Otherwise → 422 `validation_failed`: missing, not an array, empty, more than 32, an entry that is not an object, or a duplicate payment_id.
- R254 (S4 batch) [deep] Each item has the ordinary correction fields and validation (R214): `payment_id`, `expected_revision`, `amount` 0..1e9, `reason` 1..200 characters, and `effective_at` (RFC 3339 with an offset, not later than now). Per-item outcomes:
  - Invalid → 422 `validation_failed`.
  - Unknown payment → 404 `not_found`.
  - Stale expected revision → 409 `stale_revision`.
  - A capture or refund → 422 `linked_payment_immutable`.
  - An amount below the refunded amount → 422 `refund_exceeds_payment`.
- R255 (S4 batch) [deep] The operator may correct any user's ordinary, request and settlement payments, not just their own. Operator status grants no read access to others' revisions or statements.
- R256 (S4 batch) [deep] Correcting any settlement member requires including every member of that settlement; otherwise → 422 `incomplete_settlement`. Members of one settlement must have identical effective instants, compared as instants (`+00:00` and `+02:00` spellings of the same moment are equal); otherwise → 422 `validation_failed`.
- R257 (S4 batch) [deep] Error precedence is strict, and the first failure wins:
  1. Item errors, in input order. Within an item: validation, then 404, then `linked_payment_immutable`, then `stale_revision`, then `refund_exceeds_payment`.
  2. Settlement completeness (`incomplete_settlement`), then the identical-instant check.
  3. Resulting current `available` funds (`insufficient_funds`).
  4. Historical total and available at every effective or event boundary (`historical_overdraft`).
  Affordability uses the combined effect of all proposed revisions together, not one at a time.
- R258 (S4 batch) [deep] A rejected batch leaves history, balances, revisions, statements and idempotency records unchanged, and claims no key.
- R259 (S4 batch) [deep] A success returns 201 `{correction_batch_id, recorded_at, revisions:[...]}`, with revisions in input order.
  - Every new revision shares one `recorded_at`, strictly later than the previous recorded_at of every member.
  - Each revision exposes `correction_batch_id`. Single-correction revisions and r1 carry `correction_batch_id: null`.
  - A replay → 200 with the original batch response. The same key with a different body → 409 `idempotency_key_reuse`.
- R260 (S4 batch) [deep] Original payments and receipts never change. Replays of the original payments and settlements return their original bodies. /activity shows the original amounts.
  - New statements and /me reflect the new revisions. Earlier snapshot tokens page their frozen entries unchanged.
  - GET /payments/{id}/revisions lists batch revisions, with their correction_batch_id.
- R261 (S4 batch) [deep] Concurrent corrections (single or batch) that share any expected payment revision cannot both succeed: exactly one wins and the rest get 409 `stale_revision`.

### Import compatibility
- R262 (S4 import) [deep] A stage-4 service accepts exports from this team's stage-1, stage-2 and stage-3 services. It keeps settlement membership, corrections and revisions, snapshots (still pageable with their frozen entries), tokens and retries. Imported payments get `refund_of: null`. Export and import also carry refunds, refund totals, correction batches and their idempotency records (R104-R111 apply).

### Invariants restated
- R263 (S4 inv) [deep] After any mix of refunds, single corrections and batches, these still hold: the sum of totals equals the seeded total in every historical view; available is never negative now; total and available are never negative at any boundary under the latest known revisions; no payment has refunds exceeding its corrected amount.



## Notes (rejections and fixes)

- Stage 1 was accepted at 0302166 after 1 rejection (gate 5 coverage).
- Stage 2 was accepted at 485278c with 0 rejections.
- Stage 3 was accepted at 08f03d9 with 0 rejections. Before review the suite caught R200 (empty seeded created_at accepted), fixed in 9007ae2.
