package main

const consolePage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>ForgeHTTP</title>
<style>
  :root {
    --paper: #f3ecdf;
    --sheet: #fffaf2;
    --ink: #241c16;
    --quiet: #6d6256;
    --rule: #ddd2c2;
    --copper: #c24e24;
    --copper-ink: #fff7f2;
    --hot: #8a3014;
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; }
  body {
    min-height: 100vh;
    background: var(--paper);
    color: var(--ink);
    font: 16px/1.45 "Segoe UI", "Helvetica Neue", sans-serif;
  }
  header, main { width: min(1120px, calc(100% - 32px)); margin: 0 auto; }
  header { padding: 28px 0 8px; }
  h1 { margin: 0; font-size: 40px; font-weight: 650; letter-spacing: -0.03em; }
  header p { margin: 8px 0 0; max-width: 38rem; color: var(--quiet); }
  h2 { margin: 0 0 10px; font-size: 15px; font-weight: 650; }
  .slots { margin-top: 22px; }
  .slot-count { margin: 0 0 8px; font-variant-numeric: tabular-nums; }
  .cells { display: grid; grid-template-columns: repeat(25, 1fr); gap: 3px; }
  .cell { height: 12px; background: #e6dccb; }
  .cell.on { background: var(--copper); }
  .cell.hot { background: var(--hot); }
  main {
    display: grid;
    grid-template-columns: 1.1fr 0.9fr;
    gap: 28px;
    padding: 22px 0 48px;
  }
  form, .result, .people { margin: 0; }
  label { display: block; margin: 12px 0 4px; font-size: 13px; }
  input, select, textarea {
    width: 100%;
    border: 1px solid var(--ink);
    background: var(--sheet);
    color: var(--ink);
    font: inherit;
    padding: 8px 10px;
  }
  textarea { min-height: 88px; resize: vertical; font-family: Consolas, "Courier New", monospace; font-size: 14px; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
  button {
    border: 1px solid var(--ink);
    background: var(--sheet);
    color: var(--ink);
    font: inherit;
    padding: 8px 12px;
    cursor: pointer;
  }
  button.primary { background: var(--copper); color: var(--copper-ink); border-color: var(--copper); }
  button:focus-visible, input:focus-visible, select:focus-visible, textarea:focus-visible {
    outline: 2px solid var(--copper);
    outline-offset: 2px;
  }
  ol { list-style: none; margin: 0; padding: 0; }
  li {
    padding: 8px 0;
    border-top: 1px solid var(--rule);
    font-variant-numeric: tabular-nums;
  }
  li:first-child { border-top: 0; }
  .muted { color: var(--quiet); }
  .mono, pre {
    font-family: Consolas, "Courier New", monospace;
    font-size: 13px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  pre {
    margin: 14px 0 0;
    padding: 12px;
    background: var(--sheet);
    border: 1px solid var(--rule);
    min-height: 8rem;
  }
  .people { margin-top: 28px; }
  .row { display: flex; gap: 8px; }
  .row input { flex: 1; }
  @media (max-width: 860px) {
    h1 { font-size: 32px; }
    main { grid-template-columns: 1fr; width: min(1120px, calc(100% - 24px)); }
    .cells { grid-template-columns: repeat(10, 1fr); }
  }
</style>
</head>
<body>
<header>
  <h1>ForgeHTTP</h1>
  <p>Send a request and watch this process parse it, route it, and answer. One hundred slots. A connection stays open until it says close, goes idle, or stalls.</p>
  <section class="slots" aria-labelledby="slot-label">
    <h2 id="slot-label">Slots</h2>
    <p class="slot-count" id="slot-count">0 of 100 open. 0 serving.</p>
    <div class="cells" id="cells"></div>
  </section>
</header>
<main>
  <section aria-labelledby="live-label">
    <h2 id="live-label">Open connections</h2>
    <ol id="conns"><li class="muted">None yet.</li></ol>
    <h2 id="trace-label" style="margin-top:22px">Trace</h2>
    <ol id="trace" aria-labelledby="trace-label"><li class="muted">Nothing parsed yet.</li></ol>
  </section>
  <section>
    <h2>Send on a fresh connection</h2>
    <form id="send-form">
      <label for="method">Method</label>
      <select id="method">
        <option>GET</option>
        <option>POST</option>
        <option>PUT</option>
        <option>DELETE</option>
      </select>
      <label for="target">Path</label>
      <input id="target" value="/api/users">
      <label for="proto">Version</label>
      <select id="proto">
        <option>HTTP/1.1</option>
        <option>HTTP/1.0</option>
      </select>
      <label for="headers">Headers</label>
      <textarea id="headers">Host: localhost</textarea>
      <label for="body">Body</label>
      <textarea id="body" style="min-height:52px"></textarea>
      <div class="actions">
        <button class="primary" type="submit">Send once</button>
        <button type="button" data-mode="twice">Twice, same connection</button>
        <button type="button" data-mode="http10">As HTTP/1.0</button>
        <button type="button" data-mode="close">Connection: close</button>
        <button type="button" data-mode="stall">Stall until 408</button>
      </div>
    </form>
    <pre id="result" aria-live="polite">The bytes come back here.</pre>
    <div class="people">
      <h2>Users</h2>
      <form id="user-form">
        <label for="name">Name</label>
        <div class="row">
          <input id="name" autocomplete="off">
          <button class="primary" type="submit">Add</button>
        </div>
      </form>
      <p id="user-note" class="muted"></p>
      <ol id="users"><li class="muted">No users yet.</li></ol>
    </div>
  </section>
</main>
<script>
var cells = document.getElementById("cells");
for (var i = 0; i < 100; i++) {
  var cell = document.createElement("i");
  cell.className = "cell";
  cell.setAttribute("aria-hidden", "true");
  cells.appendChild(cell);
}

function line(parent, text, mono) {
  var li = document.createElement("li");
  li.textContent = text;
  if (mono) li.className = "mono";
  parent.appendChild(li);
}

function paint(list, items, empty, mono) {
  list.replaceChildren();
  if (!items.length) {
    var li = document.createElement("li");
    li.className = "muted";
    li.textContent = empty;
    list.appendChild(li);
    return;
  }
  items.forEach(function (text) { line(list, text, mono); });
}

function refresh() {
  fetch("/console/state").then(function (res) { return res.json(); }).then(function (snap) {
    var max = snap.maxConns || 100;
    document.getElementById("slot-count").textContent =
      snap.active + " of " + max + " open. " + snap.serving + " serving.";
    var nodes = cells.children;
    var shown = Math.min(max, nodes.length);
    for (var i = 0; i < nodes.length; i++) {
      nodes[i].className = "cell";
      if (i < shown && i < snap.serving) nodes[i].className = "cell hot";
      else if (i < shown && i < snap.active) nodes[i].className = "cell on";
    }
    paint(document.getElementById("conns"), (snap.connections || []).map(function (c) {
      var n = c.requests === 1 ? "1 request" : c.requests + " requests";
      return c.remote + "  " + c.stage + "  " + n;
    }), "None yet.", false);
    var traces = (snap.traces || []).slice().reverse().map(function (t) {
      var head = t.method ? (t.method + " " + t.target + " " + t.proto) : "unread request";
      var tail = t.status ? (" " + t.status) : "";
      var conn = t.connection ? ("  " + t.connection) : "";
      var note = t.note ? ("  " + t.note) : "";
      return head + tail + conn + note;
    });
    paint(document.getElementById("trace"), traces, "Nothing parsed yet.", true);
  }).catch(function () {});
}

function payload(mode) {
  var headers = document.getElementById("headers").value.split("\n").filter(Boolean).map(function (line) {
    var at = line.indexOf(":");
    if (at < 0) return { name: line.trim(), value: "" };
    return { name: line.slice(0, at).trim(), value: line.slice(at + 1).trim() };
  });
  return {
    mode: mode,
    method: document.getElementById("method").value,
    target: document.getElementById("target").value,
    proto: document.getElementById("proto").value,
    headers: headers,
    body: document.getElementById("body").value
  };
}

function showTry(data) {
  var out = document.getElementById("result");
  if (!data || data.error && !(data.exchanges || []).length) {
    out.textContent = data && data.error ? data.error : "The try failed.";
    return;
  }
  var text = (data.exchanges || []).map(function (ex) {
    return ex.rawRequest + "\n----\n" + ex.rawResponse;
  }).join("\n\n");
  if (data.error) text += "\n\n" + data.error;
  out.textContent = text || "No bytes came back.";
}

function send(mode) {
  fetch("/console/try", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload(mode))
  }).then(function (res) { return res.json(); }).then(showTry).catch(function (err) {
    document.getElementById("result").textContent = String(err);
  });
}

document.getElementById("send-form").addEventListener("submit", function (ev) {
  ev.preventDefault();
  send("once");
});
document.querySelectorAll("[data-mode]").forEach(function (btn) {
  btn.addEventListener("click", function () { send(btn.getAttribute("data-mode")); });
});

function loadUsers() {
  fetch("/api/users").then(function (res) { return res.json(); }).then(function (users) {
    paint(document.getElementById("users"), (users || []).map(function (u) {
      return u.id + "  " + u.name;
    }), "No users yet.", false);
  }).catch(function () {});
}

document.getElementById("user-form").addEventListener("submit", function (ev) {
  ev.preventDefault();
  var name = document.getElementById("name").value;
  var note = document.getElementById("user-note");
  fetch("/api/users", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: name })
  }).then(function (res) { return res.text().then(function (text) { return { status: res.status, text: text }; }); })
    .then(function (res) {
      if (res.status === 201) {
        note.textContent = "";
        document.getElementById("name").value = "";
        loadUsers();
        return;
      }
      note.textContent = res.text;
    }).catch(function (err) { note.textContent = String(err); });
});

refresh();
loadUsers();
setInterval(refresh, 500);
</script>
</body>
</html>
`
