"use strict";

const $ = (id) => document.getElementById(id);

const els = {
  series: $("series"),
  seriesHL: $("seriesHL"),
  seriesField: $("seriesField"),
  seriesToggle: $("seriesToggle"),
  seriesSummary: $("seriesSummary"),
  labels: $("labels"),
  numShards: $("numShards"),
  run: $("run"),
  modeHelp: $("modeHelp"),
  summary: $("summary"),
  dist: $("dist"),
  shards: $("shards"),
  errors: $("errors"),
  resultsPanel: $("resultsPanel"),
  resultsToggle: $("resultsToggle"),
  aggLabels: $("aggLabels"),
  aggHelp: $("aggHelp"),
  aggResults: $("aggResults"),
};

let mode = "without";
let aggMode = "without";

const MODE_HELP = {
  without: "Hash over ALL labels (__name__ is also a label) except the listed ones. Empty list = shard over the full label set.",
  by: "Hash over ONLY the listed labels. Empty list = every series hashes the empty string → all land on one shard.",
};

const AGG_HELP = {
  without: "Aggregate away the listed labels (and __name__). Output keeps the rest.",
  by: "Aggregate keeping ONLY the listed labels. __name__ is dropped unless listed.",
};

// Seed example: 3 series differing only by pod, aggregated without pod ->
// they collapse to the same identity and (since pod drives sharding) land on
// different shards, demonstrating cross-shard duplicates.
els.series.value = [
  'http_request_total{pod="a", path="foo"}',
  'http_request_total{pod="b", path="foo"}',
  'http_request_total{pod="c", path="foo"}',
].join("\n");
els.labels.value = "le";
els.aggLabels.value = "pod";

/* ---------- persistence (localStorage) ---------- */

const STORAGE_KEY = "vmagent-sharding-emulator";
// Series inputs larger than this are not persisted (avoid bloating localStorage).
const MAX_SAVED_SERIES = 1000;

function seriesCount() {
  return els.series.value.split("\n").filter((l) => l.trim() !== "").length;
}

function saveState() {
  const state = {
    mode,
    labels: els.labels.value,
    numShards: els.numShards.value,
    aggMode,
    aggLabels: els.aggLabels.value,
    resultsCollapsed: els.resultsPanel.classList.contains("collapsed"),
    seriesCollapsed: els.seriesField.classList.contains("collapsed"),
  };
  // Only persist the series text when it isn't too large.
  if (seriesCount() <= MAX_SAVED_SERIES) {
    state.series = els.series.value;
  }
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch (e) {
    /* storage unavailable or over quota — ignore */
  }
}

function loadState() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch (e) {
    return null;
  }
}

function applyState(s) {
  if (typeof s.series === "string") els.series.value = s.series;
  if (typeof s.labels === "string") els.labels.value = s.labels;
  if (s.numShards != null && s.numShards !== "") els.numShards.value = s.numShards;
  if (typeof s.aggLabels === "string") els.aggLabels.value = s.aggLabels;
  if (s.mode === "by" || s.mode === "without") mode = s.mode;
  if (s.aggMode === "by" || s.aggMode === "without") aggMode = s.aggMode;
  if (s.resultsCollapsed) {
    els.resultsPanel.classList.add("collapsed");
    els.resultsToggle.setAttribute("aria-expanded", "false");
  }
  if (s.seriesCollapsed) {
    els.seriesField.classList.add("collapsed");
    els.seriesToggle.setAttribute("aria-expanded", "false");
    updateSeriesSummary();
  }
}

const savedState = loadState();
if (savedState) applyState(savedState);

/* ---------- series syntax highlighting ---------- */

function highlightSeries(text) {
  return text.split("\n").map(highlightLine).join("\n");
}

function highlightLine(line) {
  let i = 0;
  const n = line.length;
  let out = "";

  const lead = /^\s*/.exec(line)[0];
  out += esc(lead);
  i = lead.length;

  const nm = /^[a-zA-Z_:][a-zA-Z0-9_:]*/.exec(line.slice(i));
  if (nm) {
    out += `<span class="hl-metric">${esc(nm[0])}</span>`;
    i += nm[0].length;
  }

  let pairOpen = false;
  let pair = 0;
  const closePair = () => {
    if (pairOpen) {
      out += "</span>";
      pairOpen = false;
    }
  };
  const punct = (c) => `<span class="hl-punct">${c}</span>`;

  while (i < n) {
    const c = line[i];
    if (c === "{") {
      out += punct("{");
      i++;
      continue;
    }
    if (c === "}") {
      closePair();
      out += punct("}");
      i++;
      continue;
    }
    if (c === ",") {
      closePair();
      out += punct(",");
      i++;
      continue;
    }
    if (c === "=") {
      out += `<span class="hl-eq">=</span>`;
      i++;
      continue;
    }
    if (/\s/.test(c)) {
      out += esc(c);
      i++;
      continue;
    }
    if (c === '"') {
      let j = i + 1;
      while (j < n) {
        if (line[j] === "\\") {
          j += 2;
          continue;
        }
        if (line[j] === '"') {
          j++;
          break;
        }
        j++;
      }
      out += `<span class="hl-val">${esc(line.slice(i, j))}</span>`;
      i = j;
      continue;
    }
    const idM = /^[a-zA-Z_][a-zA-Z0-9_]*/.exec(line.slice(i));
    if (idM) {
      if (!pairOpen) {
        pair++;
        out += `<span class="hl-pair ${pair % 2 ? "odd" : "even"}">`;
        pairOpen = true;
      }
      out += `<span class="hl-key">${esc(idM[0])}</span>`;
      i += idM[0].length;
      continue;
    }
    out += esc(line[i]);
    i++;
  }
  closePair();
  return out;
}

function syncEditor() {
  // trailing newline needs a placeholder so the layer keeps the last line height
  els.seriesHL.innerHTML = highlightSeries(els.series.value) + "\n";
  els.series.style.height = "auto";
  els.series.style.height = els.series.scrollHeight + "px";
}

els.series.addEventListener("input", syncEditor);
els.series.addEventListener("scroll", () => {
  els.seriesHL.parentElement.scrollTop = els.series.scrollTop;
});

/* ---------- collapse the time-series editor ---------- */

function updateSeriesSummary() {
  const count = els.series.value
    .split("\n")
    .filter((l) => l.trim() !== "").length;
  els.seriesSummary.textContent = `${count} series`;
}

els.seriesToggle.addEventListener("click", () => {
  const collapsed = els.seriesField.classList.toggle("collapsed");
  els.seriesToggle.setAttribute("aria-expanded", String(!collapsed));
  if (collapsed) {
    updateSeriesSummary();
  } else {
    // textarea height must be recomputed after being un-hidden
    syncEditor();
  }
  saveState();
});

/* ---------- sharding key toggle ---------- */

const shardSeg = $("shardSeg");

function setMode(m) {
  mode = m;
  shardSeg.querySelectorAll(".seg-btn").forEach((b) =>
    b.classList.toggle("active", b.dataset.mode === m)
  );
  els.modeHelp.textContent = MODE_HELP[m];
}

shardSeg.querySelectorAll(".seg-btn").forEach((b) =>
  b.addEventListener("click", () => {
    setMode(b.dataset.mode);
    run();
  })
);
setMode(mode);

/* ---------- aggregation key toggle ---------- */

const aggSeg = $("aggSeg");

function setAggMode(m) {
  aggMode = m;
  aggSeg.querySelectorAll(".seg-btn").forEach((b) =>
    b.classList.toggle("active", b.dataset.aggmode === m)
  );
  els.aggHelp.textContent = AGG_HELP[m];
}

aggSeg.querySelectorAll(".seg-btn").forEach((b) =>
  b.addEventListener("click", () => {
    setAggMode(b.dataset.aggmode);
    run();
  })
);
setAggMode(aggMode);

/* ---------- collapse the sharding-distribution panel ---------- */

els.resultsToggle.addEventListener("click", () => {
  const collapsed = els.resultsPanel.classList.toggle("collapsed");
  els.resultsToggle.setAttribute("aria-expanded", String(!collapsed));
  saveState();
});

els.run.addEventListener("click", run);

let debounce;
[els.series, els.labels, els.numShards, els.aggLabels].forEach((el) =>
  el.addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(run, 350);
  })
);

/* ---------- collapse individual shard cards ---------- */

// Persist collapsed shard indexes across re-renders (render rebuilds the DOM).
const collapsedShards = new Set();

els.shards.addEventListener("click", (e) => {
  const head = e.target.closest(".shard-head");
  if (!head) return;
  const card = head.closest(".shard-card.has-series");
  if (!card) return;
  const idx = Number(card.dataset.shard);
  if (card.classList.toggle("collapsed")) {
    collapsedShards.add(idx);
  } else {
    collapsedShards.delete(idx);
  }
});

/* ---------- compute + render ---------- */

async function run() {
  saveState();

  const payload = {
    series: els.series.value,
    mode,
    labels: els.labels.value,
    numShards: parseInt(els.numShards.value, 10) || 0,
    aggMode,
    aggLabels: els.aggLabels.value,
  };

  let resp;
  try {
    const r = await fetch("/api/shard", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    resp = await r.json();
    if (!r.ok) {
      renderFatal(resp.error || "request failed");
      return;
    }
  } catch (e) {
    renderFatal(String(e));
    return;
  }
  render(resp);
}

function renderFatal(msg) {
  els.summary.innerHTML = "";
  els.dist.innerHTML = "";
  els.shards.innerHTML = "";
  els.aggResults.innerHTML = "";
  els.errors.innerHTML = `<h3>Error</h3><ul><li class="e-msg">${esc(msg)}</li></ul>`;
}

function pct(count, total) {
  if (!total) return "0%";
  const v = (count / total) * 100;
  return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + "%";
}

function render(resp) {
  const ok = resp.results.filter((r) => r.ok);
  const bad = resp.results.filter((r) => !r.ok);
  const counts = resp.perShard;
  const used = counts.filter((c) => c > 0).length;
  const total = counts.reduce((a, c) => a + c, 0);

  const max = Math.max(0, ...counts);
  const nonEmpty = counts.filter((c) => c > 0);
  const min = nonEmpty.length ? Math.min(...nonEmpty) : 0;
  const ideal = resp.numShards ? total / resp.numShards : 0;
  let cv = 0;
  if (ideal > 0) {
    const variance =
      counts.reduce((a, c) => a + (c - ideal) ** 2, 0) / counts.length;
    cv = Math.sqrt(variance) / ideal;
  }
  const evenClass = cv <= 0.25 ? "good" : "warn";

  els.summary.innerHTML = `
    ${stat(ok.length, "series parsed", ok.length ? "good" : "")}
    ${stat(`${used}/${resp.numShards}`, "shards used")}
    ${stat(min === max ? max : `${min}–${max}`, "per-shard min–max")}
    ${stat((cv * 100).toFixed(0) + "%", "spread (CV)", evenClass)}
  `;

  // distribution bars
  let bars = `<h3>Routed series per shard</h3><div class="bars">`;
  for (let i = 0; i < counts.length; i++) {
    const c = counts[i];
    const barPct = max ? (c / max) * 100 : 0;
    bars += `
      <div class="bar-row ${c === 0 ? "empty" : ""}">
        <div class="name">shard ${i}</div>
        <div class="bar-track"><div class="bar-fill" style="width:${barPct}%"></div></div>
        <div class="cnt">${c} <span class="pct">${pct(c, total)}</span></div>
      </div>`;
  }
  bars += "</div>";
  els.dist.innerHTML = bars;

  // group series by shard
  const byShard = Array.from({ length: resp.numShards }, () => []);
  ok.forEach((r) => byShard[r.primary].push(r));

  let grid = "";
  for (let i = 0; i < resp.numShards; i++) {
    const list = byShard[i];
    const hasSeries = list.length > 0;
    const collapsed = hasSeries && collapsedShards.has(i);
    grid += `
      <div class="shard-card ${hasSeries ? "" : "empty"} ${hasSeries ? "has-series" : ""} ${collapsed ? "collapsed" : ""}" data-shard="${i}">
        <div class="shard-head">
          <span class="title">${hasSeries ? '<span class="chevron" aria-hidden="true">▾</span>' : ""}Shard ${i}</span>
          <span class="badge">${list.length} <span class="pct">${pct(list.length, total)}</span></span>
        </div>
        <div class="shard-node">${esc(resp.nodes[i])}</div>
        <ul class="shard-series">
          ${list
            .map(
              (r) => `<li>
                <div class="ser-raw">${highlightLine(r.raw.trim())}</div>
                <div class="ser-meta">${r.hash}</div>
              </li>`
            )
            .join("")}
        </ul>
      </div>`;
  }
  els.shards.innerHTML =
    `<h3>Series by shard</h3><div class="shard-grid-inner">${grid}</div>`;

  // errors
  if (bad.length) {
    els.errors.innerHTML =
      `<h3>${bad.length} line(s) could not be parsed</h3><ul>` +
      bad
        .map(
          (r) =>
            `<li><span class="e-line">${esc(r.raw.trim())}</span> — <span class="e-msg">${esc(
              r.error
            )}</span></li>`
        )
        .join("") +
      "</ul>";
  } else {
    els.errors.innerHTML = "";
  }

  renderAgg(resp);
}

function renderAgg(resp) {
  const agg = resp.agg || [];
  if (!agg.length) {
    els.aggResults.innerHTML = `<p class="placeholder">No aggregated series — add some time series above.</p>`;
    return;
  }

  // Group by output identity to count distinct identities and duplicates.
  const groups = new Map();
  agg.forEach((a) => {
    if (!groups.has(a.display)) groups.set(a.display, []);
    groups.get(a.display).push(a);
  });
  const identities = groups.size;
  const dupIdentities = [...groups.values()].filter((g) => g[0].duplicate).length;
  const totalOut = agg.length;

  const dupClass = dupIdentities > 0 ? "warn" : "good";
  let html = `<div class="summary">
    ${stat(totalOut, "output series (all shards)")}
    ${stat(identities, "unique identities")}
    ${stat(dupIdentities, "duplicated", dupClass)}
  </div>`;

  html += dupIdentities
    ? `<p class="agg-verdict warn">⚠ ${dupIdentities} identit${dupIdentities === 1 ? "y is" : "ies are"} emitted by more than one shard — these collide as duplicates at the remote storage. Keep the shard key inside the aggregation key to avoid this.</p>`
    : `<p class="agg-verdict good">✓ No duplicates — every aggregated identity is produced by a single shard.</p>`;

  html += `<h3>Aggregated series, per emitting shard</h3><ul class="agg-list">`;
  agg.forEach((a) => {
    const badge = a.duplicate
      ? `<span class="dup-badge">duplicate ×${a.dupCount} · shards ${a.dupShards.join(", ")}</span>`
      : "";
    html += `<li class="agg-item ${a.duplicate ? "dup" : ""}">
      <span class="agg-shard">shard ${a.shard}</span>
      <span class="agg-series">${highlightLine(a.display)}</span>
      ${badge}
    </li>`;
  });
  html += `</ul>`;

  els.aggResults.innerHTML = html;
}

function stat(num, lbl, cls = "") {
  return `<div class="stat ${cls}"><div class="num">${num}</div><div class="lbl">${lbl}</div></div>`;
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])
  );
}

syncEditor();
run();
