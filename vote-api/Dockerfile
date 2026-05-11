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
    cd vote-api && go build -trimpath -ldflags="-s -w \
      -X main.buildVersion=${VERSION:-dev} \
      -X main.buildGitSHA=${GIT_SHA:-unknown} \
      -X main.buildDate=${BUILD_DATE:-unknown}" \
      -o /out/vote-api ./cmd/vote-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vote-api /vote-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/vote-api"]
