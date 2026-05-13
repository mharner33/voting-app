# Runtime-Configurable Voting Choices Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive the frontend's vote buttons, poll id, and prompt heading from env vars read at container start, so the same image can serve different demos without a rebuild.

**Architecture:** A shell entrypoint generates `/usr/share/nginx/html/config.json` from env vars before nginx starts. `app.js` fetches `/config.json` on load and renders buttons/heading from it. The JSON-emitting logic is factored into a small testable `gen-config.sh` so the entrypoint stays trivial.

**Tech Stack:** POSIX shell (entrypoint + tests), nginx:1.27-alpine (frontend base image), vanilla JS (no framework), docker-compose env interpolation.

**Spec:** `docs/superpowers/specs/2026-05-13-runtime-voting-choices-design.md`

**Implementation note vs. spec §4.1:** The spec shows a single `docker-entrypoint.sh` that does both JSON generation and `exec nginx`. This plan splits it: `gen-config.sh` emits JSON to stdout (testable as a normal script), and `docker-entrypoint.sh` redirects that output to the served path and execs nginx. Functionally identical; cleaner to test.

---

## File Structure

| File | Responsibility |
|---|---|
| `frontend/gen-config.sh` (new) | Emit `config.json` (as a single line of JSON) to stdout from `VOTING_*` env vars with documented defaults. Pure stdout — no filesystem writes. |
| `frontend/docker-entrypoint.sh` (new) | Run `gen-config.sh` → `/usr/share/nginx/html/config.json`, then `exec nginx`. |
| `frontend/test-gen-config.sh` (new) | POSIX shell test for `gen-config.sh`. Runs four scenarios; exits 0 on pass, 1 on fail. |
| `frontend/Dockerfile` (modify) | `COPY` both new scripts, `chmod +x` them, set `ENTRYPOINT`. |
| `frontend/public/index.html` (modify) | Strip hardcoded buttons + inline `VOTING_CONFIG`; add empty `#prompt` + `.choices` containers. |
| `frontend/public/app.js` (modify) | Make IIFE `async`; `fetch("/config.json")` first; render buttons + heading from response; replace all `cfg.pollId` with `cfg.poll_id`. |
| `docker-compose.yml` (modify) | Add `VOTING_CHOICES`/`VOTING_POLL_ID`/`VOTING_HEADING` to `frontend.environment` with `.env`-overridable defaults. |
| `.env.example` (modify) | Document the three new env vars and the allowed-character caveat. |

---

## Task 1: `gen-config.sh` with TDD

**Files:**
- Create: `frontend/gen-config.sh`
- Create: `frontend/test-gen-config.sh`

- [ ] **Step 1: Write the failing test**

Create `frontend/test-gen-config.sh`:

```sh
#!/bin/sh
# Test gen-config.sh by invoking it with various env vars and
# comparing the stdout JSON line-for-line with expected output.

set -eu
cd "$(dirname "$0")"

fail=0
run_case() {
  desc="$1"; expected="$2"; shift 2
  # Run with a clean env: unset any leaked VOTING_* values first,
  # then export only what this case sets, then run.
  actual=$(env -i PATH="$PATH" "$@" ./gen-config.sh)
  if [ "$actual" = "$expected" ]; then
    echo "PASS: $desc"
  else
    echo "FAIL: $desc"
    echo "  expected: $expected"
    echo "  actual:   $actual"
    fail=1
  fi
}

run_case "defaults" \
  '{"choices":["tacos","burritos"],"poll_id":"default","heading":"Default poll: choose one."}'

run_case "override choices" \
  '{"choices":["pizza","salad","sushi"],"poll_id":"default","heading":"Default poll: choose one."}' \
  VOTING_CHOICES=pizza,salad,sushi

run_case "override poll_id and heading" \
  '{"choices":["tacos","burritos"],"poll_id":"lunch","heading":"What for lunch?"}' \
  VOTING_POLL_ID=lunch VOTING_HEADING="What for lunch?"

run_case "single choice" \
  '{"choices":["onlyone"],"poll_id":"default","heading":"Default poll: choose one."}' \
  VOTING_CHOICES=onlyone

exit $fail
```

- [ ] **Step 2: Run test to verify it fails**

Run: `chmod +x frontend/test-gen-config.sh && frontend/test-gen-config.sh`

Expected: FAIL — the script outputs something like `./gen-config.sh: not found` on the first `run_case` call, exit code 127 (which propagates as nonzero through `set -e`). Either way, exit status is nonzero. That's our red.

- [ ] **Step 3: Write `gen-config.sh`**

Create `frontend/gen-config.sh`:

```sh
#!/bin/sh
# Emit the voting-app frontend's runtime config as a single line of JSON
# on stdout. Reads VOTING_CHOICES (comma-separated), VOTING_POLL_ID, and
# VOTING_HEADING from the environment; falls back to demo defaults.
#
# Limitations: individual choices must not contain commas, double quotes,
# or backslashes. See .env.example.

set -eu

: "${VOTING_CHOICES:=tacos,burritos}"
: "${VOTING_POLL_ID:=default}"
: "${VOTING_HEADING:=Default poll: choose one.}"

choices_json=""
old_ifs=$IFS
IFS=','
for c in $VOTING_CHOICES; do
  if [ -n "$choices_json" ]; then
    choices_json="$choices_json,"
  fi
  choices_json="$choices_json\"$c\""
done
IFS=$old_ifs

printf '{"choices":[%s],"poll_id":"%s","heading":"%s"}' \
  "$choices_json" "$VOTING_POLL_ID" "$VOTING_HEADING"
```

Then make it executable: `chmod +x frontend/gen-config.sh`

- [ ] **Step 4: Run test to verify it passes**

Run: `frontend/test-gen-config.sh`

Expected:
```
PASS: defaults
PASS: override choices
PASS: override poll_id and heading
PASS: single choice
```
Exit code 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/gen-config.sh frontend/test-gen-config.sh
git commit -m "feat(frontend): add gen-config.sh to emit runtime config JSON

Pure POSIX-shell generator that turns VOTING_CHOICES, VOTING_POLL_ID,
and VOTING_HEADING into the config.json the page will fetch on load.
Defaults preserve today's behavior (tacos/burritos, poll_id=default).
Tested via frontend/test-gen-config.sh."
```

---

## Task 2: Docker entrypoint that wires gen-config to nginx

**Files:**
- Create: `frontend/docker-entrypoint.sh`

- [ ] **Step 1: Write the entrypoint**

Create `frontend/docker-entrypoint.sh`:

```sh
#!/bin/sh
# Generate /usr/share/nginx/html/config.json from VOTING_* env vars,
# then hand off to nginx. Runs once per container start.

set -eu

/gen-config.sh > /usr/share/nginx/html/config.json

exec nginx -g 'daemon off;'
```

Make it executable: `chmod +x frontend/docker-entrypoint.sh`

(No standalone test for this script — it's three lines and the meaningful logic lives in `gen-config.sh`. Task 3 verifies it works end-to-end inside the built image.)

- [ ] **Step 2: Commit**

```bash
git add frontend/docker-entrypoint.sh
git commit -m "feat(frontend): add docker-entrypoint.sh

Writes generated config.json into nginx's docroot before execing nginx
so the file is in place by the time the first request arrives."
```

---

## Task 3: Wire entrypoint into the Dockerfile and verify the built image

**Files:**
- Modify: `frontend/Dockerfile`

- [ ] **Step 1: Update the Dockerfile**

Replace `frontend/Dockerfile` with:

```dockerfile
FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/public/ /usr/share/nginx/html/
COPY frontend/gen-config.sh /gen-config.sh
COPY frontend/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /gen-config.sh /docker-entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/docker-entrypoint.sh"]
```

- [ ] **Step 2: Build the image**

Run: `docker compose build frontend`

Expected: build succeeds, ends with `naming to docker.io/library/voting-app-frontend` (or similar — the image tag depends on the compose project name).

- [ ] **Step 3: Start just the frontend and curl `/config.json`**

The frontend doesn't strictly need the APIs to serve `/config.json`, but `docker compose up frontend` will try to start `vote-api` and `results-api` via `depends_on`. Run `make up` to bring up the whole stack:

```bash
make up
```

Wait ~5 seconds for nginx, then:

```bash
curl -s http://localhost:8080/config.json
```

Expected output (exactly one line, no trailing newline):
```
{"choices":["tacos","burritos"],"poll_id":"default","heading":"Default poll: choose one."}
```

- [ ] **Step 4: Verify the page still renders the legacy hardcoded buttons**

Open http://localhost:8080 in a browser. Expected: the page still shows the two hardcoded `tacos` / `burritos` buttons from `index.html` (we haven't touched the markup yet). The `/config.json` endpoint is reachable but nothing fetches it yet. This intermediate state is fine — Task 4 makes the page use the JSON.

- [ ] **Step 5: Tear down**

```bash
make down
```

- [ ] **Step 6: Commit**

```bash
git add frontend/Dockerfile
git commit -m "feat(frontend): serve generated config.json from entrypoint

Build copies gen-config.sh + docker-entrypoint.sh into the image and
uses the entrypoint to generate /usr/share/nginx/html/config.json on
container start. Page still uses hardcoded buttons until the next
commit wires app.js to /config.json."
```

---

## Task 4: Frontend renders from `/config.json`

**Files:**
- Modify: `frontend/public/index.html`
- Modify: `frontend/public/app.js`

Both files must change together so the page doesn't leave a broken commit between them. One task, one commit.

- [ ] **Step 1: Rewrite `frontend/public/index.html`**

Replace the file with:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Voting App</title>
  <link rel="stylesheet" href="/style.css">
</head>
<body>
  <main>
    <h1>Vote</h1>
    <p id="prompt"></p>
    <div class="choices"></div>
    <p id="status" role="status"></p>

    <h2>Live results</h2>
    <ul id="results"><li>loading...</li></ul>
    <p id="updated" class="muted"></p>
  </main>

  <script src="/app.js"></script>
</body>
</html>
```

Changes vs. today: removed the hardcoded `<button data-choice="...">` elements, replaced the placeholder paragraph with `<p id="prompt"></p>`, and removed the inline `<script>` block that defined `window.VOTING_CONFIG`.

- [ ] **Step 2: Rewrite `frontend/public/app.js`**

Replace the file with:

```js
(async function () {
  const statusEl = document.getElementById("status");
  const resultsEl = document.getElementById("results");
  const updatedEl = document.getElementById("updated");
  const promptEl = document.getElementById("prompt");
  const choicesEl = document.querySelector(".choices");

  let cfg;
  try {
    cfg = await fetch("/config.json").then((r) => {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    });
  } catch (e) {
    statusEl.textContent = "config error: " + e.message;
    return;
  }

  promptEl.textContent = cfg.heading;

  function newId() {
    if (window.crypto && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID();
    }
    return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
      const r = (Math.random() * 16) | 0;
      const v = c === "x" ? r : (r & 0x3) | 0x8;
      return v.toString(16);
    });
  }

  function userId() {
    let id = localStorage.getItem("voting-user-id");
    if (!id) {
      id = newId();
      localStorage.setItem("voting-user-id", id);
    }
    return id;
  }

  async function castVote(choice) {
    statusEl.textContent = "submitting...";
    try {
      const r = await fetch("/vote", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ poll_id: cfg.poll_id, choice, user_id: userId() }),
      });
      if (!r.ok) throw new Error("HTTP " + r.status);
      statusEl.textContent = "vote recorded for " + choice;
    } catch (e) {
      statusEl.textContent = "error: " + e.message;
    }
  }

  async function refreshResults() {
    try {
      const r = await fetch("/results?poll_id=" + encodeURIComponent(cfg.poll_id));
      if (!r.ok) throw new Error("HTTP " + r.status);
      const data = await r.json();
      resultsEl.innerHTML = "";
      if (!data.results || data.results.length === 0) {
        resultsEl.innerHTML = "<li>no votes yet</li>";
      } else {
        data.results.forEach(({ choice, count }) => {
          const li = document.createElement("li");
          li.textContent = `${choice}: ${count}`;
          resultsEl.appendChild(li);
        });
      }
      updatedEl.textContent = "updated: " + (data.updated_at || "—");
    } catch (e) {
      updatedEl.textContent = "results error: " + e.message;
    }
  }

  cfg.choices.forEach((choice) => {
    const btn = document.createElement("button");
    btn.textContent = choice;
    btn.dataset.choice = choice;
    btn.addEventListener("click", () => castVote(choice));
    choicesEl.appendChild(btn);
  });

  refreshResults();
  setInterval(refreshResults, 2000);
})();
```

Key differences vs. the current `app.js`:

- IIFE is now `async` and awaits `/config.json` before doing anything else.
- The `cfg.voteApi` / `cfg.resultsApi` prefixes are gone — they were always empty strings in practice (same-origin via nginx proxy in compose, same-origin via Ingress in k8s).
- `cfg.pollId` (camelCase) → `cfg.poll_id` (wire format) everywhere.
- Buttons are created from `cfg.choices` instead of looked up by selector.

- [ ] **Step 3: Rebuild and bring the stack up**

```bash
make up
```

Wait ~5 seconds.

- [ ] **Step 4: Verify the page renders from `/config.json`**

Open http://localhost:8080 in a browser. Expected:
- Prompt under the `Vote` heading reads `Default poll: choose one.`
- Two buttons labeled `tacos` and `burritos` (in that order).
- Clicking a button shows `vote recorded for tacos` (or `burritos`) within ~1 second.
- The `Live results` list updates within ~5 seconds (one tally interval).

Also verify the raw config response:

```bash
curl -s http://localhost:8080/config.json
```
Expected: same JSON as before, unchanged.

- [ ] **Step 5: Verify the smoke test still passes**

```bash
make smoke
```

Expected: smoke test completes successfully (it doesn't exercise the frontend HTML, but we're confirming no regression in the API path).

- [ ] **Step 6: Tear down**

```bash
make down
```

- [ ] **Step 7: Commit**

```bash
git add frontend/public/index.html frontend/public/app.js
git commit -m "feat(frontend): render buttons and heading from /config.json

Page no longer carries hardcoded vote buttons or an inline
window.VOTING_CONFIG. app.js fetches /config.json on load, sets
the prompt heading, and creates one <button> per configured choice.

poll_id renamed from cfg.pollId to cfg.poll_id to match the wire
format used in /vote and /results."
```

---

## Task 5: Wire env vars through docker-compose and `.env.example`

**Files:**
- Modify: `docker-compose.yml`
- Modify: `.env.example`

- [ ] **Step 1: Add env vars to the `frontend` service**

Edit `docker-compose.yml`. Find the `frontend:` block (around line 108):

```yaml
  frontend:
    build:
      context: .
      dockerfile: frontend/Dockerfile
    ports:
      - "8080:8080"
    depends_on:
      - vote-api
      - results-api
```

Replace it with:

```yaml
  frontend:
    build:
      context: .
      dockerfile: frontend/Dockerfile
    environment:
      VOTING_CHOICES: "${VOTING_CHOICES:-tacos,burritos}"
      VOTING_POLL_ID: "${VOTING_POLL_ID:-default}"
      VOTING_HEADING: "${VOTING_HEADING:-Default poll: choose one.}"
    ports:
      - "8080:8080"
    depends_on:
      - vote-api
      - results-api
```

The `${VAR:-default}` form means: if `.env` doesn't set the var (or sets it to empty), use the default. This keeps `make up` working out-of-the-box for users who haven't filled in the new section yet.

- [ ] **Step 2: Document the env vars in `.env.example`**

Append to `.env.example`:

```
# Frontend (voting choices and poll metadata)
# VOTING_CHOICES is comma-separated. Individual choices must NOT contain
# commas, double quotes, or backslashes — those characters break the
# generated config.json. Stick to plain identifiers (e.g. "tacos,burritos").
VOTING_CHOICES=tacos,burritos
VOTING_POLL_ID=default
VOTING_HEADING=Default poll: choose one.
```

- [ ] **Step 3: Verify defaults still work without a real `.env`**

```bash
make down
make up
curl -s http://localhost:8080/config.json
```

Expected output:
```
{"choices":["tacos","burritos"],"poll_id":"default","heading":"Default poll: choose one."}
```

- [ ] **Step 4: Verify override via `.env`**

Edit your local `.env` (not `.env.example`) and add:

```
VOTING_CHOICES=pizza,salad,sushi
VOTING_POLL_ID=lunch
VOTING_HEADING=What's for lunch?
```

Then:

```bash
docker compose up -d --force-recreate frontend
sleep 3
curl -s http://localhost:8080/config.json
```

Expected:
```
{"choices":["pizza","salad","sushi"],"poll_id":"lunch","heading":"What's for lunch?"}
```

Open http://localhost:8080 in a browser. Expected: three buttons (`pizza`, `salad`, `sushi`), heading reads `What's for lunch?`. Clicking each button works and produces results under the `lunch` poll id (the live results list shows counts grouped by choice).

- [ ] **Step 5: Restore your `.env` and tear down**

Remove the three lines you added to `.env` (or revert them to the defaults). Then:

```bash
make down
```

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml .env.example
git commit -m "feat(frontend): expose VOTING_* env vars via docker-compose

The frontend service now reads VOTING_CHOICES, VOTING_POLL_ID, and
VOTING_HEADING from the environment, with .env-overridable defaults
that preserve today's tacos/burritos demo. Restart the frontend
container to apply changes; no rebuild needed."
```

---

## Task 6: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Default-config end-to-end**

```bash
make down
make up
```

Open http://localhost:8080 in a browser. Confirm:
- Heading: `Default poll: choose one.`
- Buttons: `tacos`, `burritos`
- Click each button → `vote recorded for ...`
- Within ~5s the results list updates with counts.

- [ ] **Step 2: Smoke test**

```bash
make smoke
```
Expected: exits 0.

- [ ] **Step 3: Unit test still passes against the gen-config script**

```bash
frontend/test-gen-config.sh
```
Expected: all four cases PASS, exit 0.

- [ ] **Step 4: Tear down**

```bash
make down
```

- [ ] **Step 5: Confirm no stray `window.VOTING_CONFIG` references**

```bash
grep -rn "VOTING_CONFIG" frontend/ --include='*.js' --include='*.html'
```
Expected: no output (the only matches before were the two we deleted).

No commit needed for this task — it's pure verification.
