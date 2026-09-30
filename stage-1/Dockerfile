FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pocketful .

FROM scratch
COPY --from=build /out/pocketful /pocketful
USER 65534:65534
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/pocketful"]
