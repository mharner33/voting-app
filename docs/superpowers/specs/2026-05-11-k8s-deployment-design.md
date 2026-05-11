# Kubernetes deployment for voting-app (GKE)

Status: approved design, not yet implemented.
Date: 2026-05-11.

## Goal

Run the existing voting-app stack (frontend, vote-api, results-api, tally-worker, Postgres, migrations, Datadog Agent) on a managed Google Kubernetes Engine cluster, preserving every architectural constraint listed in `CLAUDE.md` — read/write split, single-transaction worker idempotency, OTel→Datadog telemetry on every service, graceful shutdown, and the "duplicate votes are intentional" schema.

## Non-goals

- Auto-scaling (HPA), pod disruption budgets, network policies — additive; out of scope here.
- Managed Postgres (Cloud SQL) — explicitly deferred; in-cluster `StatefulSet` for this iteration.
- The Temporal worker variant (§4.4.2) — still not implemented at the app layer; no k8s scaffolding for it yet.
- SealedSecrets / external-secrets / SOPS — secrets are created out-of-band via `kubectl create secret generic`.
- CI/CD pipelines, GitOps tooling (Argo, Flux), or release automation.

## Decisions

| Choice | Value | Why |
| --- | --- | --- |
| Target cluster | GKE (managed) | User-selected. |
| Image registry | Docker Hub / public registry | User-selected; no imagePullSecret needed. |
| External access | Single GCE Ingress, path-based routing | One external IP, one TLS cert, cheapest. |
| Postgres | In-cluster `StatefulSet` + PVC | Mirrors compose; self-contained demo. |
| Datadog | Operator + `DatadogAgent` CR | Supported, batteries-included path. |
| Manifest layout | Kustomize base + overlays | No template noise; per-env tag/replica/host overrides. |
| `tally-worker` replicas | 1 (not scaled) | Multiple workers race on the same UPSERT — extra DB load, no value. Deliberate. |

## Directory layout

```
deploy/k8s/
├── base/
│   ├── kustomization.yaml
│   ├── namespace.yaml
│   ├── postgres/                  # StatefulSet, headless Service, ServiceAccount
│   ├── migrations/                # Job + ConfigMap built from migrations/*.sql
│   ├── vote-api/                  # Deployment, Service, ServiceAccount, BackendConfig
│   ├── results-api/               # Deployment, Service, ServiceAccount, BackendConfig
│   ├── tally-worker/              # Deployment, ServiceAccount (no Service)
│   ├── frontend/                  # Deployment, Service, BackendConfig
│   ├── ingress/                   # Ingress, FrontendConfig (prod), ManagedCertificate (prod)
│   ├── datadog/                   # DatadogAgent CR
│   └── config/                    # ConfigMap generator inputs
└── overlays/
    ├── dev/                       # DD_ENV=dev, replicas=1, HTTP only, host = voting-dev.example.com
    └── prod/                      # DD_ENV=prod, API/frontend replicas=2, ManagedCertificate, static IP
```

Datadog Operator (CRDs + controller) is installed via the official Helm chart as a one-time cluster bootstrap and lives outside this repo; only the `DatadogAgent` CR is committed here.

## Architecture

### Path map (single Ingress, one external IP)

```
voting.example.com/         → frontend Service:8080
voting.example.com/vote     → vote-api Service:8080      (POST)
voting.example.com/results  → results-api Service:8080   (GET)
```

The APIs already serve at `/vote` and `/results`, so the Ingress forwards paths as-is — no rewrite/strip needed.

### Required frontend change

`frontend/public/index.html` currently hardcodes:

```js
voteApi:    "http://localhost:8081",
resultsApi: "http://localhost:8082",
```

These become empty strings so `app.js` ends up calling `/vote` and `/results` relative to the page origin. Same-origin removes CORS concerns; `shared/httpx/cors.go` stays in place as defensive middleware but no longer fires preflights in the deployed path. This edit is part of the implementation, not a follow-up.

### Workloads

**Postgres**
- `StatefulSet`, replicas 1, `postgres:16-alpine`.
- `volumeClaimTemplates` requests 10 Gi from the default `standard-rwo` StorageClass.
- Headless `Service` `postgres` on 5432 → DNS `postgres.voting-app.svc.cluster.local`.
- Credentials from Secret `postgres-credentials`.
- `readinessProbe`: `pg_isready -U $POSTGRES_USER -d $POSTGRES_DB` (matches compose).

**Migrations**
- `Job`, `migrate/migrate:v4.17.1`, `restartPolicy: OnFailure`, `backoffLimit: 5`.
- Mounts a ConfigMap built by `kustomize configMapGenerator` from `migrations/*.sql`.
- DSN sourced from `postgres-credentials`.
- Re-runnable: kustomize's hash suffix on the ConfigMap forces a new Job pod when migrations change.
- Documented run order in README: `kubectl apply -k overlays/<env>` → wait for Job `Complete` → traffic.

**vote-api**, **results-api**
- `Deployment`, replicas 1 (dev) / 2 (prod), image from overlay (`docker.io/<org>/vote-api:<tag>`).
- `containerPort: 8080`, ClusterIP `Service` on 8080.
- `livenessProbe`: GET `/healthz`. `readinessProbe`: GET `/readyz`.
- `resources`: requests `cpu: 50m, memory: 64Mi`; limits `cpu: 500m, memory: 256Mi`.
- Env: `HTTP_ADDR=:8080`; `POSTGRES_DSN` from Secret; `DD_SERVICE` per-container; `DD_ENV` and `DD_VERSION` from ConfigMap; `DD_AGENT_HOST` from `status.hostIP`; `DD_TRACE_AGENT_PORT=8126`; `DD_DOGSTATSD_PORT=8125`.
- Pod annotation:
  ```yaml
  ad.datadoghq.com/<container>.logs: |
    [{"source":"go","service":"<service-name>"}]
  ```
- Dedicated `ServiceAccount` per service (positions cleanly for future Workload Identity).
- `BackendConfig` referenced via Service annotation, pointing the LB health check at `/healthz` (see "Networking" below).

**tally-worker**
- `Deployment`, replicas 1 (deliberate — see Decisions table).
- No `Service` (internal-only).
- Env: `POSTGRES_DSN`, `TALLY_INTERVAL` from ConfigMap, DD_* same as APIs.
- No HTTP probe; relies on the existing OTel/log signal to surface failures.

**frontend**
- `Deployment`, replicas 1 (dev) / 2 (prod), existing nginx image.
- ClusterIP `Service` on 8080.
- Probes: GET `/` (nginx serves `index.html` → 200).

All Deployments set `terminationGracePeriodSeconds: 30` to give the Go services room to flush the final OTel span batch on SIGTERM. The services already trap SIGTERM and call `provider.Shutdown(ctx)` per `CLAUDE.md`; k8s sends SIGTERM by default, so no `preStop` hook is needed.

### Networking

- **Ingress class:** GKE's default GCE L7 (`kubernetes.io/ingress.class: gce`). Google-managed external HTTPS LB; NodePort backing is auto-managed by the controller.
- **`BackendConfig` per Service** (vote-api, results-api, frontend) with:
  ```yaml
  healthCheck:
    type: HTTP
    requestPath: /healthz   # frontend uses /
    port: 8080
  ```
  Wired by `cloud.google.com/backend-config: '{"default":"<svc>-bc"}'` annotation on the Service. Without this the LB defaults its health check to `GET /` against the API NodePorts and marks them perpetually unhealthy → 502s. This is a known GKE Ingress footgun.
- **TLS**
  - **Dev overlay:** HTTP only.
  - **Prod overlay:** `ManagedCertificate` CRD for `voting.example.com` plus a `FrontendConfig` to redirect HTTP → HTTPS.
- **Static external IP (prod):** reserved out-of-band (`gcloud compute addresses create voting-ip --global`) and referenced by name via `kubernetes.io/ingress.global-static-ip-name`. Documented in README.
- **CORS:** now same-origin, so browser preflights stop firing. `shared/httpx/cors.go` stays as defensive middleware.

### Config and secrets

- **ConfigMap `app-config`** (kustomize-generated, hash-suffixed):
  ```
  DD_ENV: dev | prod
  DD_VERSION: 0.1.0
  DD_TRACE_AGENT_PORT: "8126"
  DD_DOGSTATSD_PORT: "8125"
  TALLY_INTERVAL: "5s"
  ```
  `DD_SERVICE` is set per-container in each Deployment. `DD_AGENT_HOST` comes from `status.hostIP` via `fieldRef`, not the ConfigMap.
- **Secret `postgres-credentials`** — `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_DSN`. Created via `kubectl create secret generic`. Not in git.
- **Secret `datadog-secret`** — `api-key`, referenced by the `DatadogAgent` CR via `global.credentials.apiSecret.secretName: datadog-secret` / `keyName: api-key`.

### Observability (DatadogAgent CR)

- `features.apm.enabled: true`
- `features.dogstatsd.hostPortConfig.enabled: true` so app pods can reach the node-local agent at `hostIP:8126/8125`. Matches the env wiring above.
- `features.logCollection.enabled: true`, `containerCollectAll: true`.
- `global.clusterName: voting-app-<env>`
- `global.tags: ["demo:voting-app", "env:<env>"]`
- `global.credentials.apiSecret.secretName: datadog-secret`
- Cluster Agent enabled (default) — supplies orchestrator metrics and cluster checks.

The Operator's CRDs + controller are installed once per cluster via Helm (out-of-repo bootstrap). Only the CR ships here.

### CLAUDE.md invariants preserved

- **Read/write split:** vote-api only writes `votes`; results-api only reads `vote_results`. Manifests don't connect either to the other.
- **Worker idempotency:** the tally-worker Deployment runs replicas=1 and its in-process loop continues to execute one Postgres transaction per tick. No k8s-level retry or splitting changes that.
- **OTel→Datadog on every service:** every Go Deployment carries DD_* env vars and the autodiscovery log annotation.
- **Graceful shutdown:** 30 s `terminationGracePeriodSeconds`, no probes that would short-circuit SIGTERM.
- **Duplicate-vote schema:** unchanged — migrations apply the existing SQL files as-is.

## Verification

1. **Smoke script** `scripts/k8s-smoke.sh`:
   - `kubectl wait --for=condition=complete --timeout=120s job/migrate`
   - `kubectl wait --for=condition=available --timeout=120s deploy/{vote-api,results-api,tally-worker,frontend}`
   - `curl -fsS -X POST "$HOST/vote" -d '{"poll_id":"smoke","choice":"a","user_id":"u1"}'` → 2xx
   - Poll `curl -fsS "$HOST/results?poll_id=smoke"` until the choice appears or `2 × TALLY_INTERVAL` elapses.
2. **Makefile targets:** `make k8s-up`, `make k8s-smoke`, `make k8s-down`.
3. **README** "Deploying to GKE" section: cluster bootstrap → install Datadog Operator (Helm) → create Secrets → reserve static IP (prod) → `kubectl apply -k deploy/k8s/overlays/<env>` → run smoke.

## Open items (not blockers)

- Choosing a real hostname for `voting.example.com` is left to the deploying operator; the overlay surfaces it as a kustomize patch target.
- Image org/account on Docker Hub — to be filled in by the operator. The overlays parameterise the image base.

## Out of scope (intentional, listed so future work knows where to plug in)

- HPA, PodDisruptionBudget, NetworkPolicy, PodSecurityAdmission tuning.
- Cloud SQL migration path (would replace the Postgres StatefulSet + add the Cloud SQL Auth Proxy sidecar).
- Temporal variant (§4.4.2) — separate spec when the app code lands.
- GitOps wiring (Argo/Flux), CI image build/push, signed images, image-policy admission.
- SealedSecrets / external-secrets — switch from `kubectl create secret` when an operator is in place.
