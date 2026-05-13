# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY vote-api/ ./vote-api/
COPY tally-worker/ ./tally-worker/
COPY tally-worker-temporal/ ./tally-worker-temporal/
COPY results-api/ ./results-api/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd tally-worker && go build -trimpath -ldflags="-s -w" \
      -o /out/tally-worker ./cmd/tally-worker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tally-worker /tally-worker
USER nonroot:nonroot
ENTRYPOINT ["/tally-worker"]
