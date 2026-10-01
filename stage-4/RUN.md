# Pocketful stage 3 — run

Build and start (no manual setup, no network needed at run time):

```
docker build -t pocketful-s3 . && docker run --rm -e PORT=8080 -p 8080:8080 pocketful-s3
```

Health: `curl http://localhost:8080/health`

## Acceptance suite

API suite (Go): `cd acceptance && BASE_URL=http://localhost:8080 go test -count=1 ./...`

Browser suite (Python Playwright): `cd acceptance && BASE_URL=http://localhost:8080 conda run -n venv python -m pytest ui`
