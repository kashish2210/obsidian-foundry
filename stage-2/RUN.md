# Pocketful stage 2 — run

Build and start (no manual setup, no network needed at run time):

```
docker build -t pocketful-s2 . && docker run --rm -e PORT=8080 -p 8080:8080 pocketful-s2
```

Health: `curl http://localhost:8080/health`

## Acceptance suite

`cd acceptance && BASE_URL=http://localhost:8080 go test -count=1 ./...`
