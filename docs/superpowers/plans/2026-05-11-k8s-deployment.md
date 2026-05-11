# Kubernetes Deployment (GKE) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy the voting-app stack (frontend, vote-api, results-api, tally-worker, Postgres, migrations, Datadog Agent) to a managed GKE cluster, preserving every `CLAUDE.md` invariant.

**Architecture:** Kustomize `base` + `dev`/`prod` overlays. Workloads are: Postgres `StatefulSet` + headless Service, golang-migrate `Job`, three Go `Deployment`s (vote-api, results-api, tally-worker), nginx frontend `Deployment`. External access is via a single GCE Ingress with path-based routing (`/`, `/vote`, `/results`) into ClusterIP Services, with per-Service `BackendConfig` to point the LB health checks at `/healthz`. Observability is via the Datadog Operator's `DatadogAgent` CR (Operator itself bootstrapped via Helm out-of-band).

**Tech Stack:** Kubernetes 1.29+, Kustomize (built into `kubectl`), GKE Ingress (GCE L7), `BackendConfig` / `ManagedCertificate` / `FrontendConfig` (GKE CRDs), Datadog Operator v1 (`DatadogAgent` CR), `postgres:16-alpine`, `migrate/migrate:v4.17.1`, `nginx:1.27-alpine`, the existing distroless service images.

**Source spec:** `docs/superpowers/specs/2026-05-11-k8s-deployment-design.md`.

**Validation cadence:** every YAML task ends with `kustomize build deploy/k8s/overlays/dev | kubectl apply --dry-run=client -f -` returning zero errors. The frontend code change is exercised with a real JS test. Cluster-only validation (the actual `kubectl apply` against GKE + smoke test) is gated behind the final task and called out clearly — it requires a live cluster and Docker Hub account.

---

## File structure

Files to create:

```
deploy/k8s/
├── base/
│   ├── kustomization.yaml
│   ├── namespace.yaml
│   ├── config/
│   │   └── app-config.env                  # input for configMapGenerator
│   ├── postgres/
│   │   ├── statefulset.yaml
│   │   └── service.yaml
│   ├── migrations/
│   │   └── job.yaml                        # ConfigMap built by configMapGenerator
│   ├── vote-api/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   ├── serviceaccount.yaml
│   │   └── backendconfig.yaml
│   ├── results-api/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   ├── serviceaccount.yaml
│   │   └── backendconfig.yaml
│   ├── tally-worker/
│   │   ├── deployment.yaml
│   │   └── serviceaccount.yaml
│   ├── frontend/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   └── backendconfig.yaml
│   ├── ingress/
│   │   └── ingress.yaml
│   └── datadog/
│       └── datadogagent.yaml
└── overlays/
    ├── dev/
    │   ├── kustomization.yaml
    │   └── patches/
    │       ├── ingress-host.yaml
    │       └── replicas-dev.yaml
    └── prod/
        ├── kustomization.yaml
        ├── managedcertificate.yaml
        ├── frontendconfig.yaml
        └── patches/
            ├── ingress-host.yaml
            ├── ingress-tls.yaml
            └── replicas-prod.yaml

scripts/k8s-smoke.sh                         # cluster-side end-to-end smoke
```

Files to modify:

```
frontend/public/index.html                   # voteApi/resultsApi → "" (relative)
frontend/public/app.js                       # no change required; verify it works with cfg.voteApi === ""
Makefile                                     # add k8s-build, k8s-up, k8s-smoke, k8s-down
README.md                                    # add "Deploying to GKE" section (create if missing)
```

---

## Task 1: Frontend — switch API URLs to same-origin

The Ingress will route `/vote` and `/results` to the APIs on the same host as the frontend, so the browser-side `cfg.voteApi` and `cfg.resultsApi` must become empty strings (`"" + "/vote" === "/vote"`). This is a small, pure-frontend change with a real test — do this first so the rest of the plan can assume same-origin.

**Files:**
- Modify: `frontend/public/index.html`
- Create: `frontend/public/app.test.html` (manual-runnable browser test page, optional — see Step 5)

- [ ] **Step 1: Write a failing assertion**

Create `frontend/public/app.test.html` with a tiny harness that loads `app.js` against a mocked `cfg` and asserts the fetch URL is relative:

```html
<!doctype html>
<html>
<head><meta charset="utf-8"><title>app.js test</title></head>
<body>
<div class="choices"><button data-choice="x"></button></div>
<p id="status"></p><ul id="results"></ul><p id="updated"></p>
<script>
  // Mock fetch and config; verify app.js fires the right URLs.
  const calls = [];
  window.fetch = async (url, opts) => {
    calls.push({ url, method: (opts && opts.method) || "GET" });
    return { ok: true, json: async () => ({ results: [], updated_at: "now" }) };
  };
  window.VOTING_CONFIG = { voteApi: "", resultsApi: "", pollId: "default" };
</script>
<script src="/app.js"></script>
<script>
  // app.js calls refreshResults() at module load. Give it a tick, then check.
  setTimeout(() => {
    const ok =
      calls.some(c => c.url === "/results?poll_id=default" && c.method === "GET");
    document.body.dataset.result = ok ? "PASS" : "FAIL: " + JSON.stringify(calls);
  }, 50);
</script>
</body>
</html>
```

- [ ] **Step 2: Verify current `index.html` would make the test fail**

Read `frontend/public/index.html` and confirm lines 24-28 hold `voteApi: "http://localhost:8081"` and `resultsApi: "http://localhost:8082"`. The test page above injects its own `VOTING_CONFIG` *after* loading `app.js`-via-fixture wouldn't see those — but a real browser hitting `index.html` would. Document by recording the expected failure: in `index.html`, the live page sends to `http://localhost:8081/vote`, not `/vote`.

- [ ] **Step 3: Edit `index.html`**

Change lines 24-28 from:

```html
<script>
  window.VOTING_CONFIG = {
    voteApi:    "http://localhost:8081",
    resultsApi: "http://localhost:8082",
    pollId:     "default"
  };
</script>
```

to:

```html
<script>
  window.VOTING_CONFIG = {
    voteApi:    "",
    resultsApi: "",
    pollId:     "default"
  };
</script>
```

`app.js` already does `fetch(cfg.voteApi + "/vote", ...)` and `fetch(cfg.resultsApi + "/results?...")`, so empty strings produce relative paths. No `app.js` change.

- [ ] **Step 4: Verify in a browser**

```bash
cd frontend/public
python3 -m http.server 8000
```

Open `http://localhost:8000/app.test.html` in a browser. `<body data-result="PASS">` means the assertion fired correctly. Expected: PASS.

Also open `http://localhost:8000/` and confirm in the Network tab that the page issues `GET /results?poll_id=default` (no `localhost:8082` hostname).

- [ ] **Step 5: Decide whether to keep the test page**

`app.test.html` is a smoke harness, not a CI test. If you want a permanent regression guard, leave it. Otherwise, remove it and rely on the smoke script (Task 13) for end-to-end coverage. Recommended: delete it — the smoke script catches the same regression cheaper.

```bash
rm frontend/public/app.test.html
```

- [ ] **Step 6: Commit**

```bash
git add frontend/public/index.html
git commit -m "$(cat <<'EOF'
feat(frontend): use relative API URLs for same-origin Ingress

The k8s deployment uses a single Ingress with path-based routing,
so vote-api and results-api are reached at /vote and /results on
the same origin as the frontend. Empty cfg.voteApi/resultsApi makes
app.js form relative paths; compose users can still override by
shipping a different index.html.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Bootstrap kustomize base — namespace + kustomization

Lay down the namespace and an empty base kustomization. Every later task appends to `base/kustomization.yaml`. Keeping it correct after each task means `kustomize build` always succeeds.

**Files:**
- Create: `deploy/k8s/base/namespace.yaml`
- Create: `deploy/k8s/base/kustomization.yaml`
- Create: `deploy/k8s/base/config/app-config.env`

- [ ] **Step 1: Write `namespace.yaml`**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: voting-app
  labels:
    app.kubernetes.io/part-of: voting-app
```

- [ ] **Step 2: Write the config input**

`deploy/k8s/base/config/app-config.env`:

```
DD_ENV=local
DD_VERSION=0.1.0
DD_TRACE_AGENT_PORT=8126
DD_DOGSTATSD_PORT=8125
TALLY_INTERVAL=5s
```

- [ ] **Step 3: Write the base kustomization**

`deploy/k8s/base/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: voting-app

commonLabels:
  app.kubernetes.io/part-of: voting-app

resources:
  - namespace.yaml

configMapGenerator:
  - name: app-config
    envs:
      - config/app-config.env

generatorOptions:
  disableNameSuffixHash: false
```

- [ ] **Step 4: Validate**

```bash
kustomize build deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: prints a `Namespace/voting-app` and a `ConfigMap/app-config-<hash>`, then `namespace/voting-app created (dry run)` and `configmap/app-config-<hash> created (dry run)`. Exit 0.

If `kustomize` isn't on the PATH but `kubectl` is recent (1.21+), use `kubectl kustomize` instead:

```bash
kubectl kustomize deploy/k8s/base | kubectl apply --dry-run=client -f -
```

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/base/namespace.yaml deploy/k8s/base/kustomization.yaml deploy/k8s/base/config/app-config.env
git commit -m "$(cat <<'EOF'
feat(k8s): kustomize base — namespace and app ConfigMap

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Postgres StatefulSet + headless Service

**Files:**
- Create: `deploy/k8s/base/postgres/statefulset.yaml`
- Create: `deploy/k8s/base/postgres/service.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: Write the Service**

`deploy/k8s/base/postgres/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: postgres
  labels:
    app.kubernetes.io/name: postgres
spec:
  clusterIP: None              # headless — stable DNS for the StatefulSet pod
  selector:
    app.kubernetes.io/name: postgres
  ports:
    - name: postgres
      port: 5432
      targetPort: 5432
```

- [ ] **Step 2: Write the StatefulSet**

`deploy/k8s/base/postgres/statefulset.yaml`:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
  labels:
    app.kubernetes.io/name: postgres
spec:
  serviceName: postgres
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: postgres
  template:
    metadata:
      labels:
        app.kubernetes.io/name: postgres
    spec:
      terminationGracePeriodSeconds: 30
      containers:
        - name: postgres
          image: postgres:16-alpine
          ports:
            - name: postgres
              containerPort: 5432
          envFrom:
            - secretRef:
                name: postgres-credentials
          volumeMounts:
            - name: pgdata
              mountPath: /var/lib/postgresql/data
              subPath: pgdata
          readinessProbe:
            exec:
              command:
                - /bin/sh
                - -c
                - pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 3
          livenessProbe:
            exec:
              command:
                - /bin/sh
                - -c
                - pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"
            initialDelaySeconds: 30
            periodSeconds: 30
            timeoutSeconds: 5
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              cpu: 1
              memory: 1Gi
  volumeClaimTemplates:
    - metadata:
        name: pgdata
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
        # storageClassName intentionally omitted — uses cluster default.
        # On GKE Standard/Autopilot that's "standard-rwo".
```

- [ ] **Step 3: Wire into base kustomization**

Modify `deploy/k8s/base/kustomization.yaml`'s `resources:` to add:

```yaml
  - postgres/statefulset.yaml
  - postgres/service.yaml
```

Final `resources:` block:

```yaml
resources:
  - namespace.yaml
  - postgres/statefulset.yaml
  - postgres/service.yaml
```

- [ ] **Step 4: Validate**

```bash
kustomize build deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: lists `namespace/voting-app`, `service/postgres`, `statefulset.apps/postgres`, `configmap/app-config-<hash>` — all "(dry run)". The Secret `postgres-credentials` is referenced but doesn't exist yet — that's fine; `--dry-run=client` doesn't resolve refs.

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/base/postgres deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): Postgres StatefulSet + headless Service

PVC from default StorageClass (gp3/standard-rwo on managed clouds).
Credentials read from postgres-credentials Secret (created out-of-band).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Migrations Job

The migrate Job runs golang-migrate against Postgres on every apply. The migration SQL is shipped as a ConfigMap built by `configMapGenerator` from the existing `migrations/` directory; the hash suffix forces a new Job pod template whenever a `.sql` file changes.

**Files:**
- Create: `deploy/k8s/base/migrations/job.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: Write the Job**

`deploy/k8s/base/migrations/job.yaml`:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate
  labels:
    app.kubernetes.io/name: migrate
spec:
  backoffLimit: 5
  ttlSecondsAfterFinished: 600
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate
    spec:
      restartPolicy: OnFailure
      initContainers:
        - name: wait-for-postgres
          image: postgres:16-alpine
          command:
            - /bin/sh
            - -c
            - |
              until pg_isready -h postgres -p 5432 -U "$POSTGRES_USER"; do
                echo "waiting for postgres..."; sleep 2;
              done
          envFrom:
            - secretRef:
                name: postgres-credentials
      containers:
        - name: migrate
          image: migrate/migrate:v4.17.1
          args:
            - -path=/migrations
            - -database=$(POSTGRES_DSN)
            - up
          env:
            - name: POSTGRES_DSN
              valueFrom:
                secretKeyRef:
                  name: postgres-credentials
                  key: POSTGRES_DSN
          volumeMounts:
            - name: migrations
              mountPath: /migrations
              readOnly: true
      volumes:
        - name: migrations
          configMap:
            name: migrations-sql
```

- [ ] **Step 2: Wire the migrations ConfigMap into the base kustomization**

Append to `configMapGenerator:`:

```yaml
  - name: migrations-sql
    files:
      - ../../../migrations/0001_create_votes.up.sql
      - ../../../migrations/0001_create_votes.down.sql
      - ../../../migrations/0002_create_vote_results.up.sql
      - ../../../migrations/0002_create_vote_results.down.sql
```

Note the relative path: `deploy/k8s/base/` → `../../../migrations/` resolves to the repo-root `migrations/`. Kustomize forbids parent-directory references unless `kustomization.yaml` opts in. Add at the top of the file:

```yaml
# allow loading SQL from repo-root migrations/
```

and run kustomize with `--load-restrictor LoadRestrictionsNone` from the Makefile (see Task 13). The flag is also required at apply time:

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/dev
```

Add the Job to `resources:`:

```yaml
  - migrations/job.yaml
```

Final `kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: voting-app

commonLabels:
  app.kubernetes.io/part-of: voting-app

resources:
  - namespace.yaml
  - postgres/statefulset.yaml
  - postgres/service.yaml
  - migrations/job.yaml

configMapGenerator:
  - name: app-config
    envs:
      - config/app-config.env
  - name: migrations-sql
    files:
      - ../../../migrations/0001_create_votes.up.sql
      - ../../../migrations/0001_create_votes.down.sql
      - ../../../migrations/0002_create_vote_results.up.sql
      - ../../../migrations/0002_create_vote_results.down.sql

generatorOptions:
  disableNameSuffixHash: false
```

- [ ] **Step 3: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: now also prints `job.batch/migrate` and `configmap/migrations-sql-<hash>`. Exit 0.

If you see `error: invalid configmap source` or similar, the relative path is wrong — `pwd` from the repo root and confirm `ls deploy/k8s/base` resolves to four `../../../migrations/*.sql` files.

- [ ] **Step 4: Re-apply behaviour note (no edit, just understanding)**

The Job is named `migrate`. K8s `Job`s are immutable after creation — applying an updated Job with the same name will fail with `field is immutable`. The solution is in Task 12: the dev/prod overlays' Makefile target deletes the Job before applying, OR you rename the Job by including a hash-suffixed config. For this plan we use the **delete-then-apply** approach because it keeps the manifest readable.

No code change in this step; the `make k8s-up` target in Task 13 handles it.

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/base/migrations deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): golang-migrate Job sourcing SQL from migrations/

SQL files are loaded via kustomize configMapGenerator (load restrictor
relaxed so kustomize can reach repo-root migrations/). Job waits for
Postgres via an initContainer pg_isready loop.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: vote-api Deployment, Service, ServiceAccount, BackendConfig

**Files:**
- Create: `deploy/k8s/base/vote-api/serviceaccount.yaml`
- Create: `deploy/k8s/base/vote-api/deployment.yaml`
- Create: `deploy/k8s/base/vote-api/service.yaml`
- Create: `deploy/k8s/base/vote-api/backendconfig.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: ServiceAccount**

`deploy/k8s/base/vote-api/serviceaccount.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vote-api
  labels:
    app.kubernetes.io/name: vote-api
```

- [ ] **Step 2: BackendConfig** (GKE-only CRD; the base ships it; non-GKE overlays would drop it)

`deploy/k8s/base/vote-api/backendconfig.yaml`:

```yaml
apiVersion: cloud.google.com/v1
kind: BackendConfig
metadata:
  name: vote-api
  labels:
    app.kubernetes.io/name: vote-api
spec:
  healthCheck:
    type: HTTP
    requestPath: /healthz
    port: 8080
  timeoutSec: 30
  connectionDraining:
    drainingTimeoutSec: 30
```

- [ ] **Step 3: Service**

`deploy/k8s/base/vote-api/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: vote-api
  labels:
    app.kubernetes.io/name: vote-api
  annotations:
    cloud.google.com/backend-config: '{"default":"vote-api"}'
    cloud.google.com/neg: '{"ingress": true}'
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: vote-api
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

`cloud.google.com/neg: '{"ingress": true}'` opts the Service into container-native load balancing, the modern GKE backend mode. Skipping it works but uses the legacy NodePort instance-group path.

- [ ] **Step 4: Deployment**

`deploy/k8s/base/vote-api/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vote-api
  labels:
    app.kubernetes.io/name: vote-api
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: vote-api
  template:
    metadata:
      labels:
        app.kubernetes.io/name: vote-api
        tags.datadoghq.com/service: vote-api
      annotations:
        ad.datadoghq.com/vote-api.logs: |
          [{"source":"go","service":"vote-api"}]
    spec:
      serviceAccountName: vote-api
      terminationGracePeriodSeconds: 30
      containers:
        - name: vote-api
          image: docker.io/votingapp/vote-api:dev   # overridden per overlay
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: HTTP_ADDR
              value: ":8080"
            - name: POSTGRES_DSN
              valueFrom:
                secretKeyRef:
                  name: postgres-credentials
                  key: POSTGRES_DSN
            - name: DD_SERVICE
              value: vote-api
            - name: DD_AGENT_HOST
              valueFrom:
                fieldRef:
                  fieldPath: status.hostIP
            - name: DD_ENV
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_ENV
            - name: DD_VERSION
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_VERSION
            - name: DD_TRACE_AGENT_PORT
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_TRACE_AGENT_PORT
            - name: DD_DOGSTATSD_PORT
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_DOGSTATSD_PORT
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 2
            periodSeconds: 5
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 500m
              memory: 256Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            capabilities:
              drop: ["ALL"]
```

- [ ] **Step 5: Wire into base kustomization**

Append to `resources:`:

```yaml
  - vote-api/serviceaccount.yaml
  - vote-api/deployment.yaml
  - vote-api/service.yaml
  - vote-api/backendconfig.yaml
```

- [ ] **Step 6: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: adds `serviceaccount/vote-api`, `deployment.apps/vote-api`, `service/vote-api`, and `backendconfig.cloud.google.com/vote-api` to the output. Exit 0.

The `BackendConfig` CRD isn't installed on a non-GKE cluster; `--dry-run=client` doesn't check that (it doesn't talk to the API server), so this still passes.

- [ ] **Step 7: Commit**

```bash
git add deploy/k8s/base/vote-api deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): vote-api Deployment, Service, ServiceAccount, BackendConfig

DD_AGENT_HOST sourced from status.hostIP (DaemonSet-on-node pattern).
BackendConfig points the GCE LB health check at /healthz to avoid the
default-on-/ footgun.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: results-api Deployment, Service, ServiceAccount, BackendConfig

Mirror of Task 5. Spelled out in full — do not "copy Task 5" mentally; the names and labels differ.

**Files:**
- Create: `deploy/k8s/base/results-api/serviceaccount.yaml`
- Create: `deploy/k8s/base/results-api/deployment.yaml`
- Create: `deploy/k8s/base/results-api/service.yaml`
- Create: `deploy/k8s/base/results-api/backendconfig.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: ServiceAccount**

`deploy/k8s/base/results-api/serviceaccount.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: results-api
  labels:
    app.kubernetes.io/name: results-api
```

- [ ] **Step 2: BackendConfig**

`deploy/k8s/base/results-api/backendconfig.yaml`:

```yaml
apiVersion: cloud.google.com/v1
kind: BackendConfig
metadata:
  name: results-api
  labels:
    app.kubernetes.io/name: results-api
spec:
  healthCheck:
    type: HTTP
    requestPath: /healthz
    port: 8080
  timeoutSec: 30
  connectionDraining:
    drainingTimeoutSec: 30
```

- [ ] **Step 3: Service**

`deploy/k8s/base/results-api/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: results-api
  labels:
    app.kubernetes.io/name: results-api
  annotations:
    cloud.google.com/backend-config: '{"default":"results-api"}'
    cloud.google.com/neg: '{"ingress": true}'
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: results-api
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

- [ ] **Step 4: Deployment**

`deploy/k8s/base/results-api/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: results-api
  labels:
    app.kubernetes.io/name: results-api
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: results-api
  template:
    metadata:
      labels:
        app.kubernetes.io/name: results-api
        tags.datadoghq.com/service: results-api
      annotations:
        ad.datadoghq.com/results-api.logs: |
          [{"source":"go","service":"results-api"}]
    spec:
      serviceAccountName: results-api
      terminationGracePeriodSeconds: 30
      containers:
        - name: results-api
          image: docker.io/votingapp/results-api:dev
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: HTTP_ADDR
              value: ":8080"
            - name: POSTGRES_DSN
              valueFrom:
                secretKeyRef:
                  name: postgres-credentials
                  key: POSTGRES_DSN
            - name: DD_SERVICE
              value: results-api
            - name: DD_AGENT_HOST
              valueFrom:
                fieldRef:
                  fieldPath: status.hostIP
            - name: DD_ENV
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_ENV
            - name: DD_VERSION
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_VERSION
            - name: DD_TRACE_AGENT_PORT
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_TRACE_AGENT_PORT
            - name: DD_DOGSTATSD_PORT
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_DOGSTATSD_PORT
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 2
            periodSeconds: 5
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 500m
              memory: 256Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            capabilities:
              drop: ["ALL"]
```

- [ ] **Step 5: Wire into base kustomization**

Append to `resources:`:

```yaml
  - results-api/serviceaccount.yaml
  - results-api/deployment.yaml
  - results-api/service.yaml
  - results-api/backendconfig.yaml
```

- [ ] **Step 6: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: now lists `serviceaccount/results-api`, `deployment.apps/results-api`, `service/results-api`, `backendconfig.cloud.google.com/results-api`. Exit 0.

- [ ] **Step 7: Commit**

```bash
git add deploy/k8s/base/results-api deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): results-api Deployment, Service, ServiceAccount, BackendConfig

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: tally-worker Deployment + ServiceAccount

No `Service`, no `BackendConfig`, no HTTP probe — the worker is internal-only and the demo intent is a single ticker. Replicas stays at 1 (see spec Decisions table).

**Files:**
- Create: `deploy/k8s/base/tally-worker/serviceaccount.yaml`
- Create: `deploy/k8s/base/tally-worker/deployment.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: ServiceAccount**

`deploy/k8s/base/tally-worker/serviceaccount.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tally-worker
  labels:
    app.kubernetes.io/name: tally-worker
```

- [ ] **Step 2: Deployment**

`deploy/k8s/base/tally-worker/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tally-worker
  labels:
    app.kubernetes.io/name: tally-worker
spec:
  replicas: 1
  strategy:
    type: Recreate              # never two workers at once
  selector:
    matchLabels:
      app.kubernetes.io/name: tally-worker
  template:
    metadata:
      labels:
        app.kubernetes.io/name: tally-worker
        tags.datadoghq.com/service: tally-worker
      annotations:
        ad.datadoghq.com/tally-worker.logs: |
          [{"source":"go","service":"tally-worker"}]
    spec:
      serviceAccountName: tally-worker
      terminationGracePeriodSeconds: 30
      containers:
        - name: tally-worker
          image: docker.io/votingapp/tally-worker:dev
          imagePullPolicy: IfNotPresent
          env:
            - name: POSTGRES_DSN
              valueFrom:
                secretKeyRef:
                  name: postgres-credentials
                  key: POSTGRES_DSN
            - name: TALLY_INTERVAL
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: TALLY_INTERVAL
            - name: DD_SERVICE
              value: tally-worker
            - name: DD_AGENT_HOST
              valueFrom:
                fieldRef:
                  fieldPath: status.hostIP
            - name: DD_ENV
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_ENV
            - name: DD_VERSION
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_VERSION
            - name: DD_TRACE_AGENT_PORT
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_TRACE_AGENT_PORT
            - name: DD_DOGSTATSD_PORT
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: DD_DOGSTATSD_PORT
          resources:
            requests:
              cpu: 25m
              memory: 64Mi
            limits:
              cpu: 250m
              memory: 256Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            capabilities:
              drop: ["ALL"]
```

- [ ] **Step 3: Wire into base kustomization**

Append to `resources:`:

```yaml
  - tally-worker/serviceaccount.yaml
  - tally-worker/deployment.yaml
```

- [ ] **Step 4: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: adds `serviceaccount/tally-worker`, `deployment.apps/tally-worker`. Exit 0.

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/base/tally-worker deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): tally-worker Deployment (replicas=1, Recreate strategy)

Single ticker by design — multiple workers race on the same UPSERT
and add DB load without value.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: frontend Deployment, Service, BackendConfig

**Files:**
- Create: `deploy/k8s/base/frontend/deployment.yaml`
- Create: `deploy/k8s/base/frontend/service.yaml`
- Create: `deploy/k8s/base/frontend/backendconfig.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: BackendConfig** (frontend health check is `/`, since nginx serves `index.html` with 200)

`deploy/k8s/base/frontend/backendconfig.yaml`:

```yaml
apiVersion: cloud.google.com/v1
kind: BackendConfig
metadata:
  name: frontend
  labels:
    app.kubernetes.io/name: frontend
spec:
  healthCheck:
    type: HTTP
    requestPath: /
    port: 8080
  timeoutSec: 30
  connectionDraining:
    drainingTimeoutSec: 10
```

- [ ] **Step 2: Service**

`deploy/k8s/base/frontend/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: frontend
  labels:
    app.kubernetes.io/name: frontend
  annotations:
    cloud.google.com/backend-config: '{"default":"frontend"}'
    cloud.google.com/neg: '{"ingress": true}'
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: frontend
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

- [ ] **Step 3: Deployment**

`deploy/k8s/base/frontend/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
  labels:
    app.kubernetes.io/name: frontend
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: frontend
  template:
    metadata:
      labels:
        app.kubernetes.io/name: frontend
    spec:
      terminationGracePeriodSeconds: 10
      containers:
        - name: frontend
          image: docker.io/votingapp/frontend:dev
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          livenessProbe:
            httpGet:
              path: /
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /
              port: http
            initialDelaySeconds: 2
            periodSeconds: 5
          resources:
            requests:
              cpu: 25m
              memory: 32Mi
            limits:
              cpu: 200m
              memory: 128Mi
          # nginx writes pid + temp files at runtime; tmpfs covers it.
          securityContext:
            allowPrivilegeEscalation: false
            runAsNonRoot: false   # nginx:1.27-alpine wants root for port binding
            capabilities:
              drop: ["ALL"]
              add: ["NET_BIND_SERVICE"]
```

`nginx:1.27-alpine` listens on 8080 (>1024) and the existing Dockerfile doesn't set `USER`. Leave `runAsNonRoot: false` here rather than switching to `nginxinc/nginx-unprivileged` — that's a separate change.

- [ ] **Step 4: Wire into base kustomization**

Append to `resources:`:

```yaml
  - frontend/deployment.yaml
  - frontend/service.yaml
  - frontend/backendconfig.yaml
```

- [ ] **Step 5: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: adds `deployment.apps/frontend`, `service/frontend`, `backendconfig.cloud.google.com/frontend`. Exit 0.

- [ ] **Step 6: Commit**

```bash
git add deploy/k8s/base/frontend deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): frontend nginx Deployment, Service, BackendConfig

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Ingress (base — HTTP only, host placeholder)

The base Ingress is HTTP only with a placeholder host. The `prod` overlay layers in `ManagedCertificate`, `FrontendConfig` (HTTP→HTTPS redirect), and the static-IP annotation. The `dev` overlay just patches the host.

**Files:**
- Create: `deploy/k8s/base/ingress/ingress.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: Ingress**

`deploy/k8s/base/ingress/ingress.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: voting-app
  annotations:
    kubernetes.io/ingress.class: gce
spec:
  rules:
    - host: voting.example.com    # patched per overlay
      http:
        paths:
          - path: /vote
            pathType: Prefix
            backend:
              service:
                name: vote-api
                port:
                  number: 8080
          - path: /results
            pathType: Prefix
            backend:
              service:
                name: results-api
                port:
                  number: 8080
          - path: /
            pathType: Prefix
            backend:
              service:
                name: frontend
                port:
                  number: 8080
```

`pathType: Prefix` with `/vote` matches `/vote`, `/vote/foo`, etc. Only `/vote` is served by the API today, so the prefix is fine; tightening to `pathType: Exact` is a future tweak if more paths get added.

Path order in the spec doesn't determine match priority — GCE Ingress uses longest-prefix-wins, so `/vote` and `/results` correctly beat `/`. Listed in that order purely for readability.

- [ ] **Step 2: Wire into base kustomization**

Append to `resources:`:

```yaml
  - ingress/ingress.yaml
```

- [ ] **Step 3: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: adds `ingress.networking.k8s.io/voting-app`. Exit 0.

- [ ] **Step 4: Commit**

```bash
git add deploy/k8s/base/ingress deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): GCE Ingress with path-based routing

/        → frontend
/vote    → vote-api
/results → results-api

Host is a placeholder patched by overlays.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: DatadogAgent CR

The Operator (CRDs + controller) is installed as a one-time bootstrap via Helm; only the CR ships in the repo. The CR lives in the `voting-app` namespace alongside the workloads — it could live in its own namespace, but co-locating keeps the kustomize build single-rooted and the demo simple.

**Files:**
- Create: `deploy/k8s/base/datadog/datadogagent.yaml`
- Modify: `deploy/k8s/base/kustomization.yaml`

- [ ] **Step 1: DatadogAgent CR**

`deploy/k8s/base/datadog/datadogagent.yaml`:

```yaml
apiVersion: datadoghq.com/v2alpha1
kind: DatadogAgent
metadata:
  name: datadog
spec:
  global:
    clusterName: voting-app          # overlays may override
    site: datadoghq.com
    credentials:
      apiSecret:
        secretName: datadog-secret
        keyName: api-key
    tags:
      - demo:voting-app
  features:
    apm:
      enabled: true
      hostPortConfig:
        enabled: true
        hostPort: 8126
    dogstatsd:
      hostPortConfig:
        enabled: true
        hostPort: 8125
      originDetectionEnabled: true
    logCollection:
      enabled: true
      containerCollectAll: true
    liveContainerCollection:
      enabled: true
    orchestratorExplorer:
      enabled: true
```

`hostPort: 8126/8125` exposes the node-local agent on the node's IP, which is what each app pod reaches via `DD_AGENT_HOST=status.hostIP`. Matches the env wiring in Tasks 5/6/7.

- [ ] **Step 2: Wire into base kustomization**

Append to `resources:`:

```yaml
  - datadog/datadogagent.yaml
```

- [ ] **Step 3: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/base | kubectl apply --dry-run=client -f -
```

Expected: adds `datadogagent.datadoghq.com/datadog`. `--dry-run=client` won't validate the CR's schema (no Operator installed locally) but YAML parsing succeeds.

- [ ] **Step 4: Commit**

```bash
git add deploy/k8s/base/datadog deploy/k8s/base/kustomization.yaml
git commit -m "$(cat <<'EOF'
feat(k8s): DatadogAgent CR (Operator-managed)

APM, dogstatsd, log collection, orchestrator explorer. Agent hostPort
binds 8126/8125 so pods reach the node-local agent via status.hostIP.
Operator itself is a cluster bootstrap (Helm); only the CR is in repo.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Dev overlay

The dev overlay sets `DD_ENV=dev`, keeps replicas at 1, and patches the Ingress host to a dev hostname.

**Files:**
- Create: `deploy/k8s/overlays/dev/kustomization.yaml`
- Create: `deploy/k8s/overlays/dev/patches/ingress-host.yaml`
- Create: `deploy/k8s/overlays/dev/patches/app-config.env`

- [ ] **Step 1: Per-env config override**

`deploy/k8s/overlays/dev/patches/app-config.env`:

```
DD_ENV=dev
DD_VERSION=0.1.0
DD_TRACE_AGENT_PORT=8126
DD_DOGSTATSD_PORT=8125
TALLY_INTERVAL=5s
```

- [ ] **Step 2: Ingress host patch**

`deploy/k8s/overlays/dev/patches/ingress-host.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: voting-app
spec:
  rules:
    - host: voting-dev.example.com    # replace with your dev hostname
      http:
        paths:
          - path: /vote
            pathType: Prefix
            backend:
              service:
                name: vote-api
                port:
                  number: 8080
          - path: /results
            pathType: Prefix
            backend:
              service:
                name: results-api
                port:
                  number: 8080
          - path: /
            pathType: Prefix
            backend:
              service:
                name: frontend
                port:
                  number: 8080
```

GCE Ingress strategic-merge against `rules` is finicky — the list element is replaced wholesale, so the patch repeats the full rule.

- [ ] **Step 3: Overlay kustomization**

`deploy/k8s/overlays/dev/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: voting-app

resources:
  - ../../base

# Override the base ConfigMap by regenerating it with dev values.
# behavior=replace makes this overlay-scoped generator fully replace
# the base generator's output (rather than merging).
configMapGenerator:
  - name: app-config
    behavior: replace
    envs:
      - patches/app-config.env

# Pin images for the dev environment.
images:
  - name: docker.io/votingapp/vote-api
    newTag: dev
  - name: docker.io/votingapp/results-api
    newTag: dev
  - name: docker.io/votingapp/tally-worker
    newTag: dev
  - name: docker.io/votingapp/frontend
    newTag: dev

patches:
  - path: patches/ingress-host.yaml
    target:
      kind: Ingress
      name: voting-app
```

- [ ] **Step 4: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/dev | kubectl apply --dry-run=client -f -
```

Expected: full base output, but `Ingress` shows `host: voting-dev.example.com` and `ConfigMap/app-config-<dev-hash>` has `DD_ENV: dev`. Exit 0.

Verify the host shows up:

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/dev | grep "host:"
```

Expected: `      - host: voting-dev.example.com`.

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/overlays/dev
git commit -m "$(cat <<'EOF'
feat(k8s): dev overlay — DD_ENV=dev, replicas=1, host=voting-dev.example.com

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Prod overlay — replicas, ManagedCertificate, FrontendConfig, static IP, TLS

**Files:**
- Create: `deploy/k8s/overlays/prod/kustomization.yaml`
- Create: `deploy/k8s/overlays/prod/managedcertificate.yaml`
- Create: `deploy/k8s/overlays/prod/frontendconfig.yaml`
- Create: `deploy/k8s/overlays/prod/patches/app-config.env`
- Create: `deploy/k8s/overlays/prod/patches/ingress-host.yaml`
- Create: `deploy/k8s/overlays/prod/patches/replicas-prod.yaml`

- [ ] **Step 1: ManagedCertificate**

`deploy/k8s/overlays/prod/managedcertificate.yaml`:

```yaml
apiVersion: networking.gke.io/v1
kind: ManagedCertificate
metadata:
  name: voting-app
spec:
  domains:
    - voting.example.com    # replace with your prod hostname
```

- [ ] **Step 2: FrontendConfig (HTTP → HTTPS)**

`deploy/k8s/overlays/prod/frontendconfig.yaml`:

```yaml
apiVersion: networking.gke.io/v1beta1
kind: FrontendConfig
metadata:
  name: voting-app
spec:
  redirectToHttps:
    enabled: true
    responseCodeName: MOVED_PERMANENTLY_DEFAULT
```

- [ ] **Step 3: Prod app-config**

`deploy/k8s/overlays/prod/patches/app-config.env`:

```
DD_ENV=prod
DD_VERSION=0.1.0
DD_TRACE_AGENT_PORT=8126
DD_DOGSTATSD_PORT=8125
TALLY_INTERVAL=5s
```

- [ ] **Step 4: Ingress patch — host, TLS, static IP, FrontendConfig**

`deploy/k8s/overlays/prod/patches/ingress-host.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: voting-app
  annotations:
    kubernetes.io/ingress.class: gce
    kubernetes.io/ingress.global-static-ip-name: voting-ip
    networking.gke.io/managed-certificates: voting-app
    networking.gke.io/v1beta1.FrontendConfig: voting-app
spec:
  rules:
    - host: voting.example.com
      http:
        paths:
          - path: /vote
            pathType: Prefix
            backend:
              service:
                name: vote-api
                port:
                  number: 8080
          - path: /results
            pathType: Prefix
            backend:
              service:
                name: results-api
                port:
                  number: 8080
          - path: /
            pathType: Prefix
            backend:
              service:
                name: frontend
                port:
                  number: 8080
```

The static-IP name (`voting-ip`) refers to a global IP reserved out-of-band:

```bash
gcloud compute addresses create voting-ip --global
```

Documented in the README (Task 14).

- [ ] **Step 5: Replicas patch**

`deploy/k8s/overlays/prod/patches/replicas-prod.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vote-api
spec:
  replicas: 2
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: results-api
spec:
  replicas: 2
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
spec:
  replicas: 2
```

Note: `tally-worker` stays at 1 — see spec.

- [ ] **Step 6: Overlay kustomization**

`deploy/k8s/overlays/prod/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: voting-app

resources:
  - ../../base
  - managedcertificate.yaml
  - frontendconfig.yaml

configMapGenerator:
  - name: app-config
    behavior: replace
    envs:
      - patches/app-config.env

# Image tags should be pinned to immutable SHAs in real prod;
# placeholders here.
images:
  - name: docker.io/votingapp/vote-api
    newTag: "0.1.0"
  - name: docker.io/votingapp/results-api
    newTag: "0.1.0"
  - name: docker.io/votingapp/tally-worker
    newTag: "0.1.0"
  - name: docker.io/votingapp/frontend
    newTag: "0.1.0"

patches:
  - path: patches/ingress-host.yaml
    target:
      kind: Ingress
      name: voting-app
  - path: patches/replicas-prod.yaml
```

- [ ] **Step 7: Validate**

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/prod | kubectl apply --dry-run=client -f -
```

Expected: full output plus `managedcertificate.networking.gke.io/voting-app` and `frontendconfig.networking.gke.io/voting-app`. The Ingress has annotations `kubernetes.io/ingress.global-static-ip-name`, `networking.gke.io/managed-certificates`, `networking.gke.io/v1beta1.FrontendConfig`. Deployments show replicas=2.

```bash
kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/prod | grep -E "host:|replicas:|ingress.global-static-ip-name|managed-certificates"
```

Expected output includes the static-IP annotation, the managed-cert annotation, and `replicas: 2` for the three app Deployments.

- [ ] **Step 8: Commit**

```bash
git add deploy/k8s/overlays/prod
git commit -m "$(cat <<'EOF'
feat(k8s): prod overlay — ManagedCertificate, FrontendConfig, static IP, replicas=2

API and frontend Deployments scale to 2; tally-worker stays at 1 by design.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Smoke script + Makefile targets

**Files:**
- Create: `scripts/k8s-smoke.sh`
- Modify: `Makefile`

- [ ] **Step 1: Smoke script**

`scripts/k8s-smoke.sh`:

```bash
#!/usr/bin/env bash
# End-to-end smoke against a deployed k8s overlay.
# Requires: kubectl context already pointing at the cluster, manifests applied,
# Job 'migrate' complete.

set -euo pipefail

OVERLAY="${OVERLAY:-dev}"
NAMESPACE="${NAMESPACE:-voting-app}"
HOST="${HOST:?HOST must be set to the Ingress host, e.g. voting-dev.example.com}"
SCHEME="${SCHEME:-http}"
POLL_ID="${POLL_ID:-smoke}"
TALLY_WAIT="${TALLY_WAIT:-12}"   # 2x default TALLY_INTERVAL + slack

echo "==> Smoke against ${SCHEME}://${HOST} (overlay=${OVERLAY}, ns=${NAMESPACE})"

echo "==> Waiting for migrate Job"
kubectl -n "$NAMESPACE" wait --for=condition=complete --timeout=120s job/migrate

echo "==> Waiting for Deployments"
for d in vote-api results-api tally-worker frontend; do
  kubectl -n "$NAMESPACE" wait --for=condition=available --timeout=120s "deployment/$d"
done

echo "==> Casting votes"
for i in 1 2 3 4 5; do
  curl -fsS -X POST "${SCHEME}://${HOST}/vote" \
    -H 'content-type: application/json' \
    -d "{\"poll_id\":\"${POLL_ID}\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
done
for i in 1 2; do
  curl -fsS -X POST "${SCHEME}://${HOST}/vote" \
    -H 'content-type: application/json' \
    -d "{\"poll_id\":\"${POLL_ID}\",\"choice\":\"burritos\",\"user_id\":\"v$i\"}" >/dev/null
done

echo "==> Sleeping ${TALLY_WAIT}s for tally-worker"
sleep "$TALLY_WAIT"

echo "==> Fetching results"
OUT="$(curl -fsS "${SCHEME}://${HOST}/results?poll_id=${POLL_ID}")"
echo "$OUT"

echo "$OUT" | grep -q '"choice":"tacos","count":5' || { echo "FAIL: tacos != 5"; exit 1; }
echo "$OUT" | grep -q '"choice":"burritos","count":2' || { echo "FAIL: burritos != 2"; exit 1; }

echo "==> PASS"
```

Make executable:

```bash
chmod +x scripts/k8s-smoke.sh
```

- [ ] **Step 2: Lint the script**

```bash
bash -n scripts/k8s-smoke.sh
```

Expected: exit 0 (syntax OK).

If `shellcheck` is on the PATH:

```bash
shellcheck scripts/k8s-smoke.sh
```

Expected: no warnings (the script is short and quoting-safe).

- [ ] **Step 3: Makefile targets**

Append to `Makefile`:

```makefile
.PHONY: k8s-build k8s-up k8s-smoke k8s-down

OVERLAY ?= dev

k8s-build:
	kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/$(OVERLAY)

k8s-up:
	# Job 'migrate' is immutable; delete it (if present) before applying.
	kubectl -n voting-app delete job/migrate --ignore-not-found
	kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/$(OVERLAY) | kubectl apply -f -

k8s-smoke:
	HOST=$${HOST:?HOST must be set (Ingress host)} ./scripts/k8s-smoke.sh

k8s-down:
	kustomize build --load-restrictor=LoadRestrictionsNone deploy/k8s/overlays/$(OVERLAY) | kubectl delete -f - --ignore-not-found
```

Also extend the top `.PHONY:` line:

Find:

```makefile
.PHONY: up down logs ps test tidy fmt vet build smoke
```

Change to:

```makefile
.PHONY: up down logs ps test tidy fmt vet build smoke k8s-build k8s-up k8s-smoke k8s-down
```

(The new targets already declare themselves `.PHONY` lower down; the top line is for grep-ability.)

- [ ] **Step 4: Validate the Makefile targets parse and `k8s-build` works locally**

```bash
make k8s-build OVERLAY=dev > /tmp/dev.yaml
echo "---"
head -5 /tmp/dev.yaml
wc -l /tmp/dev.yaml
```

Expected: prints valid YAML to `/tmp/dev.yaml`; first lines are an `apiVersion: v1 Kind: Namespace` block; the file is several hundred lines long.

```bash
make k8s-build OVERLAY=prod > /tmp/prod.yaml
grep -c "^kind:" /tmp/prod.yaml
```

Expected: a positive integer that matches the count of resources from Task 12.

- [ ] **Step 5: Commit**

```bash
git add scripts/k8s-smoke.sh Makefile
git commit -m "$(cat <<'EOF'
feat(k8s): smoke script + Makefile targets

make k8s-build / k8s-up / k8s-smoke / k8s-down. The k8s-up target
deletes the immutable migrate Job before reapplying so re-runs work.
HOST env var is required for k8s-smoke.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: README — "Deploying to GKE" section

**Files:**
- Create or modify: `README.md`

- [ ] **Step 1: Append (or create) the section**

If `README.md` doesn't exist, create it with the section below. If it exists, append.

```markdown
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
   kubectl -n voting-app create secret generic postgres-credentials \
     --from-literal=POSTGRES_USER=voting \
     --from-literal=POSTGRES_PASSWORD="$(openssl rand -base64 24)" \
     --from-literal=POSTGRES_DB=voting \
     --from-literal=POSTGRES_DSN="postgres://voting:$(kubectl -n voting-app get secret postgres-credentials -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d)@postgres:5432/voting?sslmode=disable"
   ```

   (Yes, that nested `kubectl get` is awkward — simpler is to compute the password once in a shell variable and use it in both `POSTGRES_PASSWORD` and `POSTGRES_DSN`.)

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
```

- [ ] **Step 2: Validate the markdown renders**

```bash
# any markdown viewer / pre-commit hook — at minimum:
grep -q "Deploying to GKE" README.md
```

Expected: matches.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs: README section on deploying to GKE

Cluster bootstrap, Secrets creation, image push, static IP reservation,
hostname patches, and the make k8s-* workflow.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Cluster validation (requires GKE + Docker Hub)

This task can only run against a real cluster. Skip if you don't have one — the dry-run validations in Tasks 2-12 give high confidence the manifests are syntactically correct.

- [ ] **Step 1: Run cluster bootstrap from README Task 14**

Operator install, two Secrets, image pushes, (prod only) static IP + hostname edits.

- [ ] **Step 2: Apply the dev overlay**

```bash
make k8s-up OVERLAY=dev
```

Expected: `namespace/voting-app unchanged`, plus resources `created` or `configured` across StatefulSet, Deployments, Services, Ingress, BackendConfigs, DatadogAgent, Job.

- [ ] **Step 3: Wait for everything to come up**

```bash
kubectl -n voting-app wait --for=condition=complete --timeout=180s job/migrate
kubectl -n voting-app get pods
```

Expected: `migrate-<hash>` completed; postgres-0 Running 1/1; vote-api, results-api, tally-worker, frontend Running 1/1.

- [ ] **Step 4: Wait for the Ingress to acquire a public IP**

```bash
kubectl -n voting-app get ingress voting-app -w
```

Expected: `ADDRESS` column populates within ~5 minutes (GCE LB provisioning).

Point your dev hostname's DNS at that IP (or use `--resolve` on curl for a one-off test).

- [ ] **Step 5: Run smoke**

```bash
make k8s-smoke OVERLAY=dev HOST=voting-dev.example.com
```

Expected: prints `==> PASS` and exits 0. The JSON line shows `tacos: 5` and `burritos: 2`.

- [ ] **Step 6: Spot-check Datadog**

In the Datadog UI:
- **APM Services** → `vote-api`, `results-api`, `tally-worker` appear with `env:dev`, recent traces.
- **Metrics Explorer** → metrics tagged `service:vote-api` (or whatever your service emits) show data.
- **Logs** → `service:vote-api env:dev` returns recent JSON logs with `trace_id`/`span_id` fields.

If any of these is missing, the most common cause is the API pods not finding the agent — `kubectl logs` on the datadog-agent DaemonSet and check `DD_AGENT_HOST` resolved to the node IP in the app pod's env.

- [ ] **Step 7: Teardown**

```bash
make k8s-down OVERLAY=dev
```

No commit — this task only runs commands. If anything in the manifests needed adjusting to make cluster apply work, those are separate commits with `fix(k8s):` prefixes.

---

## Self-review notes (post-write)

- **Spec coverage:** every spec section maps to tasks — namespace+config Task 2; Postgres Task 3; migrations Task 4; vote-api Task 5; results-api Task 6; tally-worker Task 7; frontend Task 8; Ingress Task 9; Datadog Task 10; overlays Tasks 11-12; verification Tasks 13-15; README Task 14. Frontend `cfg` edit (called out in the spec as in-scope) is Task 1.
- **Placeholder scan:** placeholder hostnames (`voting.example.com`, `voting-dev.example.com`), Docker Hub org (`<org>` and the default `votingapp`), and the static IP name (`voting-ip`) are intentional — they're documented as the operator's responsibility to fill in and the spec calls them out under Open Items. No "TBD" or "implement later" elsewhere.
- **Type/name consistency:** Service names match Ingress backends (`vote-api`, `results-api`, `frontend`); `BackendConfig` names match the `cloud.google.com/backend-config` annotation on each Service; `ConfigMap` names (`app-config`) and `Secret` names (`postgres-credentials`, `datadog-secret`) are referenced consistently; the Datadog Operator API group / version (`datadoghq.com/v2alpha1`) matches what the current Operator surfaces; `kubernetes.io/ingress.class: gce` matches the GKE default; the `keyName: api-key` on the Datadog secret matches the literal `--from-literal=api-key=...` in the README. The `migrate` Job's immutability is acknowledged and handled by `make k8s-up`'s delete-then-apply.
