(function () {
  const cfg = window.VOTING_CONFIG;
  const statusEl = document.getElementById("status");
  const resultsEl = document.getElementById("results");
  const updatedEl = document.getElementById("updated");

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
      const r = await fetch(cfg.voteApi + "/vote", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ poll_id: cfg.pollId, choice, user_id: userId() }),
      });
      if (!r.ok) throw new Error("HTTP " + r.status);
      statusEl.textContent = "vote recorded for " + choice;
    } catch (e) {
      statusEl.textContent = "error: " + e.message;
    }
  }

  async function refreshResults() {
    try {
      const r = await fetch(cfg.resultsApi + "/results?poll_id=" + encodeURIComponent(cfg.pollId));
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

  document.querySelectorAll(".choices button").forEach((btn) => {
    btn.addEventListener("click", () => castVote(btn.dataset.choice));
  });

  refreshResults();
  setInterval(refreshResults, 2000);
})();
