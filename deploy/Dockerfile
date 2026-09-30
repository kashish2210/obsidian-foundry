# Must build with NO outbound network: all Go deps come from vendor/.
# Usage: docker build --network=none --build-arg STAGE=stage-1 -f deploy/Dockerfile .
FROM golang:1.26-alpine AS build
ARG STAGE=stage-1
WORKDIR /src
COPY stages/${STAGE}/ ./
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor
RUN go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/service

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
EXPOSE 8080
ENTRYPOINT ["/service"]
