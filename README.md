# voting-app

A small distributed voting demo built to exercise tracing, metrics, and logs across process boundaries. It's intentionally split into four services so observability signals cross several hops:

- **frontend** — static HTML/JS served by nginx.
- **vote-api** (Go) — write path. `POST /vote` inserts a row into the `votes` table.
- **results-api** (Go) — read path. `GET /results?poll_id=…` reads pre-aggregated rows from `vote_results`.
- **tally-worker** (Go) — a ticker that aggregates `votes` → `vote_results` in a single Postgres transaction (idempotent, runs every `TALLY_INTERVAL`).

Backing services: a single Postgres (two tables, `votes` and `vote_results`), a golang-migrate job for schema, and a Datadog Agent that receives traces (port 8126) and dogstatsd metrics (port 8125) directly from the Go services. Architecture details live in [`voting-app-architecture.md`](voting-app-architecture.md); project conventions live in [`CLAUDE.md`](CLAUDE.md).

## Quick start — docker compose / podman compose

The `Makefile` shells out to `podman compose` by default; substitute `docker compose` if you use Docker — every command is compose v2 compatible.

1. **Copy the env template and fill in your Datadog API key:**

   ```bash
   cp .env.example .env
   $EDITOR .env   # set DD_API_KEY=...
   ```

   `.env` must define **every** key from `.env.example` — `DD_API_KEY`, `DD_SITE`, `DD_ENV`, `DD_VERSION`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, and `TALLY_INTERVAL`. The compose file passes them through to the containers, and the official `postgres` image refuses to start if `POSTGRES_PASSWORD` is empty. Only `DD_API_KEY` needs editing; the rest can keep the defaults shipped in the template.

2. **Build images and bring the stack up:**

   ```bash
   make up        # podman compose up -d --build
   ```

3. **Open the UI** at <http://localhost:8080>, cast some votes, and watch the tallies update.

4. **End-to-end smoke (optional):**

   ```bash
   make smoke     # casts 5 tacos + 2 burritos and asserts the counts
   ```

5. **Tear it all down:**

   ```bash
   make down      # podman compose down -v
   ```

Useful extras: `make logs` tails everything, `make ps` lists containers, `make test` runs the Go suite (requires the Docker/podman socket so testcontainers can spin up Postgres).

The frontend's API calls are relative (`/vote`, `/results`) and nginx proxies them to the sibling services inside the compose network — same routing model as the Ingress in production.

## Deploying to GKE

The `deploy/k8s` directory contains a Kustomize base + `dev` / `prod` overlays. Spec: `docs/superpowers/specs/2026-05-11-k8s-deployment-design.md`.

### One-time cluster bootstrap

1. **Create a GKE cluster** (Standard or Autopilot, 1.29+).

2. **Install the Datadog Operator** via Helm:

   ```bash
   helm repo add datadog https://helm.datadoghq.com
   helm repo update
   kubectl create namespace datadog || true
   helm install datadog-operator datadog/datadog-operator \
     --namespace datadog
   ```

3. **Create the Datadog API key Secret** in the `voting-app` namespace:

   ```bash
   kubectl create namespace voting-app || true
   kubectl -n voting-app create secret generic datadog-secret \
     --from-literal=api-key="$DD_API_KEY"
   ```

4. **Create the Postgres credentials Secret:**

   ```bash
   PG_PASSWORD="$(openssl rand -base64 24)"
   kubectl -n voting-app create secret generic postgres-credentials \
     --from-literal=POSTGRES_USER=voting \
     --from-literal=POSTGRES_PASSWORD="$PG_PASSWORD" \
     --from-literal=POSTGRES_DB=voting \
     --from-literal=POSTGRES_DSN="postgres://voting:${PG_PASSWORD}@postgres:5432/voting?sslmode=disable"
   ```

5. **Push images to Docker Hub** (substitute your org):

   ```bash
   for s in vote-api results-api tally-worker frontend; do
     docker build -t docker.io/<org>/$s:dev -f $s/Dockerfile .
     docker push docker.io/<org>/$s:dev
   done
   ```

   Update `images:` in `deploy/k8s/overlays/dev/kustomization.yaml` (and `prod/`) to point at `<org>` if it differs from the placeholder `votingapp`.

6. **(prod only) Reserve a global static IP:**

   ```bash
   gcloud compute addresses create voting-ip --global
   ```

7. **(prod only) Edit hostnames:** replace `voting.example.com` in `deploy/k8s/overlays/prod/managedcertificate.yaml` and `patches/ingress-host.yaml` with your real domain; similarly `voting-dev.example.com` in the dev overlay.

### Deploy

```bash
make k8s-up OVERLAY=dev          # or OVERLAY=prod
make k8s-smoke OVERLAY=dev HOST=voting-dev.example.com
```

### Teardown

```bash
make k8s-down OVERLAY=dev
```

Postgres data lives on a PVC and survives `k8s-down` by default. Delete the PVC manually if you want a clean slate:

```bash
kubectl -n voting-app delete pvc -l app.kubernetes.io/name=postgres
```
