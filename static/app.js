const esc = s => (s??"").toString().replace(/[&<>]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;"}[c]));

// --- provider switcher ---
async function switchProvider(name) {
  const r = await postJSON("/api/switch", {provider: name});
  if (r.ok) { document.getElementById("provider-popup")?.remove(); refresh(); return true; }
  alert("Switch failed: "+JSON.stringify(r));
  return false;
}
function toggleProviderMenu(ev) {
  ev.stopPropagation();
  const old = document.getElementById("provider-popup");
  if (old) { old.remove(); return; }
  const btn = ev.currentTarget;
  const pop = document.createElement("div");
  pop.id = "provider-popup";
  pop.className = "provider-popup";
  fetch("/api/switch").then(r=>r.json()).then(d => {
    (d.providers||[]).forEach(p => {
      const row = document.createElement("button");
      row.className = "provider-option" + (p === d.active ? " active" : "");
      row.textContent = p + (p === d.active ? " ✓" : "");
      row.onclick = () => switchProvider(p);
      pop.appendChild(row);
    });
  });
  document.body.appendChild(pop);
  const r = btn.getBoundingClientRect();
  pop.style.top = (r.bottom+4)+"px";
  pop.style.left = r.left+"px";
  setTimeout(()=>document.addEventListener("click",()=>pop.remove(),{once:true}),10);
}

// --- tiny markdown renderer for chat replies (no dependency, XSS-safe: we
// escape first, then apply a small set of transforms the LLM actually uses:
// bold/italic/code, links, ordered/unordered lists, and tables).
function mdInline(s){   // s is already HTML-escaped
  return s
    .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|message:\/\/[^\s)]+)\)/g,
             (m, text, url) => `<a href="${url}" target="_blank" rel="noopener">${text}</a>`)
    .replace(/\*\*([^*]+?)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^*_`])[*_]([^*_`\s][^*_`]*?)[*_](?![\w*])/g, "$1<em>$2</em>")
    .replace(/`([^`]+?)`/g, "<code>$1</code>");
}
function renderMarkdown(text){
  const lines = esc(text).split(/\r?\n/);
  const row = l => /^\s*\|.*\|\s*$/.test(l);
  const sep = l => /^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/.test(l);
  const cells = l => l.trim().replace(/^\||\|$/g, "").split("|").map(c => c.trim());
  const out = [];
  let i = 0;
  while (i < lines.length){
    const l = lines[i];
    if (row(l) && i+1 < lines.length && sep(lines[i+1])){          // table
      const head = cells(l); i += 2; const body = [];
      while (i < lines.length && row(lines[i])){ body.push(cells(lines[i])); i++; }
      out.push(`<table class="mdtable"><thead><tr>${head.map(h=>`<th>${mdInline(h)}</th>`).join("")}</tr></thead><tbody>${
        body.map(r=>`<tr>${r.map(c=>`<td>${mdInline(c)}</td>`).join("")}</tr>`).join("")}</tbody></table>`);
      continue;
    }
    const h = l.match(/^\s*#{1,6}\s+(.*)$/);
    if (h){ out.push(`<div class="mdh">${mdInline(h[1])}</div>`); i++; continue; }
    if (/^\s*[-*]\s+/.test(l)){                                     // unordered list
      const items = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])){ items.push(mdInline(lines[i].replace(/^\s*[-*]\s+/,""))); i++; }
      out.push(`<ul class="mdlist">${items.map(x=>`<li>${x}</li>`).join("")}</ul>`); continue;
    }
    if (/^\s*\d+\.\s+/.test(l)){                                    // ordered list
      const items = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])){ items.push(mdInline(lines[i].replace(/^\s*\d+\.\s+/,""))); i++; }
      out.push(`<ol class="mdlist">${items.map(x=>`<li>${x}</li>`).join("")}</ol>`); continue;
    }
    if (/^\s*$/.test(l)){ i++; continue; }
    const para = [];                                                // paragraph
    while (i < lines.length && lines[i].trim() && !/^\s*[-*]\s|^\s*\d+\.\s|^\s*#{1,6}\s/.test(lines[i])
           && !(row(lines[i]) && i+1<lines.length && sep(lines[i+1]))){
      para.push(mdInline(lines[i])); i++;
    }
    out.push(`<div class="mdp">${para.join("<br>")}</div>`);
  }
  return out.join("");
}
let D = null;
let oauthProviders = {}, oauthMessage = "";

// Click a section's data to open the real local file/folder (editor or Finder).
function revealFile(p){ fetch("/api/reveal?path=" + encodeURIComponent(p)); }
const reveal = (path, label) => `<a class="reveal" onclick="revealFile('${path}')">${esc(label)}</a>`;

// --- memory CRUD (dashboard side). `editing` pauses the 5s rebuild so an
// in-progress edit isn't wiped (same idea as the animation guard).
let editing = false;
async function postJSON(url, body){ return (await fetch(url,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)})).json(); }
function showAddProvider(){
  const form=document.getElementById("add-provider-form"); if(!form) return;
  form.hidden=!form.hidden; editing=!form.hidden;
  if(!form.hidden) document.getElementById("provider-name").focus();
}
async function addProvider(){
  const value=id=>document.getElementById(id).value.trim(), msg=document.getElementById("provider-form-status");
  const body={name:value("provider-name"),base_url:value("provider-base-url"),model:value("provider-model"),small_model:value("provider-small-model"),api_key:value("provider-api-key"),priority:Number(value("provider-priority"))||10};
  if(!body.name||!body.base_url||!body.model){ msg.textContent="Name, base URL, and model are required."; return; }
  try { await postJSON("/api/providers",body); editing=false; await refresh(); }
  catch(e){ msg.textContent="Could not add provider: "+e.message; }
}
async function removeProvider(name){
  if(!confirm("Remove provider "+name+"?")) return;
  try {
    const r=await fetch("/api/providers?name="+encodeURIComponent(name),{method:"DELETE"});
    if(!r.ok) throw new Error((await r.text())||"request failed");
    await refresh();
  } catch(e){ alert("Could not remove provider: "+e.message); }
}
async function startOAuth(name){
  const provider=oauthProviders[name]||{}, status=document.getElementById("oauth-status");
  oauthMessage="Starting "+(provider.display_name||name)+" login…"; if(status) status.textContent=oauthMessage;
  try {
    if(provider.auth_type==="device_code"){
      const r=await postJSON("/api/oauth/device/"+encodeURIComponent(name),{});
      oauthMessage="Enter code "+r.user_code+" at "+r.verification_url; if(status) status.textContent=oauthMessage;
      if(r.verification_url) window.open(r.verification_url, "_blank");
      for(let i=0;i<60;i++){
        await new Promise(resolve=>setTimeout(resolve,5000));
        const poll=await (await fetch("/api/oauth/device/"+encodeURIComponent(name)+"?device_code="+encodeURIComponent(r.device_code))).json();
        if(poll.ok){ oauthMessage=(provider.display_name||name)+" login complete."; await refresh(); return; }
        if(!poll.pending) throw new Error(poll.error||"login failed");
      }
      oauthMessage="Login is still pending. Try again when ready.";
    } else if(provider.auth_type==="codex_device"){
      const r=await postJSON("/api/oauth/login/"+encodeURIComponent(name),{});
      if(!r.ok) throw new Error(r.message||"login start failed");
      oauthMessage="Code: "+r.user_code+" at "+r.url; if(status) status.textContent=oauthMessage;
      if(r.url) window.open(r.url, "_blank");
      for(let i=0;i<180;i++){
        await new Promise(resolve=>setTimeout(resolve,(r.interval||5)*1000));
        const poll=await (await fetch("/api/oauth/device/"+encodeURIComponent(name)+"?device_code="+encodeURIComponent(r.device_code))).json();
        if(poll.ok){ oauthMessage=(provider.display_name||name)+" login complete."; await refresh(); return; }
        if(!poll.pending) throw new Error(poll.error||"login failed");
      }
      oauthMessage="Login is still pending. Try again when ready.";
    } else {
      const r=await postJSON("/api/oauth/login/"+encodeURIComponent(name),{});
      if(r.url) window.open(r.url, "_blank");
      oauthMessage=r.message||"Login page opened. Complete login in the new tab.";
    }
  } catch(e){ oauthMessage="Login failed: "+e.message; }
  if(status) status.textContent=oauthMessage;
}
function editFact(id){
  const row = document.getElementById("fact-"+id); if(!row) return;
  editing = true;
  const cell = row.querySelector(".fc"); const cur = cell.textContent;
  cell.innerHTML = `<textarea class="editor" id="ef-${id}">${cur.replace(/</g,"&lt;")}</textarea>`;
  const act = row.lastElementChild;
  act.innerHTML = `<a class="reveal" onclick="saveFact(${id})">save</a> · <a class="reveal" onclick="editing=false;refresh()">cancel</a>`;
  document.getElementById("ef-"+id).focus();
}
async function saveFact(id){
  const v = document.getElementById("ef-"+id).value.trim();
  await postJSON("/api/memory", {action:"update_fact", id, content:v});
  editing = false; refresh();
}
async function delMem(action, id){
  if(!confirm("Delete this from memory?")) return;
  await postJSON("/api/memory", {action, id});
  refresh();
}
// dirty-state: a Save button stays muted until its editor actually changes
function dirty(btnId){ editing = true; const b = document.getElementById(btnId); if (b) b.disabled = false; }
async function saveSoul(){
  const v = document.getElementById("soul").value;
  const r = await postJSON("/api/memory", {action:"save_soul", content:v});
  document.getElementById("soul-msg").textContent = r.error ? ("Error: "+r.error) : "Saved — live next turn.";
  if (!r.error){ const b=document.getElementById("soul-save"); if(b) b.disabled=true; editing=false; }
}
async function saveSkill(i){
  const ta = document.getElementById("sk-"+i);
  const r = await postJSON("/api/memory", {action:"save_skill", path:ta.dataset.path, content:ta.value});
  document.getElementById("skmsg-"+i).textContent = r.error ? ("Error: "+r.error) : "Saved — live next turn.";
  if (!r.error){ const b=document.getElementById("sksave-"+i); if(b) b.disabled=true; editing=false; }
}
function markEditing(){ editing = true; }

async function saveOnboarding(){
  const b = document.getElementById("onb-save");
  b.disabled = true; b.textContent = "Saving...";
  const m = document.getElementById("onb-msg");
  m.textContent = "";
  const body = {
    provider_name: document.getElementById("onb-provider").value.trim(),
    api_key: document.getElementById("onb-apikey").value.trim(),
    base_url: document.getElementById("onb-baseurl").value.trim(),
    model: document.getElementById("onb-model").value.trim(),
    small_model: document.getElementById("onb-small").value.trim(),
    telegram_token: document.getElementById("onb-tgtoken").value.trim(),
  };
  if (!body.api_key || !body.base_url || !body.model) {
    m.textContent = "API Key, Base URL, and Model are required.";
    b.disabled = false; b.textContent = "Save & Start Mino"; return;
  }
  try {
    const response = await fetch("/api/settings", {method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify(body)});
    if (!response.ok) throw new Error((await response.text()) || "configuration rejected");
    m.innerHTML = "Saved. Mino is restarting <span class=\"caret\"></span>";
    // poll until Mino comes back, then reload
    let attempts = 0;
    const poll = setInterval(async () => {
      attempts++;
      try {
        const r = await fetch("/api/data");
        if (r.ok) { clearInterval(poll); location.reload(); }
      } catch(e) {}
      if (attempts > 60) { clearInterval(poll); m.textContent = "Taking longer than expected. Try refreshing."; }
    }, 1000);
  } catch(e) {
    m.textContent = "Failed: " + e.message;
    b.disabled = false; b.textContent = "Save & Start Mino";
  }
}

const money = n => "$" + (n < 0.01 ? n.toFixed(4) : n.toFixed(2));
const secs = ms => ms==null ? "—" : (ms/1000).toFixed(1)+"s";

const gateBadge = g => !g ? "" :
  `<span class="badge ${g.decision==="retrieve"?"retrieve":""}">gate · ${esc(g.decision)}</span><span class="meta" style="margin:0">${esc(g.reason||"")}</span>`;

// A tool call renders as a status row (dot + one-line summary); the raw output
// hides behind a disclosure so an ugly osascript error never floods the page.
const toolRow = x => `<div class="tool ${x.status}">
  <div class="tool-head"><span class="dot ${x.status}"></span><code>${esc(x.tool)}</code>
    <span style="color:var(--ink2)">${esc(x.summary)}</span></div>
  <details><summary>args &amp; raw output</summary>
    <pre>${esc(x.tool)}(${esc(JSON.stringify(x.args,null,1))})\n\n${esc(x.output)}</pre>
  </details>
</div>`;

const turnCard = t => `<div class="card">
  <div class="u">${esc(t.user_message)}</div>
  <div class="meta" style="margin-top:4px">${gateBadge(t.gate)}</div>
  ${(t.tools||[]).map(toolRow).join("")}
  <div class="r">${renderMarkdown(t.reply)}</div>
  <div class="meta">${esc((t.ts||"").replace("T"," ").slice(0,19))} · ${secs(t.latency_ms)} · ${t.iterations??"?"} iter · ${money(t.cost||0)}${t.consolidation?` · consolidated ${t.consolidation.new_facts} fact(s)`:""}</div>
</div>`;

function executionTurn(t, index){
  const llms = t.llm_calls || [], tools = t.tools || [];
  const tokensIn = t.tokens_in || llms.reduce((n,x)=>n+(x.in||0),0);
  const tokensOut = t.tokens_out || llms.reduce((n,x)=>n+(x.out||0),0);
  const when = (t.ts||"").replace("T"," ").slice(0,19) || "unknown time";
  const toolSteps = tools.length ? `<div class="execution-stage"><span class="stage-node tool-node">⌘</span><div class="stage-copy"><span class="stage-label">ACT</span><strong>${tools.length} tool call${tools.length===1?"":"s"}</strong>
    <div class="execution-tools">${tools.map(x=>`<details><summary><code>${esc(x.tool)}</code><span>${esc(Object.keys(x.args||{}).join(" · ")||"no arguments")}</span></summary><pre>${esc(JSON.stringify(x.args||{},null,2))}</pre></details>`).join("")}</div></div></div>` : "";
  return `<article class="execution-turn ${index===0?"latest":""}"><header><div><span class="turn-number">${String(index+1).padStart(2,"0")}</span><span class="turn-time">${esc(when)}</span></div><span class="turn-state"><i></i> complete</span></header>
    <div class="turn-prompt"><span>USER INPUT</span><strong>${esc(t.user_message||"No prompt recorded")}</strong></div>
    <div class="execution-path"><div class="execution-stage"><span class="stage-node">→</span><div class="stage-copy"><span class="stage-label">RECEIVE</span><strong>Context assembled</strong><small>session history · working context · available tools</small></div></div>
      <div class="execution-stage"><span class="stage-node model-node">✦</span><div class="stage-copy"><span class="stage-label">REASON</span><strong>${llms.length||t.iterations||1} model pass${(llms.length||t.iterations||1)===1?"":"es"}</strong><small>${tokensIn.toLocaleString()} tokens in · ${tokensOut.toLocaleString()} out</small></div></div>
      ${toolSteps}<div class="execution-stage"><span class="stage-node reply-node">✓</span><div class="stage-copy response-copy"><span class="stage-label">RESPOND</span><details ${index===0?"open":""}><summary>View final response</summary><div class="r">${renderMarkdown(t.reply||"")}</div></details></div></div></div>
    <footer><span>${secs(t.latency_ms)} elapsed</span><span>${t.iterations ?? (llms.length || 1)} iterations</span><span>${money(t.cost||0)}</span></footer></article>`;
}

const table = (heads, rows) => rows.length
  ? `<div class="card" style="padding:4px 8px"><table><tr>${heads.map(h=>`<th>${h}</th>`).join("")}</tr>${rows.join("")}</table></div>`
  : `<div class="card empty">nothing here yet</div>`;

const gateSplit = s => {
  if (!(s.gate_skips + s.gate_retrieves))
    return `<div class="splitbar"><div class="seg-skip" style="width:100%;opacity:.35"></div></div>
      <div class="meta" style="margin-top:6px">no retrieval decisions in today's trace yet</div>`;
  const tot = s.gate_skips + s.gate_retrieves;
  const skipPct = Math.round(s.gate_skips/tot*100), retPct = 100-skipPct;
  // only label a segment when it's wide enough to fit the text — otherwise a
  // 0%/tiny segment spills its label past the bar (the "0 retri" bug).
  const seg = (cls, n, label, pct) =>
    `<div class="${cls}" style="width:${pct}%">${pct>=14?`${n} ${label}`:""}</div>`;
  return `<div class="splitbar">
    ${seg("seg-skip", s.gate_skips, "skipped", skipPct)}
    ${seg("seg-ret", s.gate_retrieves, "retrieved", retPct)}
  </div><div class="meta" style="margin-top:6px">Mino invoked recall on ${retPct}% of traced turns and used live context on ${skipPct}%</div>`;
};

// --- Chat gateway: type here, watch the harness run (turns kept in memory)
const CHAT = [];
const chatTurnCard = t => `<div class="card">
  ${t.gate?`<div class="stages"><span class="stage done">gate · ${esc(t.gate.decision)}</span>${(t.tools||[]).map(x=>`<span class="stage done">tool · ${esc(x.tool)}</span>`).join("")}<span class="stage done">reply</span></div>
    <div class="meta" style="margin:0 0 6px">${esc(t.gate.reason||"")}</div>`:""}
  ${(t.tools||[]).map(toolRow).join("")}
  <div class="r" style="margin-top:8px">${renderCardBody(t.reply)}</div>
  <div class="meta">${secs(t.latency_ms)} · ${t.iterations??"?"} iter${t.consolidation?` · consolidated ${t.consolidation.new_facts} fact(s)`:""}</div>
</div>`;

// While a turn runs we stream it live: stages light up as the harness reaches
// them, and the reply text appears token by token (with a blinking caret).
const streamingCard = m => `<div class="card">
  <div class="stages">
    <span class="stage ${m.gate?"done":"on"}">gate${m.gate?` · ${esc(m.gate.decision)}`:""}</span>
    ${(m.tools||[]).map(x=>`<span class="stage done">tool · ${esc(x.tool)}</span>`).join("")}
    <span class="stage ${m.stream?"on":""}">reply</span>
  </div>
  ${m.gate&&m.gate.reason?`<div class="meta" style="margin:0 0 6px">${esc(m.gate.reason)}</div>`:""}
  ${(m.tools||[]).map(toolRow).join("")}
  ${m.stream
     ? `<div class="r" style="margin-top:8px">${renderCardBody(m.stream)}<span class="caret"></span></div>`
     : `<div class="meta" style="margin:0">thinking&hellip;</div>`}
</div>`;

// Messages loaded from history (a switched/opened conversation) have no live
// latency/iteration data, and their stored form carries an internal
// "[tools used: ...]" annotation — strip both so the thread reads cleanly.
const stripTools = t => (t || "").replace(/\s*\[tools used:[\s\S]*\]\s*$/, "").trim();
const historicalCard = m => `<div class="card"><div class="r">${renderCardBody(stripTools(m.reply))}</div></div>`;

// renderCardBody: markdown + image rendering for data URIs and saved image paths
function renderCardBody(text) {
  let out = renderMarkdown(text);
  // data:image URIs from view_image
  out = out.replace(/(data:image\/[^;]+;base64,[A-Za-z0-9+\/=]+)/g, '<img src="$1" class="chat-img" alt="generated image">');
  // Image saved to /tmp/mino/results/... paths
  out = out.replace(/Image saved to (\/tmp\/mino\/results\/[^\s]+)/g, 'Image saved to <a href="/api/files?path=$1" target="_blank">$1</a><br><img src="/api/files?path=$1" class="chat-img" alt="generated image">');
  return out;
}

function renderChatLog(){
  if (!CHAT.length)
    return `<div class="empty" style="padding:6px 2px">Message Mino here from any tab. Open Overview to watch it flow through the harness, or the Gateway tab to see every channel's messages together.</div>`;
  return CHAT.map(m => m.role==="user"
      ? `<div class="bubble">${esc(m.text)}</div>`
      : m.pending ? streamingCard(m)
      : m.historical ? historicalCard(m)
      : chatTurnCard(m)).join("");
}

function syncChatLogs(){
  // one conversation, two surfaces: the Chat & watch tab and the side dock
  document.querySelectorAll(".chatlog").forEach(el => {
    el.innerHTML = renderChatLog();
    el.scrollTop = el.scrollHeight;      // dock scrolls its own container
  });
}

// One streamed harness event updates the live card in place.
function applyStreamEvent(pending, ev){
  if (ev.kind === "gate") pending.gate = {decision: ev.decision, reason: ev.reason};
  else if (ev.kind === "text") pending.stream = (pending.stream || "") + (ev.delta || "");
  else if (ev.kind === "tool"){
    (pending.tools = pending.tools || []).push({
      tool: ev.tool, args: ev.args, output: ev.output,
      status: (ev.output||"").toLowerCase().startsWith("error") ? "error" : "ok",
      summary: (ev.output || "").split(". ")[0].slice(0,120)});
    pending.stream = "";   // a new assistant turn begins after the tool result
  } else if (ev.kind === "done"){
    pending.pending = false; pending.stream = "";
    if (ev.error) pending.reply = "Error: " + ev.error;
    else Object.assign(pending, ev);   // reply, tools, gate, iterations, latency_ms, consolidation
  }
}

async function sendChat(fromInput){
  const input = fromInput || document.getElementById("msg") || document.getElementById("dmsg");
  const text = (input && input.value || "").trim();
  if (!text) return;
  input.value = "";
  CHAT.push({role:"user", text});
  const pending = {role:"mino", pending:true, stream:""};
  CHAT.push(pending);
  syncChatLogs();
  try {
    const res = await fetch("/api/chat/stream", {method:"POST",
      headers:{"Content-Type":"application/json"}, body:JSON.stringify({message:text})});
    const reader = res.body.getReader(), dec = new TextDecoder();
    let buf = "";
    for (;;){
      const {value, done} = await reader.read();
      if (done) break;
      buf += dec.decode(value, {stream:true});
      let i;
      while ((i = buf.indexOf("\n\n")) >= 0){
        const line = buf.slice(0, i); buf = buf.slice(i + 2);
        if (!line.startsWith("data:")) continue;
        try { applyStreamEvent(pending, JSON.parse(line.slice(5).trim())); } catch(e){}
        syncChatLogs();
      }
    }
  } catch(e){ Object.assign(pending, {pending:false, reply:"Error: "+e}); }
  if (pending.pending) pending.pending = false;   // stream ended without a 'done'
  syncChatLogs();
  input.focus();
}
function wireDock(){
  const b = document.getElementById("dsend"), i = document.getElementById("dmsg");
  if (b) b.onclick = () => sendChat(i);
  if (i) i.onkeydown = e => { if (e.key==="Enter") sendChat(i); };
  const close = document.getElementById("dock-close"), reopen = document.getElementById("dock-reopen");
  const setClosed = v => { document.body.classList.toggle("dock-closed", v); localStorage.setItem("dockClosed", v?"1":"0"); };
  if (close) close.onclick = () => setClosed(true);
  if (reopen) reopen.onclick = () => setClosed(false);
  const saved = localStorage.getItem("dockClosed");
  setClosed(saved === null ? window.innerWidth < 1180 : saved === "1");
  syncChatLogs();
}

// --- Mino Runtime Blueprint: one live engineering sheet assembled from
// correspondence, memory evidence, specialist tools, and operational proof.
function archSVG(d){
  const s = d.stats;
  const latest = (d.turns||[])[0] || {};
  const attrs = (view,nid,title,sub) => `data-node="${nid}" tabindex="0" role="link"
    aria-label="${title}: ${sub}" onclick="location.hash='${view}'"
    onkeydown="if(event.key==='Enter'||event.key===' '){event.preventDefault();location.hash='${view}'}"`;
  const wire = (path,id,cls="") => `<path class="dossier-wire ${cls}" data-edge="${id}" d="${path}" marker-end="url(#dossier-arrow)"/>`;
  const module = (x,y,w,h,kicker,title,sub,view,nid,mark,cls="") =>
    `<g class="node dossier-node ${cls}" ${attrs(view,nid,title,sub)}><title>${title}: ${sub}</title>
      <rect class="target node-panel" x="${x}" y="${y}" width="${w}" height="${h}" rx="11"/>
      <path class="module-bracket" d="M${x+8} ${y+22}V${y+8}H${x+22}M${x+w-22} ${y+h-8}H${x+w-8}V${y+h-22}"/>
      <rect class="module-index" x="${x+13}" y="${y+15}" width="28" height="28" rx="7"/>
      <text class="module-mark" x="${x+27}" y="${y+33}" text-anchor="middle">${mark}</text>
      <text class="dossier-kicker" x="${x+52}" y="${y+20}">${kicker}</text>
      <text class="module-title" x="${x+52}" y="${y+40}">${title}</text>
      <text class="module-sub" x="${x+52}" y="${y+57}">${sub}</text></g>`;
  const chip = (x,y,w,title,count,view,nid,mark) =>
    `<g class="node dossier-node evidence-node" ${attrs(view,nid,title,`${count}`)}><title>${title}: ${count}</title>
      <rect class="target evidence-chip" x="${x}" y="${y}" width="${w}" height="44" rx="7"/>
      <path class="evidence-rule" d="M${x+5} ${y+5}V${y+39}"/>
      <text class="evidence-mark" x="${x+14}" y="${y+18}">${mark}</text><text class="evidence-title" x="${x+30}" y="${y+18}">${title}</text>
      <text class="evidence-count" x="${x+w-10}" y="${y+18}" text-anchor="end">${count}</text><text class="evidence-sub" x="${x+30}" y="${y+33}">ATTACHED EVIDENCE</text></g>`;
  const stamp = (x,y,title,sub,view,nid,mark) =>
    `<g class="node dossier-node stamp-node" ${attrs(view,nid,title,sub)}><title>${title}: ${sub}</title>
      <path class="stamp-ticks" d="M${x-31} ${y-18}V${y-31}H${x-18}M${x+18} ${y-31}H${x+31}V${y-18}M${x-31} ${y+18}V${y+31}H${x-18}M${x+18} ${y+31}H${x+31}V${y+18}"/>
      <circle class="target stamp-ring" cx="${x}" cy="${y}" r="25"/><circle class="stamp-inner" cx="${x}" cy="${y}" r="19"/>
      <text class="stamp-mark" x="${x}" y="${y+4}" text-anchor="middle">${mark}</text><text class="stamp-title" x="${x+36}" y="${y-3}">${title}</text>
      <text class="stamp-sub" x="${x+36}" y="${y+13}">${sub}</text></g>`;
  const words = (value,width=45) => {
    const source = String(value||"").replace(/\s+/g," ").trim() || "Awaiting the next request";
    const out=[]; let line="";
    for (const word of source.split(" ")){
      if (line && `${line} ${word}`.length > width){ out.push(line); line=word; if(out.length===2) break; }
      else line=`${line} ${word}`.trim();
    }
    if(out.length<2 && line) out.push(line);
    return out.slice(0,2).map((line,i)=>esc(line)+(i===1 && source.length>out.join(" ").length?"…":""));
  };
  const request = words(latest.user_message);
  const response = words(latest.reply,50);
  const requestText = request.map((line,i)=>`<tspan x="357" dy="${i?18:0}">${line}</tspan>`).join("");
  const responseText = response.map((line,i)=>`<tspan x="355" dy="${i?17:0}">${line}</tspan>`).join("");
  const caseNo = String(s.turns||0).padStart(4,"0");
  const toolCount = ((d.tools||{}).catalog||[]).length;
  const defs = `<defs>
    <linearGradient id="dossier-bg" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#06111f"/><stop offset=".52" stop-color="#08213a"/><stop offset="1" stop-color="#071523"/></linearGradient>
    <linearGradient id="case-sheet" x1="0" y1="0" x2="0" y2="1"><stop stop-color="#0d2a47"/><stop offset="1" stop-color="#091c31"/></linearGradient>
    <linearGradient id="holo-scan" x1="0" y1="0" x2="1" y2="0"><stop stop-color="#67e8f9" stop-opacity="0"/><stop offset=".5" stop-color="#67e8f9" stop-opacity=".42"/><stop offset="1" stop-color="#67e8f9" stop-opacity="0"/></linearGradient>
    <pattern id="dossier-grid" width="11" height="11" patternUnits="userSpaceOnUse"><path d="M11 0H0V11" fill="none" stroke="#4b7ba4" stroke-width=".32"/><path d="M55 0H0V55" fill="none" stroke="#64a4ca" stroke-width=".62"/></pattern>
    <pattern id="blueprint-hatch" width="7" height="7" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><path d="M0 0V7" stroke="#78a9c7" stroke-width=".45" opacity=".25"/></pattern>
    <filter id="dossier-glow" x="-80%" y="-80%" width="260%" height="260%"><feGaussianBlur stdDeviation="2.3" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>
    <marker id="dossier-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto"><path d="M0 0L10 5L0 10Z" class="wire-head"/></marker>
  </defs>`;
  const core = (x,y,compact=false) => `<g class="node dossier-node mino-core" ${attrs("loop","llm","Mino case handler","reasoning on this dossier")}>
    <title>Mino: reasoning on this dossier</title><path class="core-axis" d="M${x-(compact?45:51)} ${y}H${x+(compact?45:51)}M${x} ${y-(compact?45:51)}V${y+(compact?45:51)}"/><circle class="core-scan" cx="${x}" cy="${y}" r="${compact?37:43}"/>
    <circle class="target assistant-core" cx="${x}" cy="${y}" r="${compact?30:35}"/><circle class="assistant-head" cx="${x}" cy="${y-(compact?7:8)}" r="${compact?8:9}"/>
    <path class="assistant-body" d="M${x-(compact?16:18)} ${y+(compact?18:21)}Q${x} ${y+(compact?1:2)} ${x+(compact?16:18)} ${y+(compact?18:21)}"/>
    <text class="assistant-name" x="${x}" y="${y+(compact?55:64)}" text-anchor="middle">MINO</text><text class="assistant-role" x="${x}" y="${y+(compact?68:78)}" text-anchor="middle">RUNTIME CORE</text></g>`;
  const archive = (compact=false) => compact ? `<g class="archive-block">
      <text class="dossier-kicker" x="38" y="563">MEMORY EVIDENCE INDEX</text>
      <g class="node dossier-node gate-node" ${attrs("memory/overview","gate","Memory gate",`${s.gate_skips} skip · ${s.gate_retrieves} retrieve`)}><rect class="target archive-gate" x="30" y="578" width="360" height="48" rx="8"/><text class="gate-symbol" x="49" y="607">◇</text><text class="archive-title" x="72" y="598">RETRIEVAL GATE</text><text class="archive-sub" x="72" y="614">${s.gate_skips} SKIP · ${s.gate_retrieves} RETRIEVE</text></g>
      ${chip(30,638,112,"Skills",(d.skills||[]).length,"memory/skills","procedural","S")}${chip(154,638,112,"Facts",(d.facts||[]).length,"memory/semantic","semantic","F")}${chip(278,638,112,"Episodes",(d.episodes||[]).length,"memory/episodic","episodic","E")}
      <g class="node dossier-node consolidation-node" ${attrs("memory/consolidation","consolidation","Consolidation",`${d.chat_pending||0} queued`)}><rect class="target archive-consolidation" x="30" y="694" width="236" height="43" rx="7"/><text class="archive-title" x="44" y="712">CONSOLIDATION</text><text class="archive-sub" x="44" y="727">${d.chat_pending||0} QUEUED · ${esc(d.consolidate_every)}</text></g></g>` : `<g class="archive-block">
      <rect class="archive-shell" x="28" y="333" width="229" height="260" rx="13"/><path class="archive-hatch" d="M29 348H256V378H29Z"/><text class="dossier-kicker" x="45" y="358">MEMORY EVIDENCE INDEX</text><text class="archive-code" x="239" y="358" text-anchor="end">M-04</text>
      <g class="node dossier-node gate-node" ${attrs("memory/overview","gate","Memory gate",`${s.gate_skips} skip · ${s.gate_retrieves} retrieve`)}><rect class="target archive-gate" x="43" y="373" width="199" height="53" rx="8"/><text class="gate-symbol" x="58" y="403">◇</text><text class="archive-title" x="82" y="394">RETRIEVAL GATE</text><text class="archive-sub" x="82" y="411">${s.gate_skips} SKIP · ${s.gate_retrieves} RETRIEVE</text></g>
      ${chip(43,438,199,"Skills",(d.skills||[]).length,"memory/skills","procedural","S")}${chip(43,489,199,"Facts",(d.facts||[]).length,"memory/semantic","semantic","F")}${chip(43,540,199,"Episodes",(d.episodes||[]).length,"memory/episodic","episodic","E")}
      <g class="node dossier-node consolidation-node" ${attrs("memory/consolidation","consolidation","Consolidation",`${d.chat_pending||0} queued`)}><rect class="target archive-consolidation" x="43" y="599" width="199" height="34" rx="7"/><text class="archive-title" x="55" y="620">CONSOLIDATE · ${d.chat_pending||0} QUEUED</text></g></g>`;

  if (window.innerWidth < 720) return `<div class="arch-wrap dossier-wrap"><svg viewBox="0 0 420 820" class="arch dossier-arch arch-compact" role="img" aria-labelledby="dossier-title dossier-desc">
    <title id="dossier-title">Mino live runtime blueprint</title><desc id="dossier-desc">A live engineering sheet showing correspondence, context, Mino reasoning, memory evidence, tools, reply dispatch, and operational proof.</desc>
    ${defs}<rect class="dossier-stage" x="4" y="4" width="412" height="812" rx="22"/><rect class="dossier-grid" x="5" y="5" width="410" height="810" rx="21"/>
    <path class="blueprint-ruler" d="M22 45H398M22 47V42M66 47V42M110 47V42M154 47V42M198 47V42M242 47V42M286 47V42M330 47V42M374 47V42"/>
    <text class="dossier-brand" x="25" y="29">MINO // RUNTIME BLUEPRINT</text><text class="dossier-live" x="395" y="29" text-anchor="end">SHEET ${caseNo}</text>
    ${wire("M174 92H205","e-gw-wm")}${wire("M294 123V179","e-wm-loop")}${wire("M210 510V578","e-reply-save","dashed")}${wire("M330 501H382V766H345","e-reply-trace")}
    ${wire("M210 578V538H275V500","e-gate-wm","dashed")}${wire("M86 638V626","e-gate-proc","dashed")}${wire("M210 638V626","e-gate-sem","dashed")}${wire("M334 638V626","e-gate-epi","dashed")}${wire("M266 716H278V660","e-consol-sem","dashed")}
    ${module(22,58,152,72,"CORRESPONDENCE","Gateway",`${(d.sessions||[]).length} open threads`,"gateway","gateway","IN","compact-module")}
    ${module(205,58,193,72,"CONTEXT PACKET","Assembled",`${s.turns} turns available`,"memory/overview","wm","CX","compact-module")}
    <g class="case-stack"><rect class="case-shadow back" x="35" y="166" width="350" height="342" rx="5"/><rect class="case-shadow mid" x="29" y="160" width="350" height="342" rx="5"/><path class="case-sheet" d="M23 176V151H150L168 168H391V518H23Z"/>
      <path class="case-rule" d="M42 223H372M42 360H372M42 435H372"/><rect class="case-scanline" x="27" y="185" width="360" height="3"/>
      <text class="sheet-tab" x="42" y="164">SHEET 03 / RESPONSE ASSEMBLY</text><text class="dossier-kicker" x="43" y="192">CURRENT REQUEST</text><text class="case-state" x="368" y="192" text-anchor="end">● LIVE</text>
      <text class="request-copy" x="43" y="212">${request.map((line,i)=>`<tspan x="43" dy="${i?17:0}">${line}</tspan>`).join("")}</text>
      ${core(112,302,true)}
      <text class="dossier-kicker" x="174" y="271">PROCESSOR / MINO</text><text class="case-heading" x="174" y="292">Context under review</text>
      <path class="analysis-line" d="M174 310H342M174 324H318M174 338H352"/><circle class="analysis-cursor" cx="357" cy="338" r="3"/>
      <g class="node dossier-node tools-node" ${attrs("tools","tools","Specialist tools",`${toolCount} available`)}><rect class="target tool-attachment" x="259" y="368" width="112" height="53" rx="8"/><text class="attachment-clip" x="273" y="388">⌘</text><text class="archive-title" x="294" y="387">TOOLS</text><text class="archive-sub" x="294" y="403">${toolCount} AVAILABLE</text></g>
      <g class="node dossier-node reply-node" ${attrs("loop","reply","Reply dispatch","back to you")}><rect class="target dispatch-button" x="43" y="448" width="328" height="51" rx="8"/><text class="dispatch-label" x="57" y="468">RESPONSE READY</text><text class="dispatch-copy" x="57" y="486">${esc(response[0]||"Awaiting response")}</text><text class="dispatch-enter" x="354" y="478" text-anchor="end">ENTER ↵</text></g>
    </g>
    ${archive(true)}
    ${stamp(305,766,"Trace",`${s.trace_files} files`,"ops/traces","trace","T")}${stamp(75,776,"Release",d.eval_report?d.eval_report.deterministic:"make gate","ops/release","release","R")}
    <g class="node dossier-node stamp-node" ${attrs("ops/release","eval","Evaluate","tests and judge")}><circle class="target stamp-ring" cx="190" cy="776" r="25"/><circle class="stamp-inner" cx="190" cy="776" r="19"/><text class="stamp-mark" x="190" y="780" text-anchor="middle">E</text></g>
  </svg></div>`;

  return `<div class="arch-wrap dossier-wrap"><svg viewBox="0 0 1040 660" class="arch dossier-arch" role="img" aria-labelledby="dossier-title dossier-desc">
    <title id="dossier-title">Mino live runtime blueprint</title><desc id="dossier-desc">A live engineering sheet showing correspondence, context, Mino reasoning, memory evidence, tools, reply dispatch, and operational proof.</desc>
    ${defs}<rect class="dossier-stage" x="5" y="5" width="1030" height="650" rx="23"/><rect class="dossier-grid" x="6" y="6" width="1028" height="648" rx="22"/>
    <path class="blueprint-ruler" d="M29 52H1011M29 55V48M84 55V48M139 55V48M194 55V48M249 55V48M304 55V48M359 55V48M414 55V48M469 55V48M524 55V48M579 55V48M634 55V48M689 55V48M744 55V48M799 55V48M854 55V48M909 55V48M964 55V48M1011 55V48"/>
    <text class="dossier-brand" x="30" y="36">MINO // LIVE RUNTIME BLUEPRINT</text><text class="dossier-live" x="1010" y="36" text-anchor="end">SHEET ${caseNo} · ${s.turns} TURNS · REV 02</text>
    <path class="registration-mark" d="M28 68V55H41M999 55H1012V68M28 626V639H41M999 639H1012V626"/>
    ${wire("M225 133H263V169H300","e-gw-wm")}${wire("M225 263H273V287H323","e-wm-loop")}
    ${wire("M242 399H272V310H322","e-gate-wm","dashed")}${wire("M142 438V426","e-gate-proc","dashed")}${wire("M142 489V477","e-gate-sem","dashed")}${wire("M142 540V528","e-gate-epi","dashed")}${wire("M242 616H278V511H324","e-reply-save","dashed")}${wire("M242 616V512","e-consol-sem","dashed")}
    ${wire("M742 474H780V440H819","e-reply-trace")}
    ${module(28,82,197,101,"CORRESPONDENCE","Gateway",`${(d.sessions||[]).length} open threads · every channel`,"gateway","gateway","IN")}
    ${module(28,207,197,101,"CONTEXT PACKET","Assembled",`${s.turns} traced turns available`,"memory/overview","wm","CX")}
    ${archive(false)}
    <g class="case-stack"><rect class="case-shadow back" x="315" y="84" width="440" height="520" rx="6"/><rect class="case-shadow mid" x="307" y="77" width="440" height="520" rx="6"/>
      <path class="case-sheet" d="M299 104V70H470L493 92H758V609H299Z"/>
      <path class="case-rule" d="M324 180H733M324 392H733M324 486H733"/><rect class="case-scanline" x="304" y="112" width="449" height="4"/>
      <text class="sheet-tab" x="322" y="84">SHEET 03 / RESPONSE ASSEMBLY</text><text class="dossier-kicker" x="324" y="111">ACTIVE REQUEST</text><text class="case-state" x="730" y="111" text-anchor="end">● LIVE · PRIORITY NORMAL</text>
      <text class="request-copy" x="324" y="137">${requestText}</text><text class="case-id" x="730" y="159" text-anchor="end">RUNTIME SHEET / ${caseNo}</text>
      ${core(386,286)}
      <text class="dossier-kicker" x="457" y="237">PROCESSOR / MINO</text><text class="case-heading" x="457" y="262">Context under review</text>
      <text class="case-note" x="457" y="284">One assistant coordinates the evidence, tools,</text><text class="case-note" x="457" y="302">and final response inside this live record.</text>
      <path class="analysis-line" d="M457 330H690M457 347H650M457 364H708"/><circle class="analysis-cursor" cx="714" cy="364" r="3"/>
      <text class="dossier-kicker" x="324" y="421">ATTACHED MATERIAL</text>
      <rect class="attachment-slot" x="324" y="435" width="185" height="35" rx="6"/><text class="attachment-slot-label" x="339" y="456">MEMORY EVIDENCE</text><text class="attachment-slot-count" x="493" y="456" text-anchor="end">${(d.facts||[]).length+(d.episodes||[]).length}</text>
      <g class="node dossier-node tools-node" ${attrs("tools","tools","Specialist tools",`${toolCount} available`)}><rect class="target tool-attachment" x="520" y="422" width="212" height="48" rx="7"/><text class="attachment-clip" x="538" y="452">⌘</text><text class="archive-title" x="565" y="443">SPECIALIST TOOLS</text><text class="archive-sub" x="565" y="459">${toolCount} AVAILABLE · RESULTS ATTACH HERE</text></g>
      <g class="node dossier-node reply-node" ${attrs("loop","reply","Reply dispatch","back to you")}><rect class="target dispatch-button" x="324" y="502" width="408" height="82" rx="9"/><text class="dispatch-label" x="343" y="526">APPROVED RESPONSE</text>
        <text class="dispatch-copy" x="343" y="549">${responseText}</text><rect class="enter-key" x="644" y="524" width="70" height="42" rx="7"/><text class="dispatch-enter" x="679" y="549" text-anchor="middle">ENTER ↵</text></g>
    </g>
    <g class="ops-proof"><text class="dossier-kicker" x="800" y="94">VERIFICATION RAIL</text>
      ${module(788,110,220,101,"SPECIALIST NETWORK","Tools",`${toolCount} available capabilities`,"tools","tools","⌘","tools-module")}
      ${stamp(823,275,"Trace",`${s.trace_files} files`,"ops/traces","trace","T")}${stamp(823,355,"Evaluate","tests + judge","ops/release","eval","E")}${stamp(823,435,"Release",d.eval_report?d.eval_report.deterministic:"make gate","ops/release","release","R")}
      <path class="proof-rail" d="M823 300V330M823 380V410"/>
      <text class="proof-note" x="800" y="515">Every completed turn leaves</text><text class="proof-note" x="800" y="532">a trace, an evaluation path, and</text><text class="proof-note" x="800" y="549">a deliberate release record.</text></g>
  </svg></div>`;
}

// --- sub-tabs: keep long pages short by splitting them into hash-routed tabs
// (#memory/semantic, #database/facts). Each tab is a plain link, so it's
// bookmarkable and the architecture cards can deep-link straight to one.
function subtabBar(view, tabs, active){
  return `<div class="subtabs">${tabs.map(([key,label,n]) =>
    `<a class="subtab ${key===active?"on":""}" href="#${view}/${key}">${esc(label)}${
      n!=null?`<span class="n">${n}</span>`:""}</a>`).join("")}</div>`;
}

// A raw SQLite table, scrollable, with the column names AS the (indigo) sticky
// headers so the schema lines up over its data instead of floating above it.
function dbTable(t){
  const sample = t.sample || [], columns = t.columns || [];
  if (!sample.length) return `<div class="card empty">empty — no rows yet</div>`;
  const head = columns.map(c => `<th class="dbcol">${esc(c)}${
    t.types&&t.types[c]?`<small>${esc(t.types[c].toLowerCase())}</small>`:""}</th>`).join("");
  const body = sample.map(r => `<tr>${columns.map(c =>
    `<td class="dbcell">${esc(String(r[c]??"").slice(0,120))}</td>`).join("")}</tr>`).join("");
  return `<div class="scrolly"><table><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table></div>
    <div class="meta" style="margin-top:6px">showing ${sample.length} of ${t.count} row${t.count===1?"":"s"} (newest first)</div>`;
}
const DB_DESC = {
  calendar_events: "events the create_event tool wrote (the flagship task)",
  facts: "semantic memory — durable facts (Memory ▸ Semantic)",
  episodes: "episodic memory — dated summaries (Memory ▸ Episodic)",
  chat_log: "every message, tagged by session_id — consolidation reads from here",
};
const QUERY_EXAMPLES = [
  "SELECT role, content FROM chat_log ORDER BY id DESC LIMIT 10",
  "SELECT subject, content FROM facts",
  "SELECT session_id, COUNT(*) FROM chat_log GROUP BY session_id",
];
function dbQueryView(){
  return `<section class="surface-head"><div><span class="section-kicker">READ-ONLY CONSOLE</span><h2>Query state.db</h2><p>Inspect live state with SELECT. Mutating statements are rejected by the server.</p></div><strong>SQLITE</strong></section>
    <section class="query-console"><header><span><i></i><i></i><i></i></span><code>state.db / query</code><small>read only</small></header><textarea class="sqlbox" id="sqlbox" spellcheck="false" aria-label="SQL query" onfocus="markEditing()" oninput="markEditing()" onkeydown="if((event.metaKey||event.ctrlKey)&&event.key==='Enter'){event.preventDefault();runQuery()}">${esc(QUERY_EXAMPLES[0])}</textarea><footer><button class="save" onclick="runQuery()">Run query <span>▶</span></button><span>⌘ Enter</span></footer></section>
    <div class="query-examples"><span>EXAMPLES</span>${QUERY_EXAMPLES.map(q=>`<button onclick="qFill(this.textContent)">${esc(q)}</button>`).join("")}</div><div id="qout" aria-live="polite"></div>`;
}

// --- chat sessions (the "New chat" + history picker, like a chat app)
let SESSION = "default";
async function newChat(){
  const r = await postJSON("/api/session", {action:"new"});
  if (r.session_id){ liveView = null; SESSION = r.session_id; CHAT.length = 0; syncChatLogs(); }
  closeSessMenu();
}
async function switchSession(id){
  const r = await postJSON("/api/session", {action:"switch", id});
  if (r.ok){
    SESSION = r.session_id; CHAT.length = 0;
    (r.history||[]).forEach(m => CHAT.push(m.role==="user"
      ? {role:"user", text:m.content} : {role:"mino", reply:m.content, historical:true}));
    syncChatLogs();
  }
  closeSessMenu();
}
// Open a conversation from the Gateway inbox: load it into the dock (the active
// thread), keep it live-synced (so new Telegram/voice messages appear), and make
// sure the dock is visible.
let liveView = null;   // a conversation opened from the inbox, kept live-updated
async function openConversation(id){
  document.body.classList.remove("dock-closed");
  localStorage.setItem("dockClosed", "0");
  liveView = id;
  await switchSession(id);   // switch the agent so a reply continues this thread
  render();                  // reflect the active-session highlight in the inbox
}
// Re-pull the opened conversation each refresh so incoming messages from another
// gateway (your phone) show up live — unless a turn is mid-stream in the dock.
async function syncLiveView(){
  if (!liveView || CHAT.some(m => m.pending)) return;
  const r = await postJSON("/api/session", {action:"history", id:liveView});
  if (!r.ok) return;
  const fresh = (r.history||[]).map(m => m.role==="user"
    ? {role:"user", text:m.content} : {role:"mino", reply:m.content, historical:true});
  if (fresh.length !== CHAT.length){   // only redraw when it actually changed
    CHAT.length = 0; fresh.forEach(m => CHAT.push(m)); syncChatLogs();
  }
}
function closeSessMenu(){ const m=document.getElementById("sessmenu"); if(m) m.remove(); }
function toggleSessMenu(ev){
  ev.stopPropagation();
  if (document.getElementById("sessmenu")){ closeSessMenu(); return; }
  const sessions = (D && D.sessions) || [];
  const menu = document.createElement("div");
  menu.className = "sessmenu"; menu.id = "sessmenu";
  menu.innerHTML = sessions.length ? sessions.map(s => {
    const tags = (s.sources||[]).map(src => `<span class="gwtag ${esc(src)}">${esc(src)}</span>`).join("");
    return `<div class="sessitem ${s.id===SESSION?"on":""}" onclick="openConversation('${esc(s.id)}')">
      <div>${esc(s.title||s.id)} ${tags}</div>
      <div class="sm">${s.messages} msg · ${esc((s.last_at||"").slice(0,16).replace("T"," "))}</div>
    </div>`;
  }).join("") : `<div class="sessitem">no past conversations yet</div>`;
  const r = ev.currentTarget.getBoundingClientRect();
  menu.style.top = (r.bottom+6)+"px";
  menu.style.left = Math.max(8, r.right-300)+"px";
  document.body.appendChild(menu);
}
document.addEventListener("click", e => {
  const m = document.getElementById("sessmenu");
  if (m && !m.contains(e.target)) closeSessMenu();
});
// --- read-only SQL console (item: "a simple query editor like Supabase")
function qFill(sql){ const b=document.getElementById("sqlbox"); if(b){ b.value=sql; runQuery(); } }
async function runQuery(){
  editing = true;   // keep the 5s refresh from wiping the query + results
  const sql = (document.getElementById("sqlbox")||{}).value || "";
  const out = document.getElementById("qout");
  out.innerHTML = `<div class="meta">running…</div>`;
  const r = await postJSON("/api/query", {sql});
  if (r.error){ out.innerHTML = `<div class="card empty" style="color:var(--bad)">${esc(r.error)}</div>`; return; }
  if (!r.rows.length){ out.innerHTML = `<div class="card empty">0 rows</div>`; return; }
  out.innerHTML = `<div class="scrolly"><table><thead><tr>${
    r.columns.map(c=>`<th class="dbcol">${esc(c)}</th>`).join("")}</tr></thead><tbody>${
    r.rows.map(row=>`<tr>${row.map(v=>`<td class="dbcell">${esc(String(v).slice(0,120))}</td>`).join("")}</tr>`).join("")
    }</tbody></table></div><div class="meta" style="margin-top:6px">${r.rows.length} row(s)</div>`;
}

// --- Memory sub-tabs. Memory is the friendly, per-pillar view of what persists;
// the Data tab shows the SAME rows as raw SQLite tables (see the explainer).
function memOverview(d){
  const s = d.stats;
  const facts = (d.facts||[]).length, episodes = (d.episodes||[]).length, skills = (d.skills||[]).length;
  const pillars = [
    ["✦","Semantic","semantic",facts,"facts","Durable knowledge Mino can retrieve across conversations."],
    ["◷","Episodic","episodic",episodes,"episodes","Dated highlights distilled from longer conversations."],
    ["⌘","Procedural","skills",skills,"skills","Reusable instructions loaded only when they are relevant."],
  ].map(([icon,t,sub,n,unit,desc]) => `<div class="memory-pillar" role="link" tabindex="0" onclick="location.hash='memory/${sub}'" onkeydown="if(event.key==='Enter'||event.key===' '){event.preventDefault();location.hash='memory/${sub}'}">
      <span class="memory-pillar-icon">${icon}</span><div><span>${t}</span><strong>${n} ${unit}</strong><p>${desc}</p></div><b>→</b></div>`).join("");
  return `<section class="memory-hero"><div><div class="eyebrow">MEMORY OBSERVATORY</div><h2 class="memory-title">What Mino carries forward.</h2>
      <p>Inspect durable knowledge, lived context, reusable skills, and the pipeline that keeps them current.</p></div>
      <div class="memory-health"><span class="runtime-kicker"><i></i> MEMORY STATUS</span><strong>${facts+episodes+skills} records</strong><span>${d.chat_pending||0} messages queued</span><small>SQLite · FTS5 · human-readable mirror</small></div></section>
    <section class="memory-pillar-grid">${pillars}</section>
    <section class="memory-retrieval"><div class="overview-section-head"><div><span class="section-kicker">RETRIEVAL</span><h2>Memory enters only when needed</h2></div><span class="section-note">the gate protects latency and relevance</span></div>${gateSplit(s)}</section>
    <section class="memory-source"><div><span class="section-kicker">ONE SOURCE · TWO VIEWS</span><h3>Curated here. Auditable in SQLite.</h3><p>Memory presents the useful mental model; Database exposes the exact same facts, episodes, and FTS5 indexes at row level.</p></div>
      <a href="#database">Open database →</a></section>
    <div class="memory-files"><span>FILES</span>${reveal("state.db","state.db")}${reveal("MEMORY.md","MEMORY.md")}${reveal("SOUL.md","SOUL.md")}${reveal("skills","skills/")}</div>`;
}
function memSemantic(d){
  const facts = d.facts || [];
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">SEMANTIC MEMORY</span><h2>Durable facts</h2><p>The smallest, most reusable knowledge store. Corrections and deletions are active on the next turn.</p></div><strong>${facts.length}</strong></section>`;
  if (!facts.length) return h + `<div class="memory-empty"><span>✦</span><strong>No facts stored yet</strong><p>Mino will place durable knowledge here when memory tools or consolidation save it.</p></div>`;
  h += `<div class="memory-records">${facts.map(f => `<div class="memory-record" id="fact-${f.id}">
      <div class="record-subject"><span>${esc(f.subject)}</span><small>${esc(f.source||"unknown source")}</small></div>
      <div class="fc">${esc(f.content)}</div><div class="record-date">${esc((f.created_at||"").slice(0,10)||"—")}</div>
      <div class="record-actions"><a class="reveal" onclick="editFact(${f.id})">edit</a><a class="reveal del" onclick="delMem('delete_fact',${f.id})">delete</a></div></div>`).join("")}</div>`;
  return h;
}
function memEpisodic(d){
  const episodes = d.episodes || [];
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">EPISODIC MEMORY</span><h2>Conversation highlights</h2><p>One distilled summary per consolidation. Raw messages remain available in the chat log.</p></div><strong>${episodes.length}</strong></section>
    <div class="memory-callout"><span>◷</span><p>Episodes stay intentionally small: they preserve what happened without replaying every message. <a href="#database/chat_log">Inspect the raw chat log →</a></p></div>`;
  if (!episodes.length) return h + `<div class="memory-empty"><span>◷</span><strong>No episodes yet</strong><p>Conversation highlights will appear here after a successful consolidation.</p></div>`;
  h += `<div class="episode-timeline">${episodes.map(e => `<div class="episode-item"><span class="episode-dot"></span><div><time>${esc(e.happened_at||"Undated")}</time><p>${esc(e.summary)}</p></div><a class="reveal del" onclick="delMem('delete_episode',${e.id})">delete</a></div>`).join("")}</div>`;
  return h;
}
function memSkills(d){
  const skills = d.skills || [];
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">PROCEDURAL MEMORY</span><h2>Reusable skills</h2><p>Instructions loaded only when a message matches. Teach Mino in chat, edit below, or add a SKILL.md file.</p></div><strong>${skills.length}</strong></section>
    <div class="memory-callout"><span>⌘</span><p>Skills are selective context, not permanent prompt weight. ${reveal("skills","Open the skills folder →")}</p></div>`;
  h += skills.map((sk,i) => {
    const full = `---
name: ${sk.name}
description: ${sk.description}
---

${sk.body}`;
    return `<div class="memory-editor-card"><div class="memory-editor-head"><div><code>${esc(sk.name)}</code><p>${esc(sk.description)}</p></div>
        <span class="srcpill ${sk.editable?"":"apple"}">${sk.editable?"home":"built-in"}</span></div>
      <textarea class="editor" id="sk-${i}" style="min-height:150px;margin-top:8px" data-path="${esc(sk.path)}"
        oninput="dirty('sksave-${i}')" onfocus="markEditing()">${esc(full)}</textarea>
      <div class="memory-editor-actions"><button class="save" id="sksave-${i}" disabled onclick="saveSkill(${i})">Save SKILL.md</button>
        <span class="meta" id="skmsg-${i}">${esc(sk.rel)}</span></div></div>`;
  }).join("") || `<div class="memory-empty"><span>⌘</span><strong>No skills loaded</strong><p>Create one in chat or place a SKILL.md in the skills folder.</p></div>`;
  return h;
}
function memSoul(d){
  return `<section class="memory-tab-head"><div><span class="section-kicker">IDENTITY</span><h2>Mino’s SOUL</h2><p>The persona and operating character loaded on every turn. Changes become active on the next message.</p></div><strong>SOUL.md</strong></section>
    <div class="memory-callout soul-warning"><span>◇</span><p>This file shapes how Mino speaks and decides. Review changes deliberately before saving.</p></div>
    <div class="memory-editor-card soul-editor"><textarea id="soul" class="editor" style="min-height:300px"
      oninput="dirty('soul-save')" onfocus="markEditing()">${esc(d.soul||"")}</textarea>
    <div class="memory-editor-actions"><button class="save" id="soul-save" disabled onclick="saveSoul()">Save SOUL.md</button>
      <span class="meta" id="soul-msg"></span><span class="editor-spacer"></span>${reveal("SOUL.md","open in editor")}</div></div>`;
}
function memConsolidation(d){
  const distilled = (d.facts||[]).filter(f => f.source==="consolidation");
  const queued = d.chat_pending||0, threshold = d.consolidate_every*2;
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">CONSOLIDATION</span><h2>From conversation to memory</h2><p>Bounded batches turn raw chat into durable facts and one dated episode.</p></div><strong class="${queued?"queue-live":""}">${queued} queued</strong></section>
    <div class="consolidation-flow"><div><span>1</span><strong>Chat log</strong><small>${queued} unconsolidated messages</small></div><b>→</b><div><span>2</span><strong>Distill</strong><small>every ${d.consolidate_every} exchanges</small></div><b>→</b><div><span>3</span><strong>Remember</strong><small>facts + episode</small></div></div>`;
  h += `<div class="consolidation-metrics"><div><strong>${queued}</strong><span>messages queued</span></div><div><strong>${threshold}</strong><span>trigger threshold</span></div><div><strong>${distilled.length}</strong><span>distilled facts</span></div><div><strong>${(d.episodes||[]).length}</strong><span>episodes total</span></div></div>`;
  h += `<div class="overview-section-head memory-list-head"><div><span class="section-kicker">OUTPUT</span><h2>Facts from consolidation</h2></div><span class="section-note">also traced in Ops</span></div>`;
  h += table(["subject","fact","when"], distilled.map(f =>
    `<tr><td><code>${esc(f.subject)}</code></td><td>${esc(f.content)}</td><td class="meta">${esc((f.created_at||"").slice(0,10))}</td></tr>`));
  h += `<div class="memory-files"><span>OBSERVE</span><a href="#database/chat_log">raw chat log</a><a href="#ops">consolidation traces</a></div>`;
  return h;
}

// Tools ▸ Results: the artifacts tool calls produced (kept distinct from the
// tools themselves — the old tab conflated capability with output).
function toolsResults(d){
  const recent = (d.turns||[]).flatMap(t=>(t.tools||[]).map(x=>({...x,ts:t.ts}))).slice(0,12);
  const events = d.calendar||[], drafts = d.outbox||[];
  let h = `<section class="surface-head"><div><span class="section-kicker">TOOL OUTPUT</span><h2>Results and artifacts</h2><p>What Mino’s tools produced, separated from the capability catalogue.</p></div><strong>${recent.length} recent calls</strong></section>
    <div class="result-metrics"><div><span>CALENDAR</span><strong>${events.length}</strong><small>saved events</small></div><div><span>OUTBOX</span><strong>${drafts.length}</strong><small>drafted messages</small></div><div><span>TRACE</span><strong>${recent.length}</strong><small>recent invocations</small></div></div>
    <div class="overview-section-head"><div><span class="section-kicker">RECENT ACTIVITY</span><h2>Tool invocations</h2></div><span class="section-note">arguments are visible, secrets are not</span></div>`;
  h += recent.length ? `<div class="tool-activity">${recent.map(x=>`<div><span class="activity-icon">⌘</span><div><code>${esc(x.tool)}</code><p>${esc(JSON.stringify(x.args||{}).slice(0,180))}</p></div><time>${esc((x.ts||"").replace("T"," ").slice(0,16))}</time></div>`).join("")}</div>` : `<div class="surface-empty"><span>⌘</span><strong>No tool calls traced yet</strong><p>Calls will appear here as Mino acts in the world.</p></div>`;
  h += `<div class="two-column-section"><section><div class="overview-section-head"><div><span class="section-kicker">CALENDAR</span><h2>Scheduled events</h2></div>${reveal("calendar.ics","open calendar.ics")}</div>${events.length?`<div class="compact-list">${events.map(e=>`<div><span class="list-glyph">◷</span><div><strong>${esc(e.title)}</strong><small>${esc(e.start)}${e.attendees?` · ${esc(e.attendees)}`:""}</small></div></div>`).join("")}</div>`:`<div class="surface-empty compact"><span>◷</span><strong>No calendar output</strong></div>`}</section>
    <section><div class="overview-section-head"><div><span class="section-kicker">OUTBOX</span><h2>Drafted messages</h2></div>${reveal("outbox","open folder")}</div>${drafts.length?`<div class="compact-list">${drafts.map(o=>`<div><span class="list-glyph">↗</span><div><strong>${esc(o.name)}</strong><small>${esc(o.text).slice(0,140)}</small></div></div>`).join("")}</div>`:`<div class="surface-empty compact"><span>↗</span><strong>No message drafts</strong></div>`}</section></div>`;
  return h;
}
// Tools ▸ MCP: external connectors. Shows live status + a copy-paste config so
// anyone can plug in their own server (scalable, not a one-off).
function toolsMCP(t){
  const m = {...(t.mcp||{}), servers:(t.mcp&&t.mcp.servers)||[]};
  const state = m.live ? "connected" : m.configured ? "configured" : "not configured";
  let h = `<section class="connector-hero ${m.live?"connected":""}"><div><span class="section-kicker">MODEL CONTEXT PROTOCOL</span><h2>External connectors</h2><p>Attach filesystems, databases, and third-party services without adding them to Mino’s core.</p></div><div class="connector-state"><i></i><strong>${state}</strong><span>${m.servers.length} server${m.servers.length===1?"":"s"}</span></div></section>`;
  h += m.servers.length ? `<div class="connector-grid">${m.servers.map(name=>`<div><span class="connector-icon">↗</span><div><strong>${esc(name)}</strong><small>MCP server · tools namespaced</small></div><span class="status-chip good">connected</span></div>`).join("")}</div>` : `<div class="surface-empty"><span>↗</span><strong>No MCP servers attached</strong><p>Add one configuration file to extend Mino’s available tools.</p></div>`;
  h += `<section class="setup-card"><div class="overview-section-head"><div><span class="section-kicker">CONNECT A SERVER</span><h2>One file, then restart</h2></div><span class="section-note">configuration stays outside the binary</span></div><div class="setup-steps"><div><span>1</span><p>Create <code>${esc((D&&D.home)||"~/.mino")}/mcp.d/fs.json</code></p></div><div><span>2</span><pre>{
  "name": "fs",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "${esc((D&&D.home)||"~/.mino")}"]
}</pre></div><div><span>3</span><p>Restart Mino. Discovered tools appear under <a href="#tools/available">Available</a>.</p></div></div></section>`;
  return h;
}

function toolsAvailable(t){
  const catalog = t.catalog || [];
  const groups = [
    ["builtin","Core tools","Part of the single Mino binary","◇"],
    ["extension","Extensions","Separate services discovered over HTTP","↗"],
    ["mcp","MCP tools","External servers attached through MCP","⌘"],
  ];
  const counts = Object.fromEntries(groups.map(([key])=>[key,catalog.filter(x=>x.source===key).length]));
  let h = `<section class="tools-hero"><div><span class="section-kicker">CAPABILITY SYSTEM</span><h2>What Mino can do.</h2><p>Every capability visible to the model this turn, grouped by ownership and runtime boundary.</p></div><div class="tools-total"><strong>${catalog.length}</strong><span>available tools</span><small>${counts.extension} extensions · ${counts.mcp} MCP</small></div></section>
    <div class="capability-summary">${groups.map(([key,label,desc,icon])=>`<a href="#tools/available-${key}" onclick="event.preventDefault();document.getElementById('tools-${key}')?.scrollIntoView({behavior:'smooth'})"><span>${icon}</span><div><strong>${counts[key]}</strong><small>${label}</small></div><b>→</b></a>`).join("")}</div>`;
  for (const [key,label,desc,icon] of groups){
    const items = catalog.filter(x=>x.source===key);
    h += `<section class="capability-group" id="tools-${key}"><div class="overview-section-head"><div><span class="section-kicker">${esc(key.toUpperCase())}</span><h2>${esc(label)}</h2></div><span class="section-note">${esc(desc)} · ${items.length}</span></div>`;
    h += items.length ? `<div class="capability-grid">${items.map(tool=>`<article><span class="capability-icon">${icon}</span><div><code>${esc(tool.name)}</code><p>${esc(tool.description)}</p></div><span class="srcpill ${key}">${esc(key)}</span></article>`).join("")}</div>` : `<div class="surface-empty compact"><span>${icon}</span><strong>No ${esc(label.toLowerCase())}</strong></div>`;
    h += `</section>`;
  }
  return h;
}

function databaseOverview(d){
  const db = d.db || {tables:[],fts:[],all_tables:[],size:0,path:""};
  const tables = db.tables || [], size = db.size || 0;
  const sizeLabel = size > 1048576 ? (size/1048576).toFixed(1)+" MB" : (size/1024).toFixed(1)+" KB";
  return `<section class="database-hero"><div><span class="section-kicker">LOCAL SOURCE OF TRUTH</span><h2>One file. Every durable record.</h2><p>Inspect Mino’s SQLite state at table level without leaving the command center.</p></div><div class="database-file"><span>STATE.DB</span><strong>${sizeLabel}</strong><small>${esc(db.path||"")}</small></div></section>
    <div class="database-metrics"><div><strong>${tables.length}</strong><span>data tables</span></div><div><strong>${(db.fts||[]).length}</strong><span>FTS5 indexes</span></div><div><strong>${(db.all_tables||[]).length}</strong><span>physical tables</span></div><div><strong>WAL</strong><span>journal mode</span></div></div>
    <section class="database-principle"><span>▦</span><div><span class="section-kicker">MEMORY AND DATABASE</span><h3>Friendly model above, exact rows below.</h3><p>Memory organizes facts and episodes by meaning. Database exposes the same records, schemas, indexes, and operational state without abstraction.</p></div><a href="#memory">Open Memory →</a></section>
    <div class="overview-section-head"><div><span class="section-kicker">TABLES</span><h2>Browse persisted state</h2></div><span class="section-note">up to 50 newest rows per table</span></div>
    <div class="database-grid">${tables.map(t=>`<a href="#database/${encodeURIComponent(t.name)}"><span class="table-icon">${t.name==="facts"||t.name==="episodes"?"✦":t.name==="chat_log"?"↔":"▦"}</span><div><code>${esc(t.name)}</code><p>${esc(DB_DESC[t.name]||"Mino runtime state")}</p><small>${(t.columns||[]).length} columns</small></div><strong>${t.count}</strong></a>`).join("")}</div>
    <section class="fts-card"><div><span class="section-kicker">SEARCH INDEX</span><h3>FTS5 keeps recall local and inspectable.</h3><p>${(db.fts||[]).map(x=>`<code>${esc(x)}</code>`).join(" ")||"No FTS indexes detected"}</p></div><a href="#database/query">Query state →</a></section>`;
}

function databaseTableView(t){
  return `<section class="surface-head"><div><span class="section-kicker">SQLITE TABLE</span><h2><code>${esc(t.name)}</code></h2><p>${esc(DB_DESC[t.name]||"Persistent Mino runtime data.")}</p></div><strong>${t.count} rows</strong></section>
    <div class="schema-strip"><span>SCHEMA</span>${(t.columns||[]).map(c=>`<code>${esc(c)}<small>${esc((t.types&&t.types[c]||"").toLowerCase())}</small></code>`).join("")}</div>${dbTable(t)}`;
}

function opsOverview(d){
  const s=d.stats||{}, u=d.usage||{}, turns=d.turns||[];
  const slow=[...turns].filter(x=>x.latency_ms!=null).sort((a,b)=>b.latency_ms-a.latency_ms).slice(0,5);
  return `<section class="ops-hero"><div><span class="section-kicker">RUNTIME OBSERVATORY</span><h2>Operational signal, without the noise.</h2><p>Latency, reliability, spend, retrieval, and release evidence in one place.</p></div><div class="ops-health healthy"><i></i><strong>observable</strong><span>${s.tool_errors||0} failed tool calls traced</span><small>${s.trace_files||0} trace file${s.trace_files===1?"":"s"} online</small></div></section>
    <div class="ops-metrics"><div class="primary"><span>TURNS</span><strong>${(s.turns||0).toLocaleString()}</strong><small>${(s.tool_calls||0).toLocaleString()} tool calls</small></div><div><span>AVERAGE</span><strong>${secs(s.latency_avg)}</strong><small>p95 ${secs(s.latency_p95)}</small></div><div><span>SPEND</span><strong>${money(u.total_cost||0)}</strong><small>${(u.calls||0).toLocaleString()} LLM calls</small></div><div><span>FAILED CALLS</span><strong>${s.tool_errors||0}</strong><small>trace evidence</small></div></div>
    <section class="ops-signal"><div class="overview-section-head"><div><span class="section-kicker">RETRIEVAL</span><h2>Memory gate signal</h2></div><span class="section-note">derived from recall tool activity</span></div>${gateSplit(s)}</section>
    <div class="overview-section-head"><div><span class="section-kicker">PERFORMANCE</span><h2>Slowest recent turns</h2></div><a class="section-link" href="#ops/traces">Open traces →</a></div>
    ${slow.length?`<div class="performance-list">${slow.map(t=>`<div><span class="latency-value">${secs(t.latency_ms)}</span><div><strong>${esc((t.user_message||"").slice(0,90))}</strong><small>${(t.tools||[]).length} tools · ${t.iterations||1} iterations · ${money(t.cost||0)}</small></div></div>`).join("")}</div>`:`<div class="surface-empty"><span>⌁</span><strong>No timed turns yet</strong><p>Latency appears after a traced turn completes.</p></div>`}`;
}

function opsUsage(d){
  const u=d.usage||{calls:0,total_in:0,total_out:0,total_cost:0,by_day:[],by_provider:[]};
  const days=u.by_day||[], max=Math.max(...days.map(x=>x.cost||0),.001);
  return `<section class="surface-head"><div><span class="section-kicker">USAGE LEDGER</span><h2>Tokens and estimated spend</h2><p>Append-only usage records survive dashboard resets and deployments.</p></div><strong>${money(u.total_cost||0)}</strong></section>
    <div class="usage-summary"><div><span>LLM CALLS</span><strong>${(u.calls||0).toLocaleString()}</strong></div><div><span>INPUT TOKENS</span><strong>${(u.total_in||0).toLocaleString()}</strong></div><div><span>OUTPUT TOKENS</span><strong>${(u.total_out||0).toLocaleString()}</strong></div></div>
    <div class="two-column-section usage-columns"><section><div class="overview-section-head"><div><span class="section-kicker">DAILY</span><h2>Spend over time</h2></div>${reveal("usage.jsonl","open ledger")}</div>${days.length?`<div class="usage-bars">${days.map(x=>`<div><time>${esc(x.date)}</time><span><i style="width:${Math.max(3,(x.cost||0)/max*100)}%"></i></span><strong>${money(x.cost||0)}</strong><small>${x.calls} calls</small></div>`).join("")}</div>`:`<div class="surface-empty compact"><span>$</span><strong>No usage yet</strong></div>`}</section>
      <section><div class="overview-section-head"><div><span class="section-kicker">PROVIDERS</span><h2>Call distribution</h2></div></div>${(u.by_provider||[]).length?`<div class="provider-usage">${u.by_provider.map(x=>`<div><span class="provider-avatar">${esc((x.provider||"?")[0].toUpperCase())}</span><div><strong>${esc(x.provider)}</strong><small>${x.calls} calls · ${(x.in+x.out).toLocaleString()} tokens</small></div><b>${money(x.cost||0)}</b></div>`).join("")}</div>`:`<div class="surface-empty compact"><span>◇</span><strong>No provider usage</strong></div>`}</section></div>`;
}

function opsTraces(d){
  const events=d.trace_tail||[], turns=d.turns||[];
  return `<section class="surface-head"><div><span class="section-kicker">TRACE STREAM</span><h2>What happened, in order</h2><p>Structured JSONL events from every turn, model pass, and tool invocation.</p></div><strong>${esc(d.trace_file||"no trace")}</strong></section>
    <div class="trace-layout"><section><div class="overview-section-head"><div><span class="section-kicker">EVENTS</span><h2>Latest trace lines</h2></div>${reveal("traces","open folder")}</div>${events.length?`<div class="trace-stream">${events.map(e=>`<div><span class="trace-mark ${esc(e.type)}"></span><code>${esc(e.type)}</code><p>${esc(String(e.detail||"").slice(0,120))}</p><time>${esc((e.ts||"").replace("T"," ").slice(11,19))}</time></div>`).join("")}</div>`:`<div class="surface-empty"><span>⌁</span><strong>No trace lines today</strong></div>`}</section>
      <aside><span class="section-kicker">TRACE SUMMARY</span><div class="trace-stat"><strong>${turns.length}</strong><span>recent turns</span></div><div class="trace-stat"><strong>${turns.reduce((n,t)=>n+(t.llm_calls||[]).length,0)}</strong><span>model passes</span></div><div class="trace-stat"><strong>${turns.reduce((n,t)=>n+(t.tools||[]).length,0)}</strong><span>tool invocations</span></div><p>Trace files are plain JSONL. They remain inspectable even if the dashboard is offline.</p></aside></div>`;
}

function opsRelease(d){
  const report=d.eval_report;
  return `<section class="surface-head"><div><span class="section-kicker">RELEASE GATE</span><h2>Evidence before deployment</h2><p>Deterministic checks and judge evaluations provide one explicit ship or hold decision.</p></div><strong class="${report?"status-pass":"status-idle"}">${report?"recorded":"not run"}</strong></section>
    <div class="release-gate"><div class="release-step ${report?"done":""}"><span>1</span><div><strong>Deterministic suite</strong><p>Behavioral invariants and regression tests.</p></div><b>${report?esc(report.deterministic):"awaiting"}</b></div><div class="release-line"></div><div class="release-step ${report?"done":""}"><span>2</span><div><strong>LLM judge</strong><p>Quality threshold for model-facing behavior.</p></div><b>${report?esc(report.judge):"awaiting"}</b></div></div>
    <section class="command-card"><span class="section-kicker">RUN LOCALLY</span><h3><code>make gate</code></h3><p>The release gate is intentionally manual. A deploy should be a conscious decision backed by a fresh result.</p></section>`;
}

function settingsView(d){
  const cfg=d.settings||{providers:[],config_file:""}, providers=cfg.providers||[];
  setTimeout(async()=>{
    const el=document.getElementById("oauth-providers"); if(!el) return;
    try {
      const r=await (await fetch("/api/oauth/providers")).json(), list=r.providers||[];
      oauthProviders=Object.fromEntries(list.map(p=>[p.name,p]));
      el.innerHTML=list.length?list.map(p=>{ const name=encodeURIComponent(p.name).replace(/'/g,"%27"); return `<article><div><strong>${esc(p.display_name||p.name)}</strong><small>${esc((p.models||[]).join(" · "))}</small></div>${p.logged_in?`<span class="status-chip good">logged in</span>`:`<button class="oauth-btn" onclick="startOAuth(decodeURIComponent('${name}'))">Login with ${esc(p.display_name||p.name)}</button>`}</article>`; }).join(""):`<div class="surface-empty compact"><strong>No OAuth providers available</strong></div>`;
    } catch(e){ el.innerHTML=`<div class="surface-empty compact"><strong>OAuth unavailable</strong><p>${esc(e.message)}</p></div>`; }
  },0);
  return `<section class="settings-hero"><div><span class="section-kicker">RUNTIME CONFIGURATION</span><h2>Simple, visible, restart-bound.</h2><p>Manage provider priority, credentials, and OAuth connections from one local surface.</p></div><div class="settings-runtime"><span class="runtime-kicker"><i></i> ACTIVE RUNTIME</span><strong>${esc(d.provider)} · ${esc(d.model)}</strong><small>${esc(d.home)}</small></div></section>
    <div class="overview-section-head"><div><span class="section-kicker">PROVIDER CHAIN</span><h2>Priority and health</h2></div><button class="oauth-btn" onclick="showAddProvider()">+ Add Provider</button></div>
    <form id="add-provider-form" class="add-provider-form" hidden onsubmit="event.preventDefault();addProvider()"><input id="provider-name" placeholder="Name" required><input id="provider-base-url" type="url" placeholder="Base URL" required><input id="provider-model" placeholder="Model" required><input id="provider-small-model" placeholder="Small model"><input id="provider-api-key" type="password" placeholder="API key (optional)"><input id="provider-priority" type="number" min="1" value="10" placeholder="Priority"><button type="submit">Add</button><span id="provider-form-status" aria-live="polite"></span></form>
    ${providers.length?`<div class="provider-stack">${providers.map((p,i)=>{ const name=encodeURIComponent(p.name).replace(/'/g,"%27"); return `<article><span class="provider-priority">${p.priority}</span><div class="provider-main"><div><strong>${esc(p.name)}</strong><span class="status-chip ${p.key_set?"good":"warn"}">${p.key_set?"key set":"key missing"}</span></div><p>${esc(p.model)}${p.small_model?` · small ${esc(p.small_model)}`:""}</p><small>${esc(p.base_url)}</small></div><button class="provider-remove" title="Remove provider" aria-label="Remove provider" onclick="removeProvider(decodeURIComponent('${name}'))">✕</button>${i<providers.length-1?`<span class="fallback-arrow">↓ fallback</span>`:""}</article>`; }).join("")}</div>`:`<div class="surface-empty"><span>◇</span><strong>No provider snapshot available</strong><p>Add a provider to providers.json.</p></div>`}
    <div class="overview-section-head"><div><span class="section-kicker">OAUTH</span><h2>Connected accounts</h2></div><span id="oauth-status" class="section-note" aria-live="polite">${esc(oauthMessage)}</span></div><div id="oauth-providers" class="oauth-providers"><div class="surface-empty compact"><strong>Loading OAuth providers…</strong></div></div>
    <div class="settings-grid"><section><span class="settings-icon">⌘</span><div><span class="section-kicker">CONFIG FILE</span><strong>providers.json</strong><p>${esc(cfg.config_file||"")}</p></div>${reveal("providers.json","open file")}</section><section><span class="settings-icon">▦</span><div><span class="section-kicker">STATE HOME</span><strong>Mino home</strong><p>${esc(d.home)}</p></div>${reveal("","open folder")}</section><section><span class="settings-icon">✦</span><div><span class="section-kicker">PERSONALITY</span><strong>SOUL.md</strong><p>Editable safely from Memory.</p></div><a href="#memory/soul">Open SOUL →</a></section></div>`;
}

function activeTasksView(d){
  const tasks=d.active_tasks||[];
  return `<section class="tasks-hero"><div><span class="section-kicker">CHECKPOINTS</span><h2>Work that survives a restart.</h2><p>Mino records long-running progress after tool calls, then resumes from the latest checkpoint.</p></div><div class="tasks-count"><strong>${tasks.length}</strong><span>active task${tasks.length===1?"":"s"}</span><small>${tasks.reduce((n,t)=>n+(t.tools_used||[]).length,0)} tools recorded</small></div></section>
    ${tasks.length?`<div class="task-list">${tasks.map((t,i)=>`<article><header><span class="task-index">${String(i+1).padStart(2,"0")}</span><span class="status-chip good"><i></i> ${esc(t.status||"active")}</span></header><h3>${esc(t.goal)}</h3><div class="task-progress"><span style="width:${Math.min(92,20+(t.round||0)*12)}%"></span></div><div class="task-meta"><span>round ${t.round||0}</span><span>${(t.tools_used||[]).length} tools used</span><span>${(t.discoveries||[]).length} discoveries</span></div>${(t.tools_used||[]).length?`<div class="task-tools">${t.tools_used.map(x=>`<code>${esc(x)}</code>`).join("")}</div>`:""}${(t.discoveries||[]).length?`<ul>${t.discoveries.map(x=>`<li>${esc(x)}</li>`).join("")}</ul>`:""}</article>`).join("")}</div>`:`<div class="tasks-empty"><div class="checkpoint-orbit"><span>✓</span></div><strong>No interrupted work</strong><p>Everything is complete. If Mino stops during a tool-heavy task, its checkpoint will appear here automatically.</p><a href="#loop">Inspect recent turns →</a></div>`}
    <section class="checkpoint-flow"><div><span>1</span><strong>Tool runs</strong><small>progress changes</small></div><b>→</b><div><span>2</span><strong>Checkpoint</strong><small>saved to disk</small></div><b>→</b><div><span>3</span><strong>Restart</strong><small>context restored</small></div></section>`;
}

function onboardingView(){
  const field=(label,id,placeholder,type="text",hint="")=>`<label class="onboarding-field"><span>${label}</span><input id="${id}" type="${type}" placeholder="${esc(placeholder)}" onfocus="markEditing()" oninput="markEditing()"><small>${hint}</small></label>`;
  return `<div class="onboarding-shell"><aside><div class="onboarding-mark"><span>✦</span></div><span class="section-kicker">WELCOME TO MINO</span><h2>Your personal AI system starts here.</h2><p>Connect one OpenAI-compatible provider. Mino will create a local home, keep its state in SQLite, and restart into the command center.</p><div class="onboarding-points"><div><span>01</span><p><strong>Private state</strong><small>One local SQLite file</small></p></div><div><span>02</span><p><strong>Provider resilience</strong><small>Priority and fallback ready</small></p></div><div><span>03</span><p><strong>Everywhere access</strong><small>Dashboard and optional Telegram</small></p></div></div></aside>
    <section class="onboarding-form"><div><span class="section-kicker">PROVIDER SETUP</span><h3>Connect the first model</h3><p>Keys are written to the server environment file and never returned by the dashboard API.</p></div><div class="form-grid">${field("Provider name","onb-provider","mimo","text","A short label for this connection")}${field("API key","onb-apikey","sk-...","password","Stored in mino.env")}${field("Base URL","onb-baseurl","https://api.openai.com/v1","url","OpenAI-compatible endpoint")}${field("Main model","onb-model","mimo-v2.5","text","Used for conversations and tools")}${field("Small model","onb-small","mimo-v2.5","text","Optional background work")}${field("Telegram token","onb-tgtoken","123456:ABC-DEF...","password","Optional — connect later if preferred")}</div><button id="onb-save" class="onboarding-submit" onclick="saveOnboarding()">Save configuration <span>→</span></button><div id="onb-msg" class="onboarding-message" aria-live="polite"></div><small class="onboarding-footnote">Mino restarts once after saving. The dashboard reconnects automatically.</small></section></div>`;
}

const VIEWS = {
  // Gateway: ONE unified conversation across every channel (dashboard, telegram,
  // voice, cli) — the same loop + memory answer all of them. Each message is
  // tagged with where it came in, Hermes-style. You type in the dock on the right.
  // Gateway = an INBOX of conversations (like Slack/Intercom): one row per
  // conversation, tagged with its channel(s). Click one to open it in the chat
  // dock (the active thread). No longer a flat stream that duplicates the dock.
  gateway(d){
    const sessions = d.sessions || [];
    const messageCount = sessions.reduce((n,s)=>n+(s.messages||0),0);
    const active = sessions.find(s => s.id === SESSION);
    let h = `<section class="gateway-hero"><div><div class="eyebrow">OMNI-CHANNEL INBOX</div><h2 class="gateway-title">Conversations</h2>
      <p class="gateway-lede">Every channel reaches the same Mino brain. Choose a thread to continue it in the dock.</p></div>
      <div class="gateway-summary"><strong>${sessions.length}</strong><span>threads</span><small>${messageCount} messages · live session state</small></div></section>
      <section class="gateway-layout"><div class="gateway-inbox"><div class="gateway-list-head"><div><span class="section-kicker">INBOX</span><h3>All conversations</h3></div><span>${sessions.length} thread${sessions.length===1?"":"s"}</span></div>`;
    if (!sessions.length) h += `<div class="gateway-empty"><span class="empty-orb">↔</span><strong>No conversations yet</strong><p>Say something in the chat dock and your first thread will appear here.</p><a href="#overview">Return to overview →</a></div>`;
    h += sessions.map(s => {
      const tags = (s.sources||[]).map(src => `<span class="gwtag ${esc(src)}">${esc(src)}</span>`).join("");
      const on = s.id === SESSION;
      const preview = stripTools(s.last||"").replace(/\s+/g," ").slice(0,180);
      const time = (s.last_at||"").slice(0,16).replace("T"," ");
      return `<div class="conversation-row ${on?"active":""}" role="button" tabindex="0" onclick="openConversation('${esc(s.id)}')" onkeydown="if(event.key==='Enter'||event.key===' '){event.preventDefault();openConversation('${esc(s.id)}')}">
        <span class="conversation-icon">${s.sources&&s.sources.includes("telegram")?"✈":"◉"}</span><div class="conversation-main"><div class="conversation-title"><strong>${esc(s.title||s.id)}</strong><span>${tags}</span></div>
          <p>${esc(preview||"No messages yet")}</p><div class="conversation-meta"><span>${s.messages} message${s.messages===1?"":"s"}</span><span>·</span><span>${esc(time)}</span></div></div><span class="conversation-open">${on?"OPEN":"→"}</span></div>`;
    }).join("");
    h += `</div><aside class="gateway-side"><div class="gateway-current"><span class="section-kicker">OPEN THREAD</span><strong>${active?esc(active.title||active.id):"No thread selected"}</strong>
      <p>${active?"This is the conversation currently loaded in the chat dock.":"Choose a conversation to load it into the dock."}</p><a href="#overview">Watch the live system →</a></div>
      <div class="gateway-principle"><span class="principle-icon">✦</span><strong>One brain, every channel</strong><p>Dashboard, Telegram, voice, and terminal messages share Mino’s runtime and memory.</p><div class="channel-list"><span>dashboard</span><span>telegram</span><span>voice</span><span>terminal</span></div></div></aside></section>`;
    return h;
  },
  overview(d){
    const s = d.stats;
    const u = d.usage || {total_cost:0};
    const turns = d.turns || [], facts = (d.facts||[]).length, episodes = (d.episodes||[]).length;
    const sessions = (d.sessions||[]).length;
    const latest = turns[0];
    const memoryTotal = facts + episodes;
    const latestLabel = latest ? esc((latest.ts||"").replace("T"," ").slice(0,16)) : "Awaiting first turn";
    const metric = (value,label,detail,cls="") => `<div class="overview-metric ${cls}"><span>${label}</span><strong>${value}</strong><small>${detail}</small></div>`;
    return `<section class="overview-hero">
      <div><div class="eyebrow">PERSONAL AI SYSTEM</div><h2 class="overview-title">Mino is awake.</h2>
        <p class="overview-lede">Your conversations, memory, tools, and learning loops share one live workspace.</p>
        <div class="overview-links"><a href="#gateway">Open gateway <span>→</span></a><a href="#memory">Inspect memory <span>→</span></a></div>
      </div><div class="overview-runtime"><span class="runtime-kicker"><i></i> RUNTIME STATUS</span><strong>Operational</strong>
        <span class="provider-clickable" onclick="toggleProviderMenu(event)" title="Click to switch provider">${esc(d.active_provider||d.provider)} · ${esc(d.model)} <i class="dropdown-arrow">▾</i></span><small>${sessions} active conversation${sessions===1?"":"s"} · ${esc(d.home)}</small></div>
    </section>
    <section class="overview-metrics" aria-label="Mino runtime summary">
      ${metric(s.turns,"Total turns",`${s.tool_calls} tool calls`,"primary")}
      ${metric(secs(s.latency_avg),"Average turn",`p95 ${secs(s.latency_p95)}`)}
      ${metric(money(u.total_cost),"Estimated spend",`${(u.total_in||0).toLocaleString()} input tokens`,"money")}
      ${metric(memoryTotal,"Memory records",`${facts} facts · ${episodes} episodes`)}
      ${metric(d.chat_pending||0,"Queued work",d.chat_pending?"pending consolidation":"queue is clear")}
    </section>
    <section class="overview-signal"><div class="overview-section-head"><div><span class="section-kicker">SIGNAL</span><h2>Retrieval gate</h2></div><span class="section-note">records whether recall entered each turn</span></div>
      <div class="signal-body">${gateSplit(s)}</div>
    </section>
    <section class="overview-universe"><div class="overview-section-head"><div><span class="section-kicker">LIVE SYSTEM</span><h2>Runtime blueprint</h2></div><span class="section-note">open any subsystem to inspect it <span class="arch-status"></span></span></div>${archSVG(d)}</section>
    <section class="overview-latest"><div class="overview-section-head"><div><span class="section-kicker">RECENT ACTIVITY</span><h2>Latest turn</h2></div><span class="section-note">${latestLabel}</span></div>
      ${latest?turnCard(latest):'<div class="card empty">No turns yet — send a message from the chat dock to wake Mino.</div>'}
    </section>`;
  },
  loop(d){
    const turns=d.turns||[], calls=turns.reduce((n,t)=>n+(t.llm_calls||[]).length,0), tools=turns.reduce((n,t)=>n+(t.tools||[]).length,0);
    const avg=turns.length?turns.reduce((n,t)=>n+(t.iterations||1),0)/turns.length:0;
    return `<section class="loop-hero"><div><span class="section-kicker">AGENT EXECUTION</span><h2>Every turn, step by step.</h2><p>Follow input through context, model reasoning, tool action, and the final response.</p></div><div class="loop-summary"><span class="runtime-kicker"><i></i> TRACE LIVE</span><strong>${turns.length} recent turns</strong><small>${calls} model passes · ${tools} tool calls</small></div></section>
      <div class="loop-metrics"><div><strong>${turns.length}</strong><span>traced turns</span></div><div><strong>${calls}</strong><span>model passes</span></div><div><strong>${tools}</strong><span>tool calls</span></div><div><strong>${avg.toFixed(1)}</strong><span>avg iterations</span></div></div>
      <div class="overview-section-head"><div><span class="section-kicker">TIMELINE</span><h2>Recent executions</h2></div><span class="section-note">newest first · expand responses and tool arguments</span></div>
      ${turns.length?`<div class="execution-timeline">${turns.map(executionTurn).join("")}</div>`:`<div class="surface-empty"><span>◌</span><strong>No executions yet</strong><p>Send a message in the chat dock to create the first traced turn.</p></div>`}`;
  },
  memory(d, sub){
    sub = sub || "overview";
    const tabs = [["overview","Overview"],["semantic","Semantic",(d.facts||[]).length],
      ["episodic","Episodic",(d.episodes||[]).length],["skills","Skills",(d.skills||[]).length],
      ["soul","SOUL"],["consolidation","Consolidation",d.chat_pending]];
    let h = subtabBar("memory", tabs, sub);
    if (sub==="semantic") return h + memSemantic(d);
    if (sub==="episodic") return h + memEpisodic(d);
    if (sub==="skills") return h + memSkills(d);
    if (sub==="soul") return h + memSoul(d);
    if (sub==="consolidation") return h + memConsolidation(d);
    return h + memOverview(d);
  },
  settings(d){
    return settingsView(d);
  },
  tools(d, sub){
    const raw = d.tools || {}, mcp = raw.mcp || {};
    const t = {...raw, catalog:raw.catalog||[], mcp:{...mcp, servers:mcp.servers||[]}};
    sub = sub || "available";
    const tabs = [["available","Available",t.catalog.length],["results","Results"],
      ["mcp","MCP",t.mcp.servers.length||null]];
    let h = subtabBar("tools", tabs, sub);
    if (sub === "results") return h + toolsResults(d);
    if (sub === "mcp") return h + toolsMCP(t);
    return h + toolsAvailable(t);
  },
  database(d, sub){
    const db = d.db || {tables:[], all_tables:[], fts:[], size:0, path:""};
    const tables = db.tables || [];
    sub = sub || "overview";
    const tabs = [["overview","Overview"],
      ...tables.map(t => [t.name, t.name, t.count]),
      ["query","SQL console"]];
    let h = subtabBar("database", tabs, sub);
    if (sub === "query") return h + dbQueryView();
    if (sub !== "overview"){
      const t = tables.find(x => x.name === sub);
      if (!t) return h + `<div class="surface-empty"><span>▦</span><strong>No such table</strong><p>The database schema may have changed since this link was created.</p></div>`;
      return h + databaseTableView(t);
    }
    return h + databaseOverview(d);
  },
  ops(d, sub){
    sub=sub||"overview";
    const tabs=[["overview","Overview"],["usage","Usage",(d.usage&&d.usage.calls)||0],["traces","Traces",(d.trace_tail||[]).length],["release","Release"]];
    const h=subtabBar("ops",tabs,sub);
    if(sub==="usage") return h+opsUsage(d);
    if(sub==="traces") return h+opsTraces(d);
    if(sub==="release") return h+opsRelease(d);
    return h+opsOverview(d);
  },

  activetasks(d){
    return activeTasksView(d);
  },

  onboarding(){
    return onboardingView();
  },

  files(d, sub){
    const root = "/tmp/mino/results";
    sub = sub ? decodeURIComponent(sub) : root;
    const h = `<section class="files-hero"><div><span class="section-kicker">VPS FILE BROWSER</span><h2>${esc(sub)}</h2><p>Every file Mino creates lives here — tool outputs, uploads, artifacts.</p></div></section>
      <div id="files-tree" class="files-tree">${spinner()}</div>`;
    setTimeout(async () => {
      const el = document.getElementById("files-tree");
      if (!el) return;
      try {
        const url = "/api/files" + (sub !== root ? "?path=" + encodeURIComponent(sub) : "");
        const tree = await (await fetch(url)).json();
        if (!Array.isArray(tree)) { el.innerHTML = `<span class="files-error">${esc(tree.error||"bad response")}</span>`; return; }
        el.innerHTML = renderFileTree(tree, sub);
      } catch(e) { el.innerHTML = `<span class="files-error">Could not load: ${esc(e.message)}</span>`; }
    }, 50);
    return h;
  },
};

// ---- Live Universe animation: light up the map as a turn flows through,
// driven by the trace stream so ANY gateway (browser, phone, CLI) triggers it.
const STAGE = {
  turn_start:    {nodes:["gateway","wm"],            edges:["e-gw-wm"],                 label:"message in"},
  gate:          {nodes:["gate"],                    edges:["e-gate-wm"],               label:"retrieval gate"},
  llm:           {nodes:["llm"],                     edges:["e-wm-loop"],               label:"agent reasons"},
  tool:          {nodes:["tools"],                   edges:[],                          label:"tool runs"},
  turn_end:      {nodes:["reply","trace"],           edges:["e-reply-trace","e-reply-save"], label:"reply"},
  consolidation: {nodes:["consolidation","semantic"],edges:["e-consol-sem"],            label:"consolidating memory"},
};
let evCursor = null, evQueue = [], playing = false, animating = false;

function hot(sel, cls, ms){
  document.querySelectorAll(sel).forEach(el => {   // every diagram copy lights up
    el.classList.add(cls);
    setTimeout(()=>el.classList.remove(cls), ms);
  });
}
function animateStage(ev){
  const spec = STAGE[ev.type];
  if (!spec || !document.querySelector(".arch")) return;
  document.querySelectorAll(".arch-status").forEach(st => st.innerHTML = `<span class="live-dot"></span>${spec.label}`);
  spec.nodes.forEach(n => hot(`[data-node="${n}"]`, "hot", 1000));
  spec.edges.forEach(e => hot(`[data-edge="${e}"]`, "live", 1000));
  if (ev.type==="gate" && ev.decision==="retrieve"){
    ["procedural","semantic","episodic"].forEach(n => hot(`[data-node="${n}"]`,"hot",1000));
    ["e-gate-proc","e-gate-sem","e-gate-epi"].forEach(e => hot(`[data-edge="${e}"]`,"live",1000));
  }
}
function playNext(){
  if (!evQueue.length){ playing=false; animating=false;
    document.querySelectorAll(".arch-status").forEach(st => st.innerHTML=""); return; }
  playing = true; animating = true;
  animateStage(evQueue.shift());
  setTimeout(playNext, 620);   // stagger so stages light up in sequence
}
async function pollEvents(){
  try{
    const r = await (await fetch("/api/events" + (evCursor==null?"":"?cursor="+evCursor))).json();
    if (evCursor != null && r.events.length){
      evQueue.push(...r.events);
      if (!playing) playNext();
    }
    evCursor = r.cursor;
  } catch(e){ /* server busy */ }
}

let activeView = null, activeSub = null;
const TITLES = {chat:"Chat & watch", ops:"LLM Ops",
                database:"Database — everything Mino stores (state.db)", activetasks:"Active Tasks — surviving restarts",
                files:"Files — VPS artifacts and outputs",
                onboarding:"Welcome — set up your Mino"};
function render(){
  if (!D) return;
  // onboarding gate: redirect if no API key configured
  if (D.needs_onboarding && !location.hash.startsWith("#onboarding")) {
    location.hash = "#onboarding"; return;
  }
  if (!D.needs_onboarding && location.hash.startsWith("#onboarding")) {
    location.hash = "#overview"; return;
  }
  const [v, subRaw] = (location.hash||"#overview").slice(1).split("/");
  const sub = subRaw || null;
  const view = VIEWS[v] ? v : "overview";
  document.body.classList.toggle("onboarding-mode", view === "onboarding");
  const subChanged = sub !== activeSub || view !== activeView;
  document.querySelectorAll("nav a").forEach(a=>a.classList.toggle("on", a.dataset.v===view));
  document.getElementById("title").textContent = TITLES[view] || view[0].toUpperCase()+view.slice(1);
  if (view === "overview"){
    // don't rebuild mid-animation or the glowing SVG gets wiped
    if (activeView !== "overview" || !animating){ document.getElementById("view").innerHTML = VIEWS.overview(D); }
  } else if ((view === "memory" || view === "settings" || view === "database" || view === "onboarding") && editing && !subChanged){
    // don't wipe an in-progress edit on the 5s refresh — but DO switch sub-tabs
  } else {
    editing = false;
    document.getElementById("view").innerHTML = VIEWS[view](D, sub);
  }
  activeView = view; activeSub = sub;
  document.getElementById("model").textContent = `${D.provider} · ${D.model}`;
  document.getElementById("n-gw").textContent = (D.chat_log||[]).length;
  document.getElementById("n-loop").textContent = D.stats.turns;
  document.getElementById("n-mem").textContent = (D.facts||[]).length + (D.episodes||[]).length;
  document.getElementById("n-tools").textContent = (D.tools&&D.tools.catalog||[]).length;
  document.getElementById("n-db").textContent = (D.db && D.db.all_tables.length) || "";
  document.getElementById("n-ops").textContent = "";
}
let lastFetch = Date.now();
function tickLive(){
  if (!D) return;
  const ago = Math.round((Date.now()-lastFetch)/1000);
  document.getElementById("sub").innerHTML =
    `<span class="live"><span class="dot"></span>live</span> · updated ${ago}s ago · ${esc(D.home)}`;
}
async function refresh(){
  try {
    D = await (await fetch("/api/data")).json(); lastFetch = Date.now();
    render(); tickLive();
    syncLiveView();   // live-update an opened conversation (e.g. new phone messages)
  } catch(e){ /* server restarting — keep showing last data */ }
}
// --- resizable columns: drag the thin handle between nav|main and main|dock.
// Width lives in a CSS var + localStorage, so it survives refreshes.
function wireResizer(id, cssVar, key, fromRight, min, max){
  const el = document.getElementById(id);
  if (!el) return;
  el.onmousedown = e => {
    e.preventDefault();
    document.body.classList.add("resizing");
    const move = ev => {
      let w = fromRight ? (window.innerWidth - ev.clientX) : ev.clientX;
      w = Math.max(min, Math.min(max, w));
      document.documentElement.style.setProperty(cssVar, w + "px");
      localStorage.setItem(key, w);
    };
    const up = () => { document.body.classList.remove("resizing");
      document.removeEventListener("mousemove", move); document.removeEventListener("mouseup", up); };
    document.addEventListener("mousemove", move);
    document.addEventListener("mouseup", up);
  };
}
function wireChrome(){
  // restore saved widths
  const nw = localStorage.getItem("navW"); if (nw) document.documentElement.style.setProperty("--nav-w", nw+"px");
  const dw = localStorage.getItem("dockW"); if (dw) document.documentElement.style.setProperty("--dock-w", dw+"px");
  wireResizer("nav-resizer", "--nav-w", "navW", false, 150, 380);
  wireResizer("dock-resizer", "--dock-w", "dockW", true, 260, 680);
  // hide / show the sidebar
  const setNav = v => { document.body.classList.toggle("nav-hidden", v); localStorage.setItem("navHidden", v?"1":"0"); };
  const nt = document.getElementById("nav-toggle"), nr = document.getElementById("nav-reopen");
  if (nt) nt.onclick = () => setNav(true);
  if (nr) nr.onclick = () => setNav(false);
  setNav(localStorage.getItem("navHidden") === "1");
}

// --- voice on the dashboard: record in the browser, transcribe on the server
// with the SAME local Whisper `make voice` uses. Text lands in the input for
// you to review, then Send — nothing leaves the machine.
// Voice capture records WAV (uncompressed PCM) via the Web Audio API — NOT
// MediaRecorder's WebM/Opus, which faster-whisper/PyAV often can't decode
// ("transcription failed [Errno …]"). WAV is trivially decodable server-side.
let micCtx = null, micStream = null, micNode = null, micBuf = [], micOn = false;
const micHint = (msg) => { const i = document.getElementById("dmsg");
  if (i){ i.placeholder = msg; setTimeout(()=>{ i.placeholder = "Message Mino…"; }, 8000); } };

async function toggleMic(){
  const btn = document.getElementById("mic");
  if (micOn){ await stopMic(); return; }
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia){
    micHint("voice needs a normal browser tab at localhost:7777 — not the IDE preview pane");
    return;
  }
  try {
    micStream = await navigator.mediaDevices.getUserMedia({audio:true});
    micCtx = new (window.AudioContext || window.webkitAudioContext)();
    const source = micCtx.createMediaStreamSource(micStream);
    micNode = micCtx.createScriptProcessor(4096, 1, 1);
    micBuf = [];
    micNode.onaudioprocess = e => micBuf.push(new Float32Array(e.inputBuffer.getChannelData(0)));
    source.connect(micNode); micNode.connect(micCtx.destination);
    micOn = true; btn.classList.add("rec");
  } catch(e){
    console.warn("mic error:", e);
    micHint(e && e.name === "NotAllowedError"
      ? "mic blocked — click the lock icon in the address bar → allow Microphone → reload (macOS: also System Settings ▸ Privacy ▸ Microphone ▸ your browser)"
      : "mic unavailable: " + (e && e.message || e));
  }
}

async function stopMic(){
  const btn = document.getElementById("mic"), input = document.getElementById("dmsg");
  micOn = false; btn.classList.remove("rec");
  try { micNode.disconnect(); } catch(e){}
  micStream.getTracks().forEach(t => t.stop());
  const rate = micCtx.sampleRate;
  micCtx.close();
  const wav = encodeWAV(micBuf, rate);
  const hold = input.placeholder; input.placeholder = "transcribing…";
  let r; try { r = await (await fetch("/api/voice", {method:"POST", body:wav})).json(); }
  catch(e){ r = {error:String(e)}; }
  input.placeholder = hold;
  if (r.error){ input.value = ""; micHint("voice: " + r.error); return; }
  if (r.text){ input.value = r.text; input.focus(); }
}

// float32 chunks → 16-bit PCM mono WAV blob
function encodeWAV(chunks, rate){
  let n = 0; chunks.forEach(c => n += c.length);
  const pcm = new Float32Array(n); let off = 0; chunks.forEach(c => { pcm.set(c, off); off += c.length; });
  const buf = new ArrayBuffer(44 + pcm.length * 2), view = new DataView(buf);
  const str = (o, s) => { for (let i=0;i<s.length;i++) view.setUint8(o+i, s.charCodeAt(i)); };
  str(0,"RIFF"); view.setUint32(4, 36 + pcm.length*2, true); str(8,"WAVE"); str(12,"fmt ");
  view.setUint32(16,16,true); view.setUint16(20,1,true); view.setUint16(22,1,true);
  view.setUint32(24,rate,true); view.setUint32(28,rate*2,true); view.setUint16(32,2,true); view.setUint16(34,16,true);
  str(36,"data"); view.setUint32(40, pcm.length*2, true);
  let o = 44; for (let i=0;i<pcm.length;i++){ const s = Math.max(-1, Math.min(1, pcm[i])); view.setInt16(o, s<0 ? s*0x8000 : s*0x7FFF, true); o += 2; }
  return new Blob([view], {type:"audio/wav"});
}
function wireMic(){ const b = document.getElementById("mic"); if (b) b.onclick = toggleMic; }

function spinner(){ return `<div class="files-loading"><span class="spinner"></span> Loading...</div>`; }
function renderFileTree(tree, parent){
  if (!tree.length) return `<span class="files-empty">No files in this directory.</span>`;
  const item = (n, depth) => {
    const cls = n.is_dir ? "file-node dir" : "file-node";
    const icon = n.is_dir ? "&#128193;" : "&#128196;";
    const size = n.is_dir ? "" : ` <span class="fsize">${formatSize(n.size)}</span>`;
    const time = n.mod_time ? ` <span class="ftime">${n.mod_time}</span>` : "";
    const href = n.is_dir ? `#files/${encodeURIComponent(n.path)}` : `/api/files?path=${encodeURIComponent(n.path)}`;
    const target = n.is_dir ? "" : ' target="_blank"';
    const onclick = n.is_dir ? "" : "";
    return `<a class="${cls}" style="padding-left:${depth*20+8}px" href="${esc(href)}"${target}>${icon} ${esc(n.name)}${size}${time}</a>`;
  };
  return tree.map(n => item(n, 0)).join("");
}
function formatSize(b){ if (!b) return ""; if (b < 1024) return b + " B"; if (b < 1048576) return (b/1024).toFixed(1) + " KB"; return (b/1048576).toFixed(1) + " MB"; }

window.addEventListener("hashchange", render);
let orbitNarrow = window.innerWidth < 720;
window.addEventListener("resize", () => {
  const narrow = window.innerWidth < 720;
  if (narrow === orbitNarrow) return;
  orbitNarrow = narrow;
  if (D && activeView === "overview") document.getElementById("view").innerHTML = VIEWS.overview(D);
});
window.__hold = (v)=>{ animating = v; };   // test hook: freeze the diagram
wireDock(); wireChrome(); wireMic();
refresh(); setInterval(refresh, 5000); setInterval(tickLive, 1000);
pollEvents(); setInterval(pollEvents, 450);   // live harness animation
