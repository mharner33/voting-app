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
