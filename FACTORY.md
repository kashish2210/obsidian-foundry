# Obsidian Foundry: the factory

A four-seat dark factory for Band Desktop. One human message goes in. What comes out is
a service built stage by stage, where each stage is accepted only on independent
evidence: a clean isolated build, the provided checks, and an acceptance suite that a
separate seat wrote from the specification without seeing the code.

In the submitted run it built all four pocketful stages from a single dispatch:
260 numbered requirements, 4,090 lines of Go, 242 API acceptance tests and 58 browser
tests, with 2 rejections that each changed the work.

## Seats

| Seat | Harness | Model | Owns | Never does |
|---|---|---|---|---|
| coordinator | Claude Code | claude-opus-5-5 | requirement ledger, work items, handoffs, acceptance | write product code or tests |
| implementer | Claude Code | claude-sonnet-5-5 | product source, Dockerfile, RUN.md | accept its own work, edit tests |
| tester | Claude Code | claude-sonnet-5-5 | black-box acceptance suite, one test per ledger id | read product source, fix product code |
| reviewer | Claude Code | claude-opus-5-5 | gate runs, code review, ACCEPT / REJECT | fix code |

Mandates: [`mandates/`](mandates/). They name no endpoint, field or error code, so you
can point them at a different problem by changing only the dispatched task.

## How a stage flows

```
human task ──> coordinator ── writes LEDGER.md (R1..Rn, [deep] marks), commits it
                    │
          ┌─────────┴──────────┐
          v                    v
     implementer            tester            (in parallel, no shared code)
     product code           acceptance/ from the ledger only
          │                    │
          └─────────┬──────────┘
                    v
                reviewer ── 1 clean build
                            2 isolated start (no network, 2 vCPU, 2 GiB)
                            3 provided checks
                            4 acceptance suite
                            5 ledger coverage: every id tested, or a written reason
                            6 code review (no check-specific special cases)
                            7 earlier stages still pass
                    │
          REJECT ───┴─── ACCEPT ──> coordinator copies the folder forward, freezes the old one
       (ledger ids, request,
        expected vs actual)
```

Every stage folder carries its own evidence: `LEDGER.md` (the requirements and a notes
section recording each rejection), `acceptance/` (the tester's suite) and
`acceptance/COVERAGE.md` (why an id can't be tested over HTTP).

## Design choices, and what they cost

**A requirement ledger before any code.** The provided checks cover 79% of stage 1 but
only 9% of stage 3. A factory that iterates until the provided checks are green stops
exactly where the hidden tests start. The coordinator turns every normative statement
into a numbered id and marks the ones a shipped suite is least likely to probe with
`[deep]` (42 of 122 in stage 1, 126 of 260 by stage 4). Cost: one coordinator turn per
stage before any code exists.

**A tester that never sees the code.** A reviewer that reads the implementation tends to
share its assumptions. The tester writes from the spec text alone, so when it disagrees
with the product, that's a real question about the requirements. Cost: a second
implementation-sized context per stage.

**Structural correctness over clever code.** The implementer mandate asks for
consistency-critical state to change in one place under one lock, with retries
recognised before any side effect. For a service with hard invariants under 50
concurrent requests, a serialised core is cheaper to get right and easy for the reviewer
to verify. The result: the money package and the single-lock store are the smallest
parts of the codebase.

**Gates stop at the first failure.** It's cheap to fail fast on a build or start
problem, and it keeps rejections short.

**Loop breaker.** Three rejections on the same ledger id and the coordinator splits or
reassigns the item. It was never triggered in the submitted run.

**Two model tiers.** Opus for the seats that judge (coordinator, reviewer), Sonnet for the
seats that produce volume (implementer, tester).

## How the factory caught bad work (submitted run)

| Stage | What was caught | By | Gate | Fix |
|---|---|---|---|---|
| 1 | The tester's `COVERAGE.md` was never committed: a `coverage.*` pattern in our root `.gitignore` matched it case-insensitively on Windows, so 8 ids had no committed reason, and R26, R41–R44 were exercised but not named | reviewer | 5, ledger coverage | tester force-added the file and named the tests (`400b2ca`) |
| 3 | A seeded record whose timestamp field is present but empty was accepted instead of rejected (R200), and capturing an expired authorization returned the wrong error (R239) | tester's suite, before review | 4 | implementer fix `8a48f41` |
| 4 | Batch correction precedence depended on item order: a complete settlement with mismatched instants listed first hid a later incomplete settlement, so the batch returned the wrong error (R257). The tester's suite only had one settlement per batch | reviewer | 6, code review | two-pass check `f409731`, three-order test `8fee467`, unit test `e4b1a87` |

The stage-4 case is the one the design exists for. Every shipped check passed, and the
tester's suite passed. Only reading the code against the ledger found it.

Known and accepted: with balances near 2^53, the affordability check iterates users in
map order, so which error code is reported could depend on that order. The reviewer
flagged it as non-blocking.

## Measured time

Times come from the git history (first ledger commit to the accepted revision).

| Stage | Ledger ids | Commits | Wall time | Rejections | Provided checks |
|---|---|---|---|---|---|
| 1 | 122 | 5 | 24 min | 1 | claimed stage 1 |
| 2 | 197 | 8 | 6 h 25 min (19 min of commits, then a 6 h pause before acceptance) | 0 | claimed stage 2 |
| 3 | 239 | 6 | 45 min | 0 (the suite caught R200 and R239 before review) | claimed stage 3 |
| 4 | 260 | 8 | 4 h 08 min (3 min to first product commit, then a 3 h 48 min pause before the acceptance suite) | 1 | claimed stage 4 |

Final check, from a fresh clone in isolated mode (internal network, no outbound access,
2 vCPU, 2 GiB), with `harness run --all`: every folder claims its own stage. All
shipped checks pass for every suite up to each folder's number (stage 1: 147, stage 2:
35, stage 3: 6, stage 4: 5), and each folder fails the next stage's suite, as it should.

Model spend: _fill in from Band Analytics before submitting._

## What we tried that failed

- **Short handles.** The mandates first used bare `@coordinator`-style handles. Band
  namespaces handles per account, so the mandates now use the full
  `@account/seat` form. Change the account part when you stand the factory up.
- **Our own `.gitignore`.** A generic `coverage.*` rule silently dropped a required
  evidence file on a case-insensitive filesystem (stage 1 rejection above). Keep the
  root `.gitignore` minimal and let seats commit what their mandate requires.
- **The harness's isolated mode on a Windows host.** It passes a Windows path into a
  Linux container, so isolated runs fail before any test runs. The reviewer ran the
  provided checks in host mode and ran its own isolated start (internal network, 2 vCPU,
  2 GiB) with its own suite. We re-ran isolated mode on all four folders from WSL before
  submitting.

## Stand it up yourself

1. Band Desktop: create four local agent seats running Claude Code, named exactly
   `coordinator`, `implementer`, `tester` and `reviewer`, with the models above. Load
   each seat's file from `mandates/` as its role, and replace the account part of the
   handles with yours.
2. Give the seats auto-accept permission in the result repository, for file edits, git,
   Docker, Go and the check harness. A seat that waits for approval stalls a dark run.
3. Put all four seats in one room and confirm two of them can exchange `@handle`
   messages in both directions.
4. Write your task in the shape of [`dispatch/pocketful.md`](dispatch/pocketful.md): the
   specs, the result repository, stack constraints and the gate commands. That file is
   the only thing that changes between problems.
5. Paste it to the coordinator once. Send nothing else until the final report.

## Commit ids in room.json

After the run, we edited commit messages to remove co-author trailers. File contents
are unchanged (same trees), but the commit ids changed. `room.json` is the unedited
Band download, so it cites the original ids. This table maps them.

<details>
<summary>Original id to current id (30 commits)</summary>

| In room.json | In this repository | Commit |
|---|---|---|
| `c4176d2` | `036b0fd` | Restructure into factory layout: generic mandates, FACTORY.md, dispatch task |
| `e96706c` | `1eaa473` | stage-1: requirement ledger R1-R122 |
| `c3b4f9a` | `3a16845` | stage-1: service core, settlements, export/import (R1-R122) |
| `76a3bc7` | `a7cb4cf` | stage-1 acceptance: black-box Go suite covering R1-R4, R11-R20, R22-R37, R39-R59, R61-R122 (119 tests) |
| `60d8312` | `18e608c` | stage-1 ledger notes: record rejection 1 (gate 5 coverage: R5-R7,R9,R10,R21,R38,R60,R26,R41-R44) |
| `0302166` | `400b2ca` | stage-1 acceptance: commit COVERAGE.md (was gitignored by coverage.*), add explicit tests for R26, R41-R44 |
| `064ff9b` | `cad2fec` | stage-2: requirement ledger R1-R197 (copied R1-R122, appended R123-R197) |
| `9aae7d3` | `56f7ee4` | stage-2: copy accepted stage-1 (0302166) as base |
| `8cdfd20` | `1c3b81a` | stage-2: copy stage-1 acceptance COVERAGE.md (ignored by coverage.* rule) |
| `6335484` | `3c2ce05` | stage-2: authorizations, holds, available/held on /me (R163-R188, R195); session re-check inside write lock and atomic login token (R196); return fixture hashing errors (R197); R172 insufficient_funds on available, R175 authorization_id |
| `62dda67` | `4c438e2` | stage-2: wallet UI (R123-R159, R189-R194): embedded HTML/CSS/JS, content negotiation on /requests and /authorizations |
| `3390822` | `25123c4` | stage-2: UI fixes (empty error slots, icon sizing, expiry wording) for R129, R149, R193 |
| `acba248` | `317c944` | stage-2: fixture numbers reject strings (R167), RUN.md image tag pocketful-s2 |
| `485278c` | `0873084` | stage-2 acceptance: API suite R160, R163-R188, R195 (Go) and browser suite R123-R159, R189-R194 (Playwright); COVERAGE.md for R1-R197; RUN.md acceptance commands |
| `26d624b` | `9562d3d` | stage-3: requirement ledger R1-R239 (copied R1-R197, appended R198-R239) |
| `e4cecfb` | `06f6926` | stage-3: copy accepted stage-2 (485278c) as base |
| `b3e90b8` | `5380162` | stage-3: history model, statements, corrections, historical holds (R198-R237); UI nits (R238) |
| `27082ca` | `b2a7fa6` | stage-3 acceptance: statements, as_of/known_at, corrections, revisions, snapshots, linked payments, stage-1/2 import, historical holds, export (R198-R237); R239 strict expired capture; COVERAGE.md R1-R239 |
| `9007ae2` | `8a48f41` | stage-3: seeded created_at present-but-empty is invalid (R200); capture of an expired authorization is authorization_expired (R239) |
| `08f03d9` | `0b029e4` | stage-3 acceptance: regenerate stage-2 export fixture with 10-year holds so R230 does not depend on wall-clock age |
| `6cce609` | `6ace6c6` | stage-4: requirement ledger R1-R263 (copied R1-R239, appended R240-R263) |
| `77d87ab` | `1139309` | stage-4: copy accepted stage-3 (08f03d9) as base |
| `775e7ad` | `df2c56b` | stage-4: refunds, correction batches, refund_of and correction_batch_id (R240-R263) |
| `ce751e6` | `a79c980` | stage-4 acceptance: refunds, correction batches, imports, invariants (R240-R246, R250-R263); COVERAGE.md R1-R263 |
| `96f1f66` | `1f03c94` | stage-4 ledger notes: record rejection 1 (R257 settlement precedence) |
| `f00f0b0` | `8fee467` | stage-4 acceptance: R257 two-settlement precedence case (completeness of every settlement before the instant rule); gofmt |
| `7c0bd9b` | `f409731` | stage-4: settlement completeness is checked for every settlement before any instant comparison (R256, R257) |
| `0298378` | `e4b1a87` | stage-4: unit test for settlement completeness precedence in batches (R256, R257) |
| `eff3906` | `df67ced` | Submission docs: README, FACTORY with measured results; drop internal plan and coverage ignore rule |
| `64adf86` | `73f8bc9` | FACTORY: record isolated-mode results for all four stages |

</details>
