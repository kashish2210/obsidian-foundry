# Stage 1 acceptance coverage

Run: `cd acceptance && BASE_URL=http://localhost:8080 go test -count=1 ./...`
Tests call `POST /_test/reset` themselves and run sequentially (about 40 s, several tests sleep 1.1 s to separate created_at seconds).
Test names carry the ledger id (`TestR74_...`); a few ids share a test.

## Ledger ids NOT tested black-box

| id | why |
|---|---|
| R5 | Dockerfile/RUN.md deliverable; verified by the harness build, not over HTTP |
| R6 | "no outbound network at run time", image runs standalone with `-e PORT`; needs container control |
| R7 | 2 vCPU / 2 GiB / 60 s start are deployment limits, not observable over HTTP (R8 latency is checked) |
| R9 | restart persistence explicitly not required; nothing to assert |
| R10 | default port 8080 when PORT is unset; the suite gets one address from BASE_URL |
| R21 (never changes) | no endpoint can change a handle; only uniqueness/format via R23/R57 are observable |
| R38 | administrative balance endpoint is out of scope (nothing to test) |
| R60 | password hashing; not observable over HTTP (only that login works and is exact, R52/R56) |

## Readings chosen where the spec is ambiguous

- Empty request body is an unparseable body: 400 `malformed_request` (R47).
- Request bodies `[]`/`null`/scalars for endpoints are not objects: 400 `malformed_request` (R47).
- Settlement entries that are not objects, or an `amount`/`note`/`visibility` of the wrong type, are 422 `validation_failed` (R116 per ledger); this is the one place a wrong JSON type is not 400.
- "Same body" (R68) compares parsed JSON values, so `1e2`, `100.0` and `100` are the same value; adding an unknown field or spelling a default out changes the value -> 409.
- Paying a zero-amount request from a split is legal and moves 0 (R102).
- Import with a garbage `state` object (`{"garbage":true}`) is 422 (R107).
- Non-ASCII email local part (`très@`) accepts either per-character or per-byte underscore replacement (R23).

## Ids covered by tests named after another id

Every other ledger id has a `TestR<id>_...` function. Explicit tests for R26, R41, R42, R43 and R44 are in `ledger_ids_test.go`
(R41 and R42 re-run the broader R61 / R58 tests). Further mappings:

- R26 also: TestR85, TestR86, TestR89, TestR90, TestR3_PayVsDeclineVsCancelRace
- R41 also: TestR61_MissingIdempotencyKey (all five write paths)
- R42 also: TestR58_AuthRequired (every endpoint, six bad-header forms)
- R43 also: TestR86, TestR64, TestR75
- R44 also: TestR46/R49 query tests (TestR94_R46_R49_RequestsPagination, TestR100_R49_R46_ActivityParamValidation), TestR77, TestR20_AmountBoundaries
