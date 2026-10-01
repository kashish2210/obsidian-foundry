# Obsidian Foundry

Our entry for the **WeAreDevelopers x BAND "Dark Factory" hackathon**, `pocketful` track.

A four-seat software factory in Band Desktop (coordinator, implementer, tester and
reviewer) built a Venmo-style wallet service in four stages from one dispatched task.
Money is never created, destroyed or spent twice, under concurrent transfers, retries
and rounding.

**Team:** Vikas, Kashish, Dhruv

## Demo video

<!-- VIDEO: replace this line with the video link or embed -->

## Demo users

The [`deploy`](../../tree/deploy) branch build loads these users on start. Every user's
password is `demo-pass-1`.

| Name | Email | Handle | Starting balance |
|---|---|---|---|
| Ada | `ada@example.com` | `@ada` | 120.00 EUR |
| Bob | `bob@example.com` | `@bob` | 45.00 EUR |
| Cy | `cy@example.com` | `@cy` | 30.00 EUR |

Also seeded: Bob has a pending 12.00 EUR taxi request to Ada, a public 5.00 EUR coffee
payment from Ada to Bob, and a private 12.50 EUR concert payment from Cy to Ada. Data
resets to this on every restart.

## How to read this repository

| Path | What it is |
|---|---|
| [`FACTORY.md`](FACTORY.md) | The factory: seats, design choices, what it caught, time, how to stand it up |
| [`mandates/`](mandates/) | One mandate per seat, each naming its harness and model |
| [`dispatch/pocketful.md`](dispatch/pocketful.md) | The single task we gave the coordinator, the only human input |
| [`room.json`](room.json) | The full Band room session the code came out of |
| [`stage-1/`](stage-1/) | JSON API: payments, requests, splits, feed, idempotency, settlements, export/import |
| [`stage-2/`](stage-2/) | Stage 1 + the browser product, holds and partial captures, recovery from lost responses |
| [`stage-3/`](stage-3/) | Stage 2 + immutable corrections, point-in-time history, snapshot-stable statements |
| [`stage-4/`](stage-4/) | Stage 3 + refunds and atomic batch corrections |

Everything under `stage-*/` was written by the band in the room. We wrote the mandates,
the dispatch task and these two documents.

Each stage folder is a complete service on its own and holds:

- `Dockerfile` and `RUN.md`: build and start it with one command,
- `LEDGER.md`: every requirement as a numbered id, plus the notes on rejections and fixes,
- `acceptance/`: the tester's black-box suite (Go for the API, Playwright for the UI) and
  `COVERAGE.md`.

## Run it

```sh
cd stage-4
docker build -t pocketful-s4 . && docker run --rm -e PORT=8080 -p 8080:8080 pocketful-s4
```

Then open http://localhost:8080. The service starts empty. Load users with
`POST /_test/reset` (the fixture format is in the stage-1 spec, §4), or sign up in the UI.

## Stack

Go standard library, in-memory state behind a single lock, a static binary in a
`scratch` image (about 3 MB), with the UI embedded in the binary. No network at run time.
