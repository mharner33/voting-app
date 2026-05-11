# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY vote-api/ ./vote-api/
COPY tally-worker/ ./tally-worker/
COPY results-api/ ./results-api/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd results-api && go build -trimpath -ldflags="-s -w" \
      -o /out/results-api ./cmd/results-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/results-api /results-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/results-api"]
