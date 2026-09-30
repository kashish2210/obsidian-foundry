# Dispatch: pocketful (paste everything below the line to @shrivastavkashish420/coordinator, once)

This is the only human input for the submitted run. Paste it once and send nothing else
until the coordinator posts its final report. "Looks good, continue" counts as steering.

---

@shrivastavkashish420/coordinator You are the lead seat for our factory. Build all four stages of the
pocketful track in order, coordinating @shrivastavkashish420/implementer, @shrivastavkashish420/tester and @shrivastavkashish420/reviewer. Every stage
lives in its own complete, buildable folder.

Workspace root: K:/hackathons
Kickoff package (specs + harness): K:/hackathons/dark-factory-wearedevs
Track: pocketful
Result repository: K:/hackathons/obsidian-foundry
Check output root: K:/hackathons/band-work/checks

Specs, one per stage:
- K:/hackathons/dark-factory-wearedevs/pocketful/spec/stage-1.md
- K:/hackathons/dark-factory-wearedevs/pocketful/spec/stage-2.md
- K:/hackathons/dark-factory-wearedevs/pocketful/spec/stage-3.md
- K:/hackathons/dark-factory-wearedevs/pocketful/spec/stage-4.md

Implement stage N in K:/hackathons/obsidian-foundry/stage-N/. Finish and accept a stage
before starting the next. For stage N+1, copy stage-N/ to stage-(N+1)/ (no nested .git)
and extend the copy. Never change an accepted earlier folder.

Per stage folder the band produces:
- source, a Dockerfile, and RUN.md with the exact command that builds and starts the
  service without manual setup,
- LEDGER.md: the numbered requirement ledger for this stage and all earlier ones,
- acceptance/: the tester's black-box suite with a one-line run command in RUN.md.

Stack constraints (these are ours, not the spec's):
- Go 1.27, standard library first. Any third-party module must be justified in the
  commit message. Multi-stage Dockerfile; small runtime image; the build may fetch
  dependencies, the running container may not reach the network.
- Browser assets (HTML, CSS, JS, fonts) are embedded in the binary. No CDN.
- API acceptance tests in Go (black-box HTTP, service address from BASE_URL).
  Browser acceptance tests may use Python Playwright via `conda run -n venv`.

Gate commands the reviewer runs (Windows host, Docker running). Use a new --out
directory every time, e.g. s1-r1, s1-r2:

    cd K:/hackathons/dark-factory-wearedevs
    conda run -n venv python -m harness run --track pocketful --repo K:/hackathons/obsidian-foundry --stage N --mode isolated --out K:/hackathons/band-work/checks/sN-rX

For stage-N/ the result to aim for is `claimed stage: N`. The next stage's suite is
expected to fail against it. The provided checks are only part of the graded tests, so
a green run is not proof: the ledger and the acceptance suite are.

Commit early and often with messages that name the ledger ids. Do not amend, rebase or
squash. Do not touch README.md, FACTORY.md, mandates/, dispatch/ or room.json.

Post a final report per stage as your mandate describes.
