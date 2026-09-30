# Obsidian Foundry

**Hackathon:** WeAreDevelopers x BAND — The Dark Factory (lablab.ai)
**Build window:** Sep 26 – **Oct 5, 2026** (4 days left as of Sep 30, so this one comes first)
**Track:** `pocketful` (wallet / payments), with the rule "money must never be created, destroyed or spent twice"
**Team:** Vikas, Kashish, Dhruv

---

## 1. What we're actually building

The challenge isn't the wallet app itself. It's the **factory** that builds it. In BAND Desktop we set up at least 3 coding-agent seats that plan, build, review and verify the service one stage at a time without us steering each step.

Our angle: **a factory that proves correctness instead of just claiming it.** Most teams will have a reviewer agent that reads code and says "looks good". We add a separate, deterministic **Rust oracle** that:

- keeps a reference model of the ledger in memory,
- hits the service with thousands of randomised operations from N concurrent clients, including retries and duplicate idempotency keys,
- checks conservation of money, no negative balances and no double-spends at every read,
- fails the stage if anything drifts, so the verifier seat sends the work back to the builder.

That maps straight onto the judging split: factory design 50%, app quality 25%, agent teamwork 25%.

### Why pocketful over tablekeeper
Money invariants are easy to state exactly (the sum of all balances is constant, apart from explicit mint and burn), so the oracle can be strict and the demo is clear. Double-booking under time zones is fuzzier to verify in 4 days.

---

## 2. Stack (and why)

| Layer | Choice | Why |
|---|---|---|
| Service (built by agents) | **Go**, stdlib `net/http`, `database/sql` + SQLite (`modernc.org/sqlite`, pure Go) | Builds fast, one static binary, and `go mod vendor` makes the no-network container build trivial. No framework for agents to misuse. |
| Oracle / verifier | **Rust** (tokio, reqwest, proptest) | Property-based testing plus real concurrency. The type system keeps the reference model honest. |
| Container | Multi-stage Docker, `--network=none` build, distroless runtime | Matches the "clean container, no outbound network" rule exactly. |
| Factory config | Plain files: `seats.toml`, generic mandates in markdown | The rules require generic mandates, and plain files are easy to show in the video. |

**Hard rule we can't break:** mandates stay generic. No "wallet", no endpoint names, no field names in `factory/mandates/*`. Track-specific stuff only lives in `factory/brief/` (the kickoff brief) and in what the planner seat derives from it.

---

## 3. Folder structure (follow this)

```
obsidian-foundry/
├── PLAN.md                     <- this file
├── factory/
│   ├── seats.toml              <- seat -> mandate -> harness/model mapping
│   ├── mandates/               <- GENERIC role mandates (planner/builder/reviewer/verifier)
│   ├── agreements/             <- shared working agreement, definition of done
│   ├── brief/                  <- kickoff brief + stage specs from the organisers (track-specific)
│   ├── dispatch/               <- tasks the planner dispatches, one file per stage
│   └── tools/guard/pre-commit  <- blocks commits outside the current stage folder
├── oracle/                     <- Rust invariant oracle (we write this by hand)
│   ├── Cargo.toml
│   └── src/{main,model,invariants,workload}.rs
├── stages/
│   └── stage-N/                <- agent-written Go service per stage (go.mod, vendor/, cmd/service)
├── deploy/
│   ├── Dockerfile              <- offline build, distroless runtime
│   └── compose.yaml
├── scripts/offline-build.sh    <- proves the no-network build
└── .github/workflows/ci.yml
```

Ownership rule: **we write `factory/`, `oracle/`, `deploy/`. Agents write `stages/`.** If a human edits `stages/`, the demo loses its point.

---

## 4. Seats

| Seat | Job | Hands off to |
|---|---|---|
| planner | Reads the brief, splits the stage into tasks, writes `dispatch/stage-N.md` | builder |
| builder | Implements the tasks in `stages/stage-N/` | reviewer |
| reviewer | Code review: readability, error handling, API shape | builder (fix) or verifier |
| verifier | Runs the offline build and the oracle, and rejects on any invariant break | builder (fix) or done |

It's a good idea to use two different model families across the seats (one for builder, another for reviewer and verifier), so the reviewer doesn't share the builder's blind spots.

---

## 5. Plan (4 days)

| Day | Goal |
|---|---|
| Sep 30 | Read the kickoff brief into `factory/brief/`, set up BAND Desktop, finalise the seats and generic mandates |
| Oct 1 | Oracle MVP: model + conservation invariant + concurrent workload. Stage 1 run end to end |
| Oct 2 | Offline Docker build green, guard hook, stage 2 |
| Oct 3 | Harden: idempotency / retry scenarios in the oracle, a planted-bug test (to prove the verifier catches it) |
| Oct 4 | More stages if stable, polish UI, **record the BAND room** (a missing recording disqualifies us) |
| Oct 5 | Submit: public repo, video, description |

Suggested split: one person on the factory (seats, mandates, BAND), one on the oracle (Rust), one on the container, CI, recording and submission.

---

## 6. Submission checklist

- [ ] At least 3 distinct seats with generic mandates
- [ ] Public GitHub repo, at least Stage 1 complete
- [ ] Video including the BAND Desktop room recording
- [ ] `docker build --network=none` passes from clean
- [ ] Planted-bug demo: verifier catches it and the builder fixes it

## 7. Toolchain

- Go 1.26+, Rust stable (edition 2024), Docker Desktop.
- `go mod vendor` inside every stage before commit, and commit `vendor/`.
