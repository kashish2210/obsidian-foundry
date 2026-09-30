# Obsidian Foundry: the factory

> Draft. Sections marked TODO get filled with measured numbers after the submitted run.

A four-seat dark factory for Band Desktop. One human message goes in per run. What comes
out is a service built stage by stage, where each stage is accepted only on independent
evidence: a clean isolated build, the provided checks, and an acceptance suite that a
separate seat wrote from the specification without seeing the code.

## Seats

| Seat | Harness | Model | Owns | Never does |
|---|---|---|---|---|
| coordinator | Claude Code | claude-opus-5-5 | requirement ledger, work items, handoffs, acceptance | write product code or tests |
| implementer | Claude Code | claude-sonnet-5-5 | product source, Dockerfile, RUN.md | accept its own work, edit tests |
| tester | Claude Code | claude-sonnet-5-5 | black-box acceptance suite, one test per ledger id | read product source, fix product code |
| reviewer | Claude Code | claude-opus-5-5 | gate runs, code review, ACCEPT / REJECT | fix code |

Mandates: [`mandates/`](mandates/). They name no endpoint, field or error code, so you
could hand them to a team building something else.

## How a stage flows

```
human task ──> coordinator ── writes LEDGER.md (R1..Rn, [deep] marks)
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
                            3 provided checks, isolated mode
                            4 acceptance suite
                            5 ledger coverage: every id tested or explained
                            6 code review (no check-specific special cases)
                            7 earlier stages still pass
                    │
          REJECT ───┴─── ACCEPT ──> coordinator copies stage forward
       (ledger ids, request,
        expected vs actual)
```

## Design choices, and what they cost

**A requirement ledger before any code.** The provided checks cover 79% of stage 1 but
only 9% of stage 3. A factory that iterates until the provided checks go green stops
exactly where the hidden tests begin. The ledger turns every "must" in the spec into a
numbered id, and `[deep]` marks the edge cases a check suite is least likely to have
shipped. Cost: one extra coordinator turn per stage.

**A tester that never sees the code.** Reviewers that read the implementation tend to
share its assumptions. The tester writes from the spec text alone, so when it disagrees
with the product, that's a real question about the requirements rather than an echo.
Cost: a second implementation-sized context per stage (the tester's).

**Structural correctness over clever code.** The implementer mandate asks for
consistency-critical state to change in one place under one lock or transaction, with
retries recognised before any side effect. For a service with hard invariants under 50
concurrent requests, a simple serialised core is cheaper to get right than a fine-grained
one, and it's easy for the reviewer to verify.

**Reviewer gates stop at the first failure.** It's cheap to fail fast on a build or
start problem, and it keeps rejections short and actionable.

**Loop breaker.** Three rejections on the same ledger id and the coordinator splits or
reassigns the item instead of burning more turns on it.

**Two model tiers.** Opus for the seats that judge (coordinator, reviewer), and Sonnet for
the seats that produce volume (implementer, tester).

## How the factory catches bad work

- A missing requirement shows up as an uncovered ledger id at gate 5.
- A special-cased check shows up at gate 6 (code review) and usually at gate 4, because
  the tester's inputs differ from the provided checks.
- A lost update under concurrency shows up in the tester's invariant assertions over the
  final state.
- A service that needs the network, or a missing asset, shows up at gate 2.
- A regression in an earlier stage shows up at gate 7.

TODO: the real example from the submitted run (the ledger id, what the reviewer
rejected, the fix commit).

## Measured cost and time

| Stage | Wall time | Handoffs | Rejections | Model spend | Result |
|---|---|---|---|---|---|
| 1 | TODO | TODO | TODO | TODO | TODO |
| 2 | TODO | TODO | TODO | TODO | TODO |
| 3 | TODO | TODO | TODO | TODO | TODO |
| 4 | TODO | TODO | TODO | TODO | TODO |

## What we tried that failed

TODO: filled in from the practice runs.

## Stand it up yourself

1. Band Desktop: create four local agent seats (Claude Code) named exactly
   `coordinator`, `implementer`, `tester`, `reviewer`, with the models above. Paste each
   seat's mandate from `mandates/` as its instructions. Give them permission to edit
   files in the result repository, run git and Docker, and run the check harness.
2. Put all four seats in one room. Confirm each `@handle` reaches its seat and gets a
   reply.
3. Prepare a result repository and your task. [`dispatch/`](dispatch/) has ours: the
   only thing that changes between problems is that file.
4. Paste the task to `@coordinator` once. Don't send anything else until the final
   report.
