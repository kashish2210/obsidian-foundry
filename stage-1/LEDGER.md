# Pocketful requirement ledger

Source spec: `K:/hackathons/dark-factory-wearedevs/pocketful/spec/stage-1.md`.
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

## Notes (rejections and fixes)

_None yet._
