.PHONY: up down logs ps test tidy fmt vet build smoke

up:
	podman compose up -d --build

down:
	podman compose down -v

logs:
	podman compose logs -f --tail=100

ps:
	podman compose ps

test:
	DOCKER_HOST=unix:///run/user/$$(id -u)/podman/podman.sock \
	TESTCONTAINERS_RYUK_DISABLED=true \
	go test ./... -count=1

tidy:
	cd shared      && go mod tidy
	cd vote-api    && go mod tidy
	cd tally-worker && go mod tidy
	cd results-api && go mod tidy

fmt:
	gofmt -w shared vote-api tally-worker results-api

vet:
	go vet ./...

build:
	podman compose build

smoke:
	./scripts/smoke.sh
