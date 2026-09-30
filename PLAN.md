# Obsidian Foundry: working plan

**Hackathon:** WeAreDevelopers x BAND, The Dark Factory, `pocketful` track
**Deadline:** Mon Oct 5, 23:59 PDT (Tue Oct 6, 12:29 PM IST)
**Kickoff package:** https://github.com/band-ai/dark-factory-wearedevs (cloned at `K:/hackathons/dark-factory-wearedevs`)

## The rules that change everything

1. **Hand-built code doesn't count.** Every line under `stage-N/` has to come out of the
   Band Desktop room. We (and Claude Code outside the room) only build the *factory*:
   mandates, FACTORY.md, the dispatch task. We never commit into `stage-*/`.
2. **Mandates must be generic.** No endpoint paths, field names or error codes. The
   harness scans for this, and a hit disqualifies us. Run `harness check` after every
   mandate edit.
3. **Don't write to the tests.** Only 79% / 35% / 9% / 16% of stages 1–4 are shipped.
   Judging uses the full set, so we build to the spec, not to green checks.
4. **The submitted run is hands-off.** One dispatch message, then nothing until the
   final report. Practice runs can be steered as much as we like.

So the Rust oracle from the first plan is gone: a hand-written, track-specific checker
would make the factory less generic, and the band can't be the one who wrote it. Its job
now belongs to the **tester** seat, which writes a black-box suite from the spec for
every stage.

## Repository layout (required by the judges)

```
obsidian-foundry/
├── README.md          team, track, how to read the repo            (we write, last day)
├── FACTORY.md         seats, design, costs, failure handling       (drafted, fill numbers)
├── mandates/          coordinator, implementer, tester, reviewer   (done, generic)
├── dispatch/          the task we paste to @coordinator            (done)
├── room.json          full session download from the Band console  (after the run)
├── stage-1/ .. 4/     written by the band only: source, Dockerfile, RUN.md, LEDGER.md, acceptance/
└── PLAN.md            this file
```

## Local workspace

| Path | What |
|---|---|
| `K:/hackathons/dark-factory-wearedevs` | kickoff: specs, harness (run it from here) |
| `K:/hackathons/band-work/checks` | harness `--out` dirs (a new name every run) |
| `K:/hackathons/obsidian-foundry` | the real result repo (the final run goes here) |

Harness: `conda run -n venv python -m harness ...` (deps + Chromium installed).

## Timeline

| Day | Goal |
|---|---|
| Oct 1 | Band Desktop set up, 4 seats, @handle round trip works. First pocketful run (a trial: steering allowed). |
| Oct 2 | Fix what the trial exposed (permissions, handoffs, paths), wipe stage-*/, rerun. |
| Oct 3 | Tune mandates from the practice runs (keep them generic!). Measure time and cost per stage. |
| Oct 4 | **The submitted run**: fresh room, the real repo, one dispatch, hands off. Record the room. |
| Oct 5 | Download room.json, harness check + isolated `--all` on a fresh clone, README/FACTORY numbers, video, slides, submit. |

## Human-only steps (Band Desktop)

1. Install Band Desktop, sign in at app.band.ai, and join the hackathon.
2. Create 4 local agent seats (Claude Code): `coordinator`, `implementer`, `tester`,
   `reviewer`. Paste each mandate as that seat's instructions. Set the model per the
   mandate header (change the header if Band shows a different id).
3. Give the seats permissions for file edits in the result repo, git, docker, go and
   `conda run`.
4. Make one room, add all 4, and do a manual @handle hello in both directions.
5. Paste `dispatch/pocketful.md` to the coordinator and watch.
