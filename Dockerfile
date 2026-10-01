FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pocketful .

# alpine (not scratch) so the start script can load demo data after boot.
FROM alpine:3.22
COPY --from=build /out/pocketful /app/pocketful
COPY demo/fixture.json /app/demo/fixture.json
COPY start.sh /app/start.sh
RUN chmod +x /app/start.sh
USER 65534:65534
# Render sets PORT; 8080 is the local default.
ENV PORT=8080 SEED_DEMO=1
EXPOSE 8080
ENTRYPOINT ["/app/start.sh"]
