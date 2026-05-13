.PHONY: up down logs ps test tidy fmt vet build smoke up-temporal down-temporal smoke-temporal k8s-build k8s-up k8s-smoke k8s-down

up:
	podman compose --profile baseline up -d --build

down:
	podman compose --profile baseline --profile temporal down -v

logs:
	podman compose logs -f --tail=100

ps:
	podman compose ps

test:
	DOCKER_HOST=unix:///run/user/$$(id -u)/podman/podman.sock \
	TESTCONTAINERS_RYUK_DISABLED=true \
	go test ./... -count=1

tidy:
	cd shared                && go mod tidy
	cd vote-api              && go mod tidy
	cd tally-worker          && go mod tidy
	cd tally-worker-temporal && go mod tidy
	cd results-api           && go mod tidy

fmt:
	gofmt -w shared vote-api tally-worker tally-worker-temporal results-api

vet:
	go vet ./...

build:
	podman compose build

smoke:
	./scripts/smoke.sh

up-temporal:
	podman compose --profile temporal up -d --build

down-temporal:
	podman compose --profile temporal down -v

smoke-temporal:
	./scripts/smoke-temporal.sh

.PHONY: k8s-build k8s-up k8s-smoke k8s-down

OVERLAY ?= dev

k8s-build:
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/$(OVERLAY)

k8s-up:
	# Job 'migrate' is immutable; delete it (if present) before applying.
	kubectl -n voting-app delete job/migrate --ignore-not-found
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/$(OVERLAY) | kubectl apply -f -

k8s-smoke:
	HOST=$${HOST:?HOST must be set (Ingress host)} ./scripts/k8s-smoke.sh

k8s-down:
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/$(OVERLAY) | kubectl delete -f - --ignore-not-found
