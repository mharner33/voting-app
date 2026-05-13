# Runtime-Configurable Voting Choices — Design

**Date:** 2026-05-13
**Status:** Approved for implementation planning
**Scope:** Allow the voting-app frontend to render its vote buttons, poll id, and prompt heading from environment variables read at container start, without rebuilding the image.

## 1. Goal

Today the vote buttons are hardcoded in `frontend/public/index.html` (`tacos`, `burritos`) and the `pollId` is hardcoded in an inline `window.VOTING_CONFIG` block. Changing any of these requires editing source and rebuilding the frontend image. The goal is to drive all three from env vars supplied at container start, so the same image can serve different demos (lunch poll, dinner poll, etc.) just by changing values in `.env` and restarting the `frontend` service.

## 2. Constraints carried in from `CLAUDE.md` and existing code

- **`vote-api` stays permissive.** The architecture intentionally accepts any non-empty `choice` string and does not enforce a list (`vote-api/internal/handler/vote.go:49`, `CLAUDE.md` "Duplicate votes are intentional"). Server-side validation of which choices are allowed is **explicitly out of scope** — the frontend is the only thing that knows the configured list.
- **Configuration is environment-variable driven** (`CLAUDE.md` "Configuration is environment-variable driven"). New config follows the same pattern; nothing gets hardcoded into images.
- **Frontend remains static HTML/JS** (`CLAUDE.md` "static HTML/JS served by nginx"). No build tooling, no framework, no Node runtime in the container.
- **Same image must work under docker-compose and k8s.** The k8s deployment (`deploy/k8s/base/frontend/deployment.yaml`) uses the same image; supplying env vars via `Deployment.spec.template.spec.containers[].env` must be sufficient — no source changes required for k8s.

## 3. Decisions (locked during brainstorming)

| Decision | Choice |
|---|---|
| Where the frontend reads config from | A small `GET /config.json` endpoint served by nginx; `app.js` fetches it on load. **Not** envsubst on `index.html` directly. |
| Where `/config.json` comes from | The frontend container's entrypoint generates `/usr/share/nginx/html/config.json` from env vars at startup, then execs nginx. |
| Choice shape | Plain strings (comma-separated in the env var). Button label and the value POSTed to `vote-api` are the same string. No display-label/value pairs, no JSON in env vars. |
| Config scope | Three settings: `VOTING_CHOICES`, `VOTING_POLL_ID`, `VOTING_HEADING`. |
| Server-side validation of choices | **None.** `vote-api` continues to accept any non-empty string. |

## 4. Architecture

```
.env (VOTING_CHOICES, VOTING_POLL_ID, VOTING_HEADING)
   │
   ▼
docker-compose.yml frontend.environment   ← or k8s Deployment env
   │
   ▼
docker-entrypoint.sh                      ← runs once per container start
   │   builds JSON from env vars
   ▼
/usr/share/nginx/html/config.json         ← written before nginx execs
   │
   ▼  GET /config.json
app.js  → renders <button>s from choices[],
          sets heading text,
          uses poll_id for /vote POST and /results GET
```

### 4.1 Entrypoint script (new)

`frontend/docker-entrypoint.sh` — pure POSIX shell, no extra packages, runs as the image's existing ENTRYPOINT-replacement before nginx starts:

```sh
#!/bin/sh
set -e

: "${VOTING_CHOICES:=tacos,burritos}"
: "${VOTING_POLL_ID:=default}"
: "${VOTING_HEADING:=Default poll: choose one.}"

choices_json=""
IFS=','
for c in $VOTING_CHOICES; do
  [ -n "$choices_json" ] && choices_json="$choices_json,"
  choices_json="$choices_json\"$c\""
done

cat > /usr/share/nginx/html/config.json <<EOF
{"choices":[$choices_json],"poll_id":"$VOTING_POLL_ID","heading":"$VOTING_HEADING"}
EOF

exec nginx -g 'daemon off;'
```

Defaults match today's behavior so the image starts cleanly with no env vars set (important for the k8s base manifest, which doesn't pin any of these).

### 4.2 Dockerfile change

`frontend/Dockerfile` adds the entrypoint:

```dockerfile
FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/public/ /usr/share/nginx/html/
COPY frontend/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/docker-entrypoint.sh"]
```

We deliberately don't use nginx's stock `/docker-entrypoint.d/*.sh` template mechanism — keeping a single explicit entrypoint script is easier to audit and works identically whether or not the base image keeps that convention.

### 4.3 `index.html` change

Strip the hardcoded buttons and the inline `window.VOTING_CONFIG`. Replace with empty containers and a prompt slot:

```html
<h1>Vote</h1>
<p id="prompt"></p>
<div class="choices"></div>
<p id="status" role="status"></p>

<h2>Live results</h2>
<ul id="results"><li>loading...</li></ul>
<p id="updated" class="muted"></p>

<script src="/app.js"></script>
```

The `<script>` block defining `window.VOTING_CONFIG` is removed entirely.

### 4.4 `app.js` change

The current top-level IIFE becomes `async`. Before doing anything else, it fetches `/config.json` and uses that result everywhere the inline `cfg` is used today:

```js
(async function () {
  const cfg = await fetch("/config.json").then(r => r.json());

  document.getElementById("prompt").textContent = cfg.heading;

  const container = document.querySelector(".choices");
  cfg.choices.forEach(choice => {
    const b = document.createElement("button");
    b.textContent = choice;
    b.dataset.choice = choice;
    b.addEventListener("click", () => castVote(choice));
    container.appendChild(b);
  });

  // ... existing castVote / refreshResults bodies,
  // with cfg.pollId → cfg.poll_id everywhere
})();
```

The existing `userId()`, `castVote()`, and `refreshResults()` functions move inside the async IIFE so they close over `cfg`. The poll-id field name changes from `pollId` (JS camelCase) to `poll_id` (matches the wire format and avoids a rename layer in the entrypoint).

### 4.5 `nginx.conf` — no change

`/config.json` is just another static file under `/usr/share/nginx/html/`. The existing `location /` block with `try_files $uri $uri/ =404;` already serves it. No new location block needed.

### 4.6 `docker-compose.yml` change

Add the three env vars to the `frontend` service, with `.env`-overridable defaults:

```yaml
  frontend:
    build:
      context: .
      dockerfile: frontend/Dockerfile
    ports:
      - "8080:8080"
    environment:
      VOTING_CHOICES: "${VOTING_CHOICES:-tacos,burritos}"
      VOTING_POLL_ID: "${VOTING_POLL_ID:-default}"
      VOTING_HEADING: "${VOTING_HEADING:-Default poll: choose one.}"
    depends_on:
      - vote-api
      - results-api
```

### 4.7 `.env.example` change

Add a new section:

```
# Frontend (voting choices and poll metadata)
# VOTING_CHOICES is comma-separated. Individual choices must not contain
# commas, double quotes, or backslashes — those characters break the
# generated config.json. Stick to plain identifiers (e.g. "tacos,burritos").
VOTING_CHOICES=tacos,burritos
VOTING_POLL_ID=default
VOTING_HEADING=Default poll: choose one.
```

### 4.8 k8s manifests

No source changes required for the k8s deployment to work — the same image picks up env vars from `Deployment.spec.template.spec.containers[].env`. As a follow-up (out of scope for this spec), an overlay can set per-environment values; the base manifest at `deploy/k8s/base/frontend/deployment.yaml` is left alone so it continues to use the defaults baked into the entrypoint.

## 5. Explicit non-goals

- **No server-side validation in `vote-api`.** Adding a known-choices check would push policy into the wrong service and contradict the "permissive write path" design in `CLAUDE.md`.
- **No per-user / per-session configuration.** All viewers of a given frontend container see the same choices and heading.
- **No admin UI for changing config at runtime.** Restart the container to apply changes — matches every other config var in this project.
- **No caching headers on `/config.json`.** The file is tiny and read once per page load; nginx defaults are fine.
- **No fallback if `/config.json` 404s.** If the entrypoint failed, the page is broken anyway — silent fallbacks would hide that. `app.js` lets the fetch error surface to the existing status element.
- **No k8s overlay changes in this spec.** Adding env to overlays is a one-line follow-up that doesn't need to gate this work.

## 6. Risks and known limits

- **Choices can't contain commas, double quotes, or backslashes.** The shell loop splits on commas and concatenates raw values into JSON. Documented in `.env.example`. If a future demo needs richer strings, switch the entrypoint to `jq` (one-line change, one extra `apk add jq` in the Dockerfile).
- **Empty `VOTING_CHOICES` would produce `"choices":[]`** and a page with no buttons. The defaults in the entrypoint (`: "${VOTING_CHOICES:=tacos,burritos}"`) prevent this when the env var is unset, but an explicit empty value would slip through. Acceptable — a misconfigured demo should look broken, not silently fall back.
- **`window.VOTING_CONFIG` is going away.** Nothing else in the tree references it (verified by grep at design time); if any out-of-tree code reads it, that code breaks. Acceptable for a demo project.

## 7. Test plan (high level — details in implementation plan)

- `make build` and `make up` start cleanly with default env values; the page renders `tacos` and `burritos` buttons exactly as today.
- Override `VOTING_CHOICES=pizza,salad,sushi` in `.env`, re-up the frontend container, reload the page → three buttons, labels match. Voting for each still hits `vote-api` and shows up in `/results`.
- `curl http://localhost:8080/config.json` returns the expected JSON.
- `make smoke` continues to pass unmodified (it doesn't exercise the frontend, but we verify it doesn't regress).
- Manually confirm the entrypoint script is executable in the built image (`docker run --rm --entrypoint sh <image> -c 'ls -l /docker-entrypoint.sh'`).
