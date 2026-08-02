const esc = s => (s??"").toString().replace(/[&<>]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;"}[c]));
const jsArg = s => JSON.stringify(String(s)).replace(/"/g, "&quot;");

// --- provider switcher ---
async function switchProvider(name, model="", reasoning="default") {
  const r = await postJSON("/api/switch", {provider: name, model, reasoning});
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
    const options=d.options||(d.providers||[]).map(name=>({name,models:[""] ,reasoning_levels:["default"]}));
    options.forEach(p => (p.models||[p.model]).forEach(model => {
      const row=document.createElement("div"), button=document.createElement("button");
      const active=p.name===d.active&&model===d.active_model;
      row.className="provider-option-row";
      button.className="provider-option"+(active?" active":"");
      button.textContent=p.name+(model?" · "+model:"")+(active?" ✓":"");
      const levels=p.reasoning_levels||["default"], effort=document.createElement("select");
      effort.className="provider-reasoning"; effort.title="Reasoning effort";
      levels.forEach(level=>{ const opt=document.createElement("option"); opt.value=level; opt.textContent=level; opt.selected=active&&level===(d.reasoning||"default"); effort.appendChild(opt); });
      effort.onclick=event=>event.stopPropagation();
      button.onclick=()=>switchProvider(p.name,model,effort.value);
      row.append(button); if(levels.length>1) row.append(effort); pop.appendChild(row);
    }));
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
  oauthMessage="Starting "+(provider.display_name||name)+" login"; if(status) status.textContent=oauthMessage;
  try {
    if(provider.auth_type==="device_code"){
      const r=await postJSON("/api/oauth/device/"+encodeURIComponent(name),{});
      oauthMessage="Enter code "+r.user_code+" at "+r.verification_url; if(status) status.textContent=oauthMessage;
      if(r.verification_url) window.open(r.verification_url, "_blank");
      for(let i=0;i<180;i++){
        await new Promise(resolve=>setTimeout(resolve,(r.interval||5)*1000));
        const poll=await (await fetch("/api/oauth/device/"+encodeURIComponent(name)+"?device_code="+encodeURIComponent(r.device_code))).json();
        if(poll.ok){ oauthMessage=(provider.display_name||name)+" login complete."; await refresh(); return; }
        if(!poll.pending) throw new Error(poll.error||"login failed");
      }
      oauthMessage="Login pending. Try again.";
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
      oauthMessage="Login pending. Try again.";
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
  act.innerHTML = `<a class="reveal" onclick="saveFact(${jsArg(id)})">save</a> · <a class="reveal" onclick="editing=false;refresh()">cancel</a>`;
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
    tavily_key: document.getElementById("onb-tavily").value.trim(),
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

// Keep the activity row compact even when an older API payload uses the full
// result as `summary`; the complete output stays behind a closed disclosure.
const toolSummary = x => {
  let s = String(x.summary || x.output || "").replace(/\[action_receipt[\s\S]*$/, "").replace(/\s+/g, " ").trim();
  const end = s.indexOf(". ");
  if (end >= 0) s = s.slice(0, end + 1);
  return s.length > 120 ? s.slice(0, 117).trim() + "..." : s;
};
// A tool call renders as a status row (dot + one-line summary); the raw output
// hides behind a disclosure so an ugly osascript error never floods the page.
const toolRow = x => `<div class="tool ${x.status}">
  <div class="tool-head"><span class="dot ${x.status}"></span><code>${esc(x.tool)}</code>
    <span style="color:var(--ink2)">${esc(toolSummary(x))}</span></div>
  <details><summary>args &amp; raw output</summary>
    <pre>${esc(x.tool)}(${esc(JSON.stringify(x.args,null,1))})\n\n${esc(x.output)}</pre>
  </details>
</div>`;

const turnCard = t => `<div class="card">
  <div class="u">${esc(t.user_message)}</div>
  <div class="meta" style="margin-top:4px">${gateBadge(t.gate)}</div>
  ${(t.tools||[]).map(toolRow).join("")}
  <div class="r">${renderMarkdown(stripTools(t.reply))}</div>
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
      ${toolSteps}<div class="execution-stage"><span class="stage-node reply-node">✓</span><div class="stage-copy response-copy"><span class="stage-label">RESPOND</span><details ${index===0?"open":""}><summary>View final response</summary><div class="r">${renderMarkdown(stripTools(t.reply||""))}</div></details></div></div></div>
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
  <div class="r" style="margin-top:8px">${renderCardBody(stripTools(t.reply))}</div>
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
    pending.stream = "";
  } else if (ev.kind === "completion"){
    // complete_task protocol: streamed text is provisional, status+reply are final
    pending.pending = false;
    pending.reply = (ev.reply || "");
    pending.status = ev.status;
    pending.stream = "";
  } else if (ev.kind === "done"){
    pending.pending = false;
    if (ev.error) pending.reply = "Error: " + ev.error;
    else if (!pending.reply) Object.assign(pending, ev);
    pending.stream = "";
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
  if (i) {
    i.onkeydown = e => { if (e.key==="Enter") sendChat(i); };
    i.onfocus = () => setAskOpen(true);
  }
  document.getElementById("dock-close")?.addEventListener("click", () => setAskOpen(false));
  document.getElementById("ask-expand")?.addEventListener("click", () => setAskOpen(true));
  document.getElementById("ask-top")?.addEventListener("click", () => setAskOpen(true));
  document.getElementById("ask-mobile")?.addEventListener("click", () => setAskOpen(true));
  syncChatLogs();
}

function setAskOpen(open){
  document.body.classList.toggle("ask-open", open);
  const expand=document.getElementById("ask-expand");
  if(expand) expand.setAttribute("aria-expanded", open?"true":"false");
  if(open) setTimeout(()=>document.getElementById("dmsg")?.focus(), 20);
}

function askAboutResponsibility(title){
  setAskOpen(true);
  const input=document.getElementById("dmsg");
  if(input){ input.value=`Help me resolve “${title}”`; input.setSelectionRange(input.value.length,input.value.length); }
}

function wireOperatorShell(){
  const toggle=document.getElementById("health-toggle"), panel=document.getElementById("health-panel");
  const setHealthOpen=open=>{
    if(!toggle||!panel) return;
    panel.hidden=!open;
    toggle.setAttribute("aria-expanded",open?"true":"false");
  };
  toggle?.addEventListener("click",event=>{ event.stopPropagation(); setHealthOpen(panel.hidden); });
  panel?.addEventListener("click",event=>event.stopPropagation());
  document.addEventListener("click",()=>setHealthOpen(false));
  document.addEventListener("keydown",event=>{
    if(event.key!=="Escape") return;
    setHealthOpen(false);
    if(document.body.classList.contains("ask-open")) setAskOpen(false);
  });
}

// --- Mino Runtime Spine: the whole process, driven only by real dashboard data.
function archSVG(d){
  const s=d.stats||{}, latest=(d.turns||[])[0]||{}, active=(d.active_tasks||[])[0]||null;
  const catalog=((d.tools||{}).catalog||[]), llms=latest.llm_calls||[], lastLLM=llms[llms.length-1]||{};
  const count=source=>catalog.filter(t=>t.source===source).length;
  const builtinCount=count("builtin"), mcpCount=count("mcp"), extensionCount=count("extension");
  const toolCount=catalog.length, sessions=(d.sessions||[]).length, tables=((d.db||{}).all_tables||[]).length;
  const records=(d.facts||[]).length+(d.episodes||[]).length+(d.skills||[]).length;
  const providerRaw=String(d.active_provider||d.provider||"provider"), modelRaw=String(d.model||"model");
  const iteration=Number(active&&active.round || latest.iterations || 0);
  const selected=lastLLM.selected_tools==null?"—":Number(lastLLM.selected_tools);
  const completion=active?"RUNNING":String(latest.status||"idle").toUpperCase();
  const fmt=n=>Number(n||0).toLocaleString(), fmtBytes=n=>n?`${(Number(n)/1048576).toFixed(1)} MB`:"0 MB";
  const attr=value=>esc(value).replace(/"/g,"&quot;");
  const short=(value,max,fallback)=>{const source=String(value||"").replace(/\s+/g," ").trim()||fallback;return esc(source.length>max?source.slice(0,max-1).trim()+"…":source)};
  const attrs=(view,nid,title,sub)=>`data-node="${nid}" tabindex="0" role="link" aria-label="${attr(`${title}: ${sub}`)}" onclick="location.hash='${view}'" onkeydown="if(event.key==='Enter'||event.key===' '){event.preventDefault();location.hash='${view}'}"`;
  const defs=`<defs><linearGradient id="spine-stage" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#07101e"/><stop offset=".55" stop-color="#0a1730"/><stop offset="1" stop-color="#0b1023"/></linearGradient><linearGradient id="spine-loop" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#16295a"/><stop offset="1" stop-color="#10233e"/></linearGradient><pattern id="spine-grid" width="24" height="24" patternUnits="userSpaceOnUse"><path d="M24 0H0V24" fill="none" stroke="#7890bc" stroke-width=".35" opacity=".12"/></pattern><filter id="core-glow" x="-100%" y="-100%" width="300%" height="300%"><feGaussianBlur stdDeviation="3" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter><marker id="core-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto"><path d="M0 0L10 5L0 10Z" class="flow-head"/></marker></defs>`;
  const wire=(path,id,cls="")=>`<path class="flow-wire ${cls}" data-edge="${id}" d="${path}" marker-end="url(#core-arrow)"/>`;
  const node=(x,y,w,h,kicker,title,lines,view,nid,mark,cls="")=>`<g class="node core-node ${cls}" ${attrs(view,nid,title,lines.join(" · "))}><rect class="target spine-card" x="${x}" y="${y}" width="${w}" height="${h}" rx="12"/><circle class="card-mark" cx="${x+25}" cy="${y+27}" r="12"/><text class="card-symbol" x="${x+25}" y="${y+31}" text-anchor="middle">${mark}</text><text class="card-kicker" x="${x+45}" y="${y+20}">${kicker}</text><text class="card-title" x="${x+45}" y="${y+40}">${title}</text>${lines.map((line,i)=>`<text class="card-sub" x="${x+16}" y="${y+h-18+(i-lines.length+1)*14}">${line}</text>`).join("")}</g>`;
  const mini=(x,y,w,label,nid,view,mark)=>`<g class="node core-node gateway-node" ${attrs(view,nid,label,"gateway")}><rect class="target spine-mini" x="${x}" y="${y}" width="${w}" height="38" rx="9"/><text class="mini-mark" x="${x+13}" y="${y+24}">${mark}</text><text class="mini-title" x="${x+32}" y="${y+24}">${label}</text></g>`;
  const step=(x,y,w,n,label,hot=false)=>`<g class="loop-step ${hot?"current":""}"><rect x="${x}" y="${y}" width="${w}" height="28" rx="7"/><text x="${x+9}" y="${y+18}"><tspan>${n}</tspan> ${label}</text></g>`;
  const loop=(x,y,w,h)=>`<g class="node core-node runloop-node" ${attrs("loop","loop","RunLoop",`iteration ${iteration}, ${selected} tools selected`)}><rect class="target runloop-card" x="${x}" y="${y}" width="${w}" height="${h}" rx="16"/><circle class="loop-pulse" cx="${x+22}" cy="${y+23}" r="5"/><text class="loop-kicker" x="${x+36}" y="${y+27}">RUNLOOP · ITERATION ${iteration||"—"}</text><text class="loop-state" x="${x+w-16}" y="${y+27}" text-anchor="end">${completion}</text><text class="loop-title" x="${x+18}" y="${y+58}">Observe → act → verify</text>${step(x+18,y+74,(w-44)/2,"01","SELECT TOOLS")}${step(x+26+(w-44)/2,y+74,(w-44)/2,"02","CALL MODEL")}${step(x+18,y+110,(w-44)/2,"03","EXECUTE")}${step(x+26+(w-44)/2,y+110,(w-44)/2,"04","VERIFY")}${step(x+18,y+146,w-36,"05","COMPLETE_TASK",completion==="RUNNING")}<text class="loop-sub" x="${x+18}" y="${y+h-15}">${selected} action schemas selected · ${fmt(latest.tokens_in)} in / ${fmt(latest.tokens_out)} out</text></g>`;
  const metric=(x,y,w,label,value,sub,nid="telemetry")=>`<g class="node core-node metric-node" ${attrs("ops",nid,label,`${value} ${sub}`)}><rect class="target metric-card" x="${x}" y="${y}" width="${w}" height="62" rx="9"/><text class="metric-label" x="${x+12}" y="${y+17}">${label}</text><text class="metric-value" x="${x+12}" y="${y+39}">${value}</text><text class="metric-sub" x="${x+12}" y="${y+53}">${sub}</text></g>`;
  const header=(x,y,w)=>`<g class="node core-node runtime-node" ${attrs("settings","settings","Mino core runtime",`${providerRaw} ${modelRaw}`)}><rect class="target runtime-panel" x="${x}" y="${y}" width="${w}" height="58" rx="11"/><circle class="runtime-dot" cx="${x+18}" cy="${y+20}" r="4"/><text class="runtime-copy" x="${x+30}" y="${y+23}">MINO · CORE PROCESS</text><text class="runtime-model" x="${x+16}" y="${y+45}">${short(providerRaw,18,"provider")} · ${short(modelRaw,34,"model")} · ${esc(d.reasoning||"default")}</text><text class="runtime-copy end" x="${x+w-16}" y="${y+23}" text-anchor="end">${active?"TURN ACTIVE":"ONLINE"}</text><text class="runtime-meta" x="${x+w-16}" y="${y+45}" text-anchor="end">${sessions} SESSIONS · ${fmt(s.turns)} TRACED TURNS</text></g>`;
  const telemetry=(x,y,w,mobile=false)=>{
    const values=[
      ["TOKENS",`${fmt(latest.tokens_in||s.tokens_in)} / ${fmt(latest.tokens_out||s.tokens_out)}`,latest.tokens_in?"last turn · in / out":"usage log · in / out","tokens"],
      ["LATENCY",secs(latest.latency_ms||s.latency_avg),latest.latency_ms?"last turn":`p95 ${secs(s.latency_p95)}`,"latency"],
      ["EVALUATION",completion,latest.status?"complete_task status":"awaiting first turn","evaluation"],
      ["TOOL ERRORS",fmt(s.tool_errors),`${fmt(s.tool_calls)} recorded calls`,"errors"],
      ["RETRIEVAL",`${fmt(s.gate_retrieves)} / ${fmt(s.gate_skips)}`,"retrieve / skip","retrieval"],
      ["TRACES",fmt(s.trace_files),`${money(s.total_cost||0)} estimated`,"trace"],
    ];
    if(mobile){const cardW=(w-6)/2;return values.map((m,i)=>metric(x+(i%2)*(cardW+6),y+Math.floor(i/2)*70,cardW,...m)).join("");}
    return values.map((m,i)=>metric(x+i*(w+8),y,w,...m)).join("");
  };
  const toolLines=[`${builtinCount} built-ins · ${mcpCount} MCP`,`${extensionCount} sidecar tools · ${toolCount} total`];
  const sqliteLines=[`${tables} tables · ${fmtBytes((d.db||{}).size)}`,`${records} memory records · WAL state`];
  const taskLabel=active?short(active.goal,24,"active schedule"):short(latest.user_message,24,"waiting for a turn");

  if(window.innerWidth<720)return `<div class="arch-wrap core-wrap"><svg viewBox="0 0 420 1750" class="arch core-arch compact" role="img" aria-labelledby="spine-title spine-desc"><title id="spine-title">Mino Runtime Spine</title><desc id="spine-desc">A vertical live map of Mino gateways, session, context, RunLoop, provider, tools, SQLite state, verification telemetry, and external sidecars.</desc>${defs}<rect class="core-stage" x="7" y="7" width="406" height="1362" rx="22"/><rect class="core-grid" x="8" y="8" width="404" height="1360" rx="21"/>${header(24,22,372)}
    <text class="boundary-label" x="25" y="104">REQUEST SPINE</text>${wire("M210 174V211","e-gw-session")}${wire("M210 299V408","e-session-context")}${wire("M210 493V527","e-context-loop")}${wire("M210 739V772","e-loop-provider")}${wire("M210 857V892","e-loop-tools")}${wire("M210 988V1022","e-tools-db")}${wire("M210 1107V1142","e-db-trace")}
    <g class="node core-node gateway-stack" ${attrs("gateway","gateway","Gateways",`${sessions} sessions`)}><rect class="target spine-card" x="45" y="119" width="330" height="55" rx="12"/><text class="card-kicker" x="61" y="140">INGRESS</text><text class="gateway-list" x="61" y="160">TELEGRAM  ·  DASHBOARD  ·  SCHEDULER</text></g>
    ${node(65,211,290,88,"TURN STATE","Session",[taskLabel,`${sessions} known threads`],"gateway","session","S")}${wire("M155 299V323","e-session-cancel","dashed")}${wire("M265 299V323","e-session-checkpoint","dashed")}
    ${node(45,323,155,70,"CONTROL","Cancel",["context signal"],"activetasks","cancel","×","control-card")}${node(220,323,155,70,"SCHEDULER","Schedules",[`${(d.active_tasks||[]).length} scheduled`],"activetasks","checkpoint","C","control-card")}
    ${node(65,408,290,85,"ASSEMBLY","Context",[`${fmt(d.chat_pending)} pending messages`,`${records} recall records ready`],"memory/overview","context","C")}${loop(45,527,330,212)}
    ${node(65,772,290,85,"MODEL ROUTER","Provider",[`${short(providerRaw,18,"provider")} · ${short(modelRaw,24,"model")}`,`${esc(d.reasoning||"default")} reasoning`],"settings","provider","P")}
    ${node(65,892,290,96,"EXECUTION","Tool Registry",toolLines,"tools","tools","⌘")}
    ${node(65,1022,290,85,"PERSISTENCE","SQLite",sqliteLines,"database","sqlite","DB")}
    <text class="boundary-label" x="25" y="1135">OBSERVABILITY RAIL</text>${telemetry(28,1150,364,true)}
    <text class="external-label" x="22" y="1398">OUTSIDE CORE · NETWORK BOUNDARIES</text>${wire("M210 857V1417","e-provider-remote","external")}${wire("M210 988V1537","e-tools-sidecars","external")}
    ${node(45,1417,330,90,"REMOTE","Model API",[`${short(providerRaw,20,"provider")} endpoint`,"request / response"],"settings","remote","↗","external-card")}
    ${node(45,1537,330,82,"HTTP SIDECAR","minowrap",["universal tool adapter"],"tools","minowrap","M","external-card")}
    ${node(45,1643,330,82,"HTTP SIDECAR","fileingest",["document intake service"],"tools","fileingest","F","external-card")}</svg></div>`;

  return `<div class="arch-wrap core-wrap"><svg viewBox="0 0 1200 760" class="arch core-arch" role="img" aria-labelledby="spine-title spine-desc"><title id="spine-title">Mino Runtime Spine</title><desc id="spine-desc">A live overview of Mino's gateways, session, context, RunLoop, provider, tool registry, SQLite state, verification telemetry, and external sidecars.</desc>${defs}<rect class="core-stage" x="7" y="7" width="943" height="746" rx="22"/><rect class="core-grid" x="8" y="8" width="941" height="744" rx="21"/>${header(27,22,903)}
    <text class="boundary-label" x="30" y="105">MINO REQUEST FLOW</text><text class="external-label" x="975" y="105">EXTERNAL SERVICES</text>
    ${wire("M168 149H204V173","e-gw-session")}${wire("M168 195H204","e-gw-session")}${wire("M168 241H204V217","e-gw-session")}${wire("M344 199H379","e-session-context")}${wire("M519 199H544","e-context-loop")}${wire("M754 199H784","e-loop-provider")}${wire("M929 199H974","e-provider-remote","external")}${wire("M649 313V373","e-loop-tools")}${wire("M559 429H520V501","e-tools-db","dashed")}${wire("M449 249V291","e-context-checkpoint","dashed")}${wire("M274 249V291","e-session-cancel","dashed")}${wire("M449 353V501","e-checkpoint-db","dashed")}${wire("M749 429H974V373","e-tools-sidecars","external")}${wire("M749 449H974V487","e-tools-sidecars","external")}${wire("M464 591V617","e-db-trace","dashed")}
    ${mini(35,130,133,"TELEGRAM","telegram","gateway","T")}${mini(35,176,133,"DASHBOARD","dashboard","gateway","D")}${mini(35,222,133,"SCHEDULER","scheduler","settings","S")}
    ${node(204,151,140,98,"TURN STATE","Session",[taskLabel,`${sessions} known threads`],"gateway","session","S")}
    ${node(379,151,140,98,"ASSEMBLY","Context",[`${fmt(d.chat_pending)} pending messages`,`${records} recall records`],"memory/overview","context","C")}
    ${loop(544,96,210,217)}
    ${node(784,151,145,98,"MODEL ROUTER","Provider",[`${short(providerRaw,15,"provider")} · ${short(modelRaw,18,"model")}`,`${esc(d.reasoning||"default")} reasoning`],"settings","provider","P")}
    ${node(194,291,155,76,"CONTROL","Cancel",["context signal"],"activetasks","cancel","×","control-card")}
    ${node(369,291,155,76,"SCHEDULER","Schedules",[`${(d.active_tasks||[]).length} scheduled · round ${iteration||"—"}`],"activetasks","checkpoint","C","control-card")}
    ${node(559,373,190,112,"EXECUTION","Tool Registry",toolLines,"tools","tools","⌘")}
    ${node(359,501,210,90,"PERSISTENCE","SQLite",sqliteLines,"database","sqlite","DB")}
    <text class="boundary-label" x="30" y="614">OBSERVABILITY · TRACE LOGS</text>${telemetry(29,628,142)}
    ${node(974,139,207,104,"REMOTE","Model API",[`${short(providerRaw,18,"provider")} endpoint`,"tokens · latency · response"],"settings","remote","↗","external-card")}
    <g class="sidecar-boundary"><rect x="965" y="320" width="225" height="269" rx="16"/><text class="sidecar-label" x="983" y="344">HTTP TOOL SIDECARS · ${extensionCount} TOOLS</text></g>
    ${node(978,359,199,98,"SIDECAR","minowrap",["universal tool adapter","tools.json · :9876"],"tools","minowrap","M","external-card")}
    ${node(978,473,199,98,"SIDECAR","fileingest",["document intake service","HTTP · :9103"],"tools","fileingest","F","external-card")}
    <text class="core-caption" x="600" y="742" text-anchor="middle">ONLY THE CURRENT TRACE STAGE ANIMATES · CLICK ANY NODE TO INSPECT IT</text></svg></div>`;
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
  setAskOpen(true);
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
  const facts = (d.facts||[]).length, episodes = (d.episodes||[]).length, skills = (d.skills||[]).length, playbooks = (d.playbooks||[]).length;
  const pillars = [
    ["✦","Semantic","semantic",facts,"facts","Durable knowledge Mino can retrieve across conversations."],
    ["◷","Episodic","episodic",episodes,"episodes","Dated highlights distilled from longer conversations."],
    ["⌘","Procedural","skills",skills,"skills","Reusable instructions loaded only when they are relevant."],
    ["◇","Playbooks","playbooks",playbooks,"playbooks","Executable filesystem procedures with stages, outputs, and schedules."],
  ].map(([icon,t,sub,n,unit,desc]) => `<div class="memory-pillar" role="link" tabindex="0" onclick="location.hash='memory/${sub}'" onkeydown="if(event.key==='Enter'||event.key===' '){event.preventDefault();location.hash='memory/${sub}'}">
      <span class="memory-pillar-icon">${icon}</span><div><span>${t}</span><strong>${n} ${unit}</strong><p>${desc}</p></div><b>→</b></div>`).join("");
  return `<section class="memory-hero"><div><div class="eyebrow">MEMORY OBSERVATORY</div><h2 class="memory-title">What Mino carries forward.</h2>
      <p>Inspect durable knowledge, lived context, reusable skills, and the pipeline that keeps them current.</p></div>
      <div class="memory-health"><span class="runtime-kicker"><i></i> MEMORY STATUS</span><strong>${facts+episodes+skills+playbooks} records</strong><span>${d.chat_pending||0} messages queued</span><small>SQLite · FTS5 · human-readable mirror</small></div></section>
    <section class="memory-pillar-grid">${pillars}</section>
    <section class="memory-retrieval"><div class="overview-section-head"><div><span class="section-kicker">RETRIEVAL</span><h2>Memory enters only when needed</h2></div><span class="section-note">the gate protects latency and relevance</span></div>${gateSplit(s)}</section>
    <section class="memory-source"><div><span class="section-kicker">GRAPH SOURCE · SQLITE DIAGNOSTIC</span><h3>Curated in Markdown. Audited in SQLite.</h3><p>Graph claims are authoritative semantic memory. SQLite remains available for migration parity and operational history.</p></div>
      <a href="#database">Open database →</a></section>
    <div class="memory-files"><span>FILES</span>${reveal("state.db","state.db")}${reveal("MEMORY.md","MEMORY.md")}${reveal("SOUL.md","SOUL.md")}${reveal("skills","skills/")}${reveal("playbooks","playbooks/")}</div>`;
}
function memSemantic(d){
  const facts = d.facts || [];
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">SEMANTIC MEMORY</span><h2>Durable facts</h2><p>The smallest, most reusable knowledge store. Corrections and deletions are active on the next turn.</p></div><strong>${facts.length}</strong></section>`;
  if (!facts.length) return h + `<div class="memory-empty"><span>✦</span><strong>No facts stored yet</strong><p>Mino will place durable knowledge here when memory tools or consolidation save it.</p></div>`;
  h += `<div class="memory-records">${facts.map(f => { const rawId=String(f.id), id=jsArg(rawId); return `<div class="memory-record" id="fact-${esc(rawId)}">
      <div class="record-subject"><span>${esc(f.subject)}</span><small>${esc(f.source||"unknown source")}</small></div>
      <div class="fc">${esc(f.content)}</div><div class="record-date">${esc((f.created_at||"").slice(0,10)||"—")}</div>
      <div class="record-actions"><a class="reveal" onclick="editFact(${id})">edit</a><a class="reveal del" onclick="delMem('delete_fact',${id})">delete</a></div></div>`; }).join("")}</div>`;
  return h;
}
function memEpisodic(d){
  const episodes = d.episodes || [];
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">EPISODIC MEMORY</span><h2>Conversation highlights</h2><p>One distilled summary per consolidation. Raw messages remain available in the chat log.</p></div><strong>${episodes.length}</strong></section>
    <div class="memory-callout"><span>◷</span><p>Episodes stay intentionally small: they preserve what happened without replaying every message. <a href="#database/chat_log">Inspect the raw chat log →</a></p></div>`;
  if (!episodes.length) return h + `<div class="memory-empty"><span>◷</span><strong>No episodes yet</strong><p>Conversation highlights will appear here after a successful consolidation.</p></div>`;
  h += `<div class="episode-timeline">${episodes.map(e => `<div class="episode-item"><span class="episode-dot"></span><div><time>${esc(e.happened_at||"Undated")}</time><p>${esc(e.summary)}</p></div><a class="reveal del" onclick="delMem('delete_episode',${jsArg(e.id)})">delete</a></div>`).join("")}</div>`;
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
function memPlaybooks(d){
  const playbooks = d.playbooks || [];
  let h = `<section class="memory-tab-head"><div><span class="section-kicker">EXECUTABLE PROCEDURES</span><h2>Playbooks</h2><p>Filesystem state machines that Mino can choose to run through the normal runtime.</p></div><strong>${playbooks.length}</strong></section>
    <div class="memory-callout"><span>◇</span><p>Playbooks are separate from skills: skills guide Mino’s judgment; playbooks define repeatable stages, outputs, and schedules. ${reveal("playbooks","Open the playbooks folder →")}</p></div>`;
  if (!playbooks.length) return h + `<div class="memory-empty"><span>◇</span><strong>No playbooks found</strong><p>Ask Mino to create a repeatable workflow, or add a playbook folder under ~/.mino/playbooks/.</p></div>`;
  h += `<div class="playbook-grid">${playbooks.map(pb => {
    if (pb.error) return `<article class="memory-editor-card playbook-card"><div class="memory-editor-head"><div><code>${esc(pb.name)}</code><p class="playbook-error">${esc(pb.error)}</p></div><span class="srcpill warn">invalid</span></div><div class="memory-editor-actions"><span class="meta">${esc(pb.path||"")}</span></div></article>`;
    const stages = pb.stages || [], outputs = pb.outputs || [];
    return `<article class="memory-editor-card playbook-card"><div class="memory-editor-head"><div><code>${esc(pb.name)}</code><p>${esc(pb.description||"No description")}</p></div><span class="srcpill ${pb.status==="active"?"good":"warn"}">${esc(pb.status||"active")}</span></div>
      <div class="playbook-meta"><span>${stages.length} stage${stages.length===1?"":"s"}</span>${pb.schedule?`<span>schedule · ${esc(pb.schedule)}</span>`:""}${pb.notify?`<span>Telegram delivery</span>`:""}</div>
      <div class="playbook-stages">${stages.map((stage,i)=>`<details class="playbook-stage" ${i===0?"open":""}><summary><b>${String(stage.number||i+1).padStart(2,"0")}</b><span>${esc(stage.name||"stage")}</span>${(stage.tools||[]).length?`<small>${stage.tools.map(t=>esc(t)).join(" · ")}</small>`:""}</summary>${stage.context?`<pre class="playbook-contract">${esc(stage.context)}</pre>`:`<div class="meta">No contract text</div>`}</details>`).join("")}</div>
      ${outputs.length?`<div class="playbook-outputs"><span>OUTPUT</span>${outputs.map(path=>`<code>${esc(path)}</code>`).join("")}</div>`:`<div class="meta playbook-empty-output">No output recorded yet</div>`}
      <div class="memory-editor-actions"><span class="meta">${esc(pb.path||"")}</span>${reveal(pb.path||"","open folder")}</div></article>`;
  }).join("")}</div>`;
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
  // Group events into stage blocks: a stage run is a contiguous run of events
  // carrying the same playbook+stage tag. Untagged events (main loop) stay flat.
  const groups=[];
  for(const e of events){
    const key=e.playbook&&e.stage?`${e.playbook}/${e.stage}`:null;
    if(key&&groups.length&&groups[groups.length-1].key===key&&!groups[groups.length-1].closed){
      groups[groups.length-1].events.push(e);
    } else {
      const last=groups[groups.length-1];
      if(last&&last.key) last.closed=true;
      groups.push({key,playbook:e.playbook,stage:e.stage,events:[e]});
    }
  }
  const row=e=>`<div><span class="trace-mark ${esc(e.type)}"></span><code>${esc(e.type)}</code><p>${esc(String(e.detail||"").slice(0,120))}</p><time>${esc((e.ts||"").replace("T"," ").slice(11,19))}</time></div>`;
  const body=groups.map(g=>g.key
    ? `<details class="trace-stage" open><summary><span class="trace-mark stage"></span><code>${esc(g.playbook)}</code><b>${esc(g.stage)}</b><time>${g.events.length} events</time></summary><div class="trace-stream">${g.events.map(row).join("")}</div></details>`
    : row(g.events[0])).join("");
  return `<section class="surface-head"><div><span class="section-kicker">TRACE STREAM</span><h2>What happened, in order</h2><p>Structured JSONL events from every turn, model pass, and tool invocation. Stage work is grouped — expand a stage to see its calls.</p></div><strong>${esc(d.trace_file||"no trace")}</strong></section>
    <div class="trace-layout"><section><div class="overview-section-head"><div><span class="section-kicker">EVENTS</span><h2>Latest trace lines</h2></div>${reveal("traces","open folder")}</div>${events.length?`<div class="trace-stream">${body}</div>`:`<div class="surface-empty"><span>⌁</span><strong>No trace lines today</strong></div>`}</section>
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
  return `<section class="settings-hero"><div><span class="section-kicker">RUNTIME CONFIGURATION</span><h2>Simple, visible, restart-bound.</h2><p>Manage provider priority, credentials, and OAuth connections from one local surface.</p></div><div class="settings-runtime"><span class="runtime-kicker"><i></i> ACTIVE RUNTIME</span><strong>${esc(d.active_provider||d.provider)} · ${esc(d.model)}</strong><span class="provider-clickable" onclick="toggleProviderMenu(event)" title="Switch provider, model, or reasoning">${esc(d.reasoning||"default")} reasoning · ${(d.sessions||[]).length} conversations <i class="dropdown-arrow">▾</i></span><small>${esc(d.home)}</small></div></section>
    <div class="overview-section-head"><div><span class="section-kicker">PROVIDER CHAIN</span><h2>Priority and health</h2></div><button class="oauth-btn" onclick="showAddProvider()">+ Add Provider</button></div>
    <form id="add-provider-form" class="add-provider-form" hidden onsubmit="event.preventDefault();addProvider()"><input id="provider-name" placeholder="Name" required><input id="provider-base-url" type="url" placeholder="Base URL" required><input id="provider-model" placeholder="Model" required><input id="provider-small-model" placeholder="Small model"><input id="provider-api-key" type="password" placeholder="API key (optional)"><input id="provider-priority" type="number" min="1" value="10" placeholder="Priority"><button type="submit">Add</button><span id="provider-form-status" aria-live="polite"></span></form>
    ${providers.length?`<div class="provider-stack">${providers.map((p,i)=>{ const name=encodeURIComponent(p.name).replace(/'/g,"%27"); return `<article><span class="provider-priority">${p.priority}</span><div class="provider-main"><div><strong>${esc(p.name)}</strong><span class="status-chip ${p.key_set?"good":"warn"}">${p.key_set?"key set":"key missing"}</span></div><p>${esc(p.model)}${p.small_model?` · small ${esc(p.small_model)}`:""}</p><small>${esc(p.base_url)}</small></div><button class="provider-remove" title="Remove provider" aria-label="Remove provider" onclick="removeProvider(decodeURIComponent('${name}'))">✕</button>${i<providers.length-1?`<span class="fallback-arrow">↓ fallback</span>`:""}</article>`; }).join("")}</div>`:`<div class="surface-empty"><span>◇</span><strong>No provider snapshot available</strong><p>Add a provider to providers.json.</p></div>`}
    <div class="overview-section-head"><div><span class="section-kicker">OAUTH</span><h2>Connected accounts</h2></div><span id="oauth-status" class="section-note" aria-live="polite">${esc(oauthMessage)}</span></div><div id="oauth-providers" class="oauth-providers"><div class="surface-empty compact"><strong>Loading OAuth providers…</strong></div></div>
    <div class="settings-grid"><section><span class="settings-icon">⌘</span><div><span class="section-kicker">CONFIG FILE</span><strong>providers.json</strong><p>${esc(cfg.config_file||"")}</p></div>${reveal("providers.json","open file")}</section><section><span class="settings-icon">▦</span><div><span class="section-kicker">STATE HOME</span><strong>Mino home</strong><p>${esc(d.home)}</p></div>${reveal("","open folder")}</section><section><span class="settings-icon">✦</span><div><span class="section-kicker">PERSONALITY</span><strong>SOUL.md</strong><p>Editable safely from Memory.</p></div><a href="#memory/soul">Open SOUL →</a></section></div>`;
}

function activeTasksView(d){
  const tasks=d.active_tasks||[];
  return `<section class="tasks-hero"><div><span class="section-kicker">SCHEDULES</span><h2>Work that runs on a schedule.</h2><p>Playbooks fire at their configured times and deliver results automatically.</p></div><div class="tasks-count"><strong>${tasks.length}</strong><span>active schedule${tasks.length===1?"":"s"}</span><small>${tasks.reduce((n,t)=>n+(t.stages||0),0)} stages configured</small></div></section>
    ${tasks.length?`<div class="task-list">${tasks.map((t,i)=>`<article><header><span class="task-index">${String(i+1).padStart(2,"0")}</span><span class="status-chip good"><i></i> ${esc(t.status||"active")}</span></header><h3>${esc(t.goal)}</h3><div class="task-progress"><span style="width:${Math.min(92,20+(t.round||0)*12)}%"></span></div><div class="task-meta"><span>round ${t.round||0}</span><span>${(t.tools_used||[]).length} tools used</span><span>${(t.discoveries||[]).length} discoveries</span></div>${(t.tools_used||[]).length?`<div class="task-tools">${t.tools_used.map(x=>`<code>${esc(x)}</code>`).join("")}</div>`:""}${(t.discoveries||[]).length?`<ul>${t.discoveries.map(x=>`<li>${esc(x)}</li>`).join("")}</ul>`:""}</article>`).join("")}</div>`:`<div class="tasks-empty"><div class="checkpoint-orbit"><span>✓</span></div><strong>No scheduled playbooks</strong><p>Add a schedule by asking Mino to run a playbook on a recurring basis.</p><a href="#memory/playbooks">Browse playbooks →</a></div>`}
    <section class="checkpoint-flow"><div><span>1</span><strong>Schedule</strong><small>time & playbook</small></div><b>→</b><div><span>2</span><strong>Fire</strong><small>at scheduled time</small></div><b>→</b><div><span>3</span><strong>Deliver</strong><small>result to Telegram</small></div></section>`;
}

function onboardingView(){
  const field=(label,id,placeholder,type="text",hint="")=>`<label class="onboarding-field"><span>${label}</span><input id="${id}" type="${type}" placeholder="${esc(placeholder)}" onfocus="markEditing()" oninput="markEditing()"><small>${hint}</small></label>`;
  return `<div class="onboarding-shell"><aside><img class="onboarding-logo" src="/static/assets/logo-full.png" alt="Mino — Personal AI Assistant"><span class="section-kicker">WELCOME TO MINO</span><h2>Pick your AI brain.</h2><p>Choose how Mino connects to an LLM. Your keys stay on your server — never sent anywhere else.</p><div class="onboarding-points"><div><span>01</span><p><strong>Private state</strong><small>One local SQLite file</small></p></div><div><span>02</span><p><strong>Provider resilience</strong><small>Priority and fallback ready</small></p></div><div><span>03</span><p><strong>Everywhere access</strong><small>Dashboard and optional Telegram</small></p></div></div></aside>
    <section class="onboarding-form"><div><span class="section-kicker">PROVIDER SETUP</span><h3>Connect the first model</h3><p>Keys are written to the server environment file and never returned by the dashboard API.</p></div><div class="onb-provider-buttons"><button class="onb-btn" onclick="startOAuth('codex')"><span class="onb-btn-icon">⬡</span>ChatGPT<span>Codex device flow</span></button><button class="onb-btn" onclick="startOAuth('claude')"><span class="onb-btn-icon">✤</span>Claude<span>PKCE login</span></button><button class="onb-btn onb-btn-alt" onclick="document.getElementById('onb-manual').hidden=!document.getElementById('onb-manual').hidden"><span class="onb-btn-icon">⌨</span>I have a key<span>Manual setup</span></button></div><div id="onb-manual" hidden><div class="form-grid">${field("Provider name","onb-provider","mimo","text","A short label for this connection")}${field("API key","onb-apikey","sk-...","password","Stored in mino.env")}${field("Base URL","onb-baseurl","https://api.openai.com/v1","url","OpenAI-compatible endpoint")}${field("Main model","onb-model","mimo-v2.5","text","Used for conversations and tools")}${field("Small model","onb-small","mimo-v2.5","text","Optional background work")}</div><button id="onb-save" class="onboarding-submit" onclick="saveOnboarding()">Save configuration <span>→</span></button></div><div id="onb-msg" class="onboarding-message" aria-live="polite"></div><details class="onb-telegram"><summary>Set up Telegram (optional)</summary><p>Create a bot with <a href="https://t.me/BotFather" target="_blank">@BotFather</a>, then paste the token here:</p><label class="onboarding-field"><input id="onb-tgtoken" type="password" placeholder="123456:ABC-DEF..." onfocus="markEditing()" oninput="markEditing()"><small>Optional — connect now or later in Settings</small></label></details><input id="onb-tavily" type="hidden" value=""><small class="onboarding-footnote">Mino restarts once after saving. The dashboard reconnects automatically.</small></section></div>`;
}

const RESPONSIBILITY_STATUS = {
  needs_you:"Needs you", working:"Working", waiting:"Waiting",
  blocked:"Blocked", verified:"Verified", stopped:"Stopped",
};
const responsibilityStatus = status => RESPONSIBILITY_STATUS[status] || status || "Unknown";
function responsibilityTime(value, withDate=false){
  if (!value) return "No time recorded";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("en-MY", {
    timeZone:(D&&D.timezone)||"Asia/Kuala_Lumpur",
    day:withDate?"numeric":undefined, month:withDate?"short":undefined,
    hour:"numeric", minute:"2-digit",
  }).format(date);
}
function responsibilityEvidence(value){
  const lines=String(value||"").split("\n").filter(Boolean);
  if(!lines.length) return "";
  return `<details class="responsibility-evidence"><summary>Inspect evidence</summary><div>${lines.map(line=>{
    if(line.startsWith("artifact:")){
      const label=line.slice(9), path=label.replace(/\s+\(\d+ bytes\)$/,"");
      return `<a href="/api/responsibility-evidence?path=${encodeURIComponent(path)}" target="_blank" rel="noopener"><span>Artifact</span>${esc(label)}</a>`;
    }
    const split=line.indexOf(":"), kind=split>0?line.slice(0,split):"Evidence", text=split>0?line.slice(split+1):line;
    return `<p><span>${esc(kind)}</span>${esc(text)}</p>`;
  }).join("")}</div></details>`;
}
function responsibilityEntry(entry){
  const latest=entry.latest||{}, status=entry.status||"waiting";
  const action=entry.next_action?`<div class="responsibility-next"><span>Next</span><strong>${esc(entry.next_action)}</strong><small>${esc(entry.next_owner||entry.owner||"mino")}</small></div>`:"";
  const needsOwner=status==="needs_you"||status==="blocked";
  const ownerActions=needsOwner?`<div class="responsibility-actions"><a href="#responsibility/${encodeURIComponent(entry.id)}">Review next step</a><button type="button" onclick="askAboutResponsibility(${jsArg(entry.title)})">Ask Mino</button></div>`:"";
  return `<article class="responsibility-entry status-${esc(status)}">
    <time>${esc(responsibilityTime(latest.at))}</time>
    <div class="responsibility-copy"><div class="responsibility-title"><a href="#responsibility/${encodeURIComponent(entry.id)}">${esc(entry.title)}</a><span class="responsibility-status ${esc(status)}">${esc(responsibilityStatus(status))}</span></div>
      <p>${esc(latest.summary||entry.outcome||"No meaningful update recorded.")}</p>${action}${ownerActions}${responsibilityEvidence(latest.evidence)}</div>
    <a class="responsibility-open" href="#responsibility/${encodeURIComponent(entry.id)}" aria-label="Open Responsibility">→</a>
  </article>`;
}
function todayView(d){
  const state=d.responsibilities||{}, entries=state.today||[];
  if(state.error) return `<div class="responsibility-empty"><span>!</span><strong>Responsibility state is unavailable.</strong><p>${esc(state.error)}</p></div>`;
  const needs=entries.filter(x=>x.status==="needs_you"||x.status==="blocked").length;
  const date=new Intl.DateTimeFormat("en-MY",{timeZone:d.timezone||"Asia/Kuala_Lumpur",weekday:"long",day:"numeric",month:"long"}).format(new Date());
  return `<section class="responsibility-hero"><div><h2>${esc(date)}</h2><p>Meaningful changes in work Mino has accepted—not raw runtime activity.</p></div><p class="responsibility-summary ${needs?"attention":""}"><strong>${needs?`${needs} need${needs===1?"s":""} you`:"Nothing needs you"}</strong><span>${entries.length} meaningful update${entries.length===1?"":"s"} today</span></p></section>
    ${entries.length?`<div class="today-journal">${entries.map(responsibilityEntry).join("")}</div>`:`<div class="responsibility-empty"><span>◉</span><strong>Nothing needs attention today.</strong><p>Mino has not recorded a meaningful Responsibility change yet.</p><a href="#work">Review current Work →</a></div>`}`;
}
function workView(d){
  const state=d.responsibilities||{}, entries=state.work||[];
  if(state.error) return `<div class="responsibility-empty"><span>!</span><strong>Responsibility state is unavailable.</strong><p>${esc(state.error)}</p></div>`;
  const groups=["needs_you","blocked","working","waiting","verified","stopped"];
  const open=entries.filter(x=>!["verified","stopped"].includes(x.status)).length;
  return `<section class="responsibility-hero"><div><h2>Work Mino owns.</h2><p>Standing Routines and durable outcomes, grouped by their truthful current state.</p></div><p class="responsibility-summary"><strong>${open} open</strong><span>${entries.length} Responsibilities in total</span></p></section>
    ${entries.length?groups.map(status=>{const items=entries.filter(x=>x.status===status);return items.length?`<section class="work-group"><header><h3>${esc(responsibilityStatus(status))}</h3><span>${items.length}</span></header><div>${items.map(responsibilityEntry).join("")}</div></section>`:""}).join(""):`<div class="responsibility-empty"><span>□</span><strong>Mino owns no Work yet.</strong><p>A request appears here when it must survive a turn, dependency, deadline, or future verification.</p></div>`}`;
}
function overviewResponsibility(d){
  const state=d.responsibilities||{}, entries=state.work||[];
  if(state.error) return `<div class="responsibility-empty"><span>!</span><strong>Responsibility state is unavailable.</strong><p>${esc(state.error)}</p><a href="#ops">Inspect runtime health →</a></div>`;
  const attention=entries.filter(x=>["needs_you","blocked","working"].includes(x.status)).slice(0,3);
  const verified=(state.today||[]).filter(x=>x.status==="verified").slice(0,3);
  const open=entries.filter(x=>!['verified','stopped'].includes(x.status)).length;
  return `<section class="overview-responsibility"><div class="overview-responsibility-head"><div><span class="section-kicker">OWNER WORKSPACE</span><h2>What matters now.</h2><p>Accepted responsibilities, current truth, and verified outcomes in one place.</p></div><div class="overview-work-count"><strong>${open}</strong><span>open Responsibilit${open===1?"y":"ies"}</span><small>${entries.length} total in Work</small></div></div>
    <div class="overview-responsibility-grid"><section><header><div><span class="section-kicker">CURRENT</span><h3>Needs attention</h3></div><a href="#work">Open Work →</a></header>${attention.length?`<div class="overview-entry-list">${attention.map(responsibilityEntry).join("")}</div>`:`<div class="overview-clear"><span>✓</span><strong>Nothing is waiting on you.</strong><p>Mino has no active blocked or owner-dependent Responsibility.</p></div>`}</section>
      <section><header><div><span class="section-kicker">TODAY</span><h3>Verified outcomes</h3></div><a href="#today">Open journal →</a></header>${verified.length?`<div class="overview-entry-list">${verified.map(responsibilityEntry).join("")}</div>`:`<div class="overview-clear"><span>◉</span><strong>No verified outcome yet.</strong><p>Meaningful completed work will appear here as Mino proves it.</p></div>`}</section></div>
    <nav class="overview-links" aria-label="Workspace shortcuts"><a href="#today"><span>◉</span><strong>Today</strong><small>Journal of meaningful changes</small></a><a href="#work"><span>□</span><strong>Work</strong><small>Everything Mino owns</small></a><a href="#gateway"><span>↔</span><strong>Conversations</strong><small>Every channel, one thread</small></a><a href="#ops"><span>⌁</span><strong>Runtime health</strong><small>Inspect the operating system</small></a></nav></section>`;
}
function responsibilityDetailView(detail){
  const history=detail.history||[];
  return `<div class="responsibility-detail-head"><a href="#work">← Work</a><span class="responsibility-status ${esc(detail.status)}">${esc(responsibilityStatus(detail.status))}</span><h2>${esc(detail.title)}</h2><p>${esc(detail.outcome)}</p></div>
    <div class="responsibility-detail-grid"><div><section class="responsibility-current"><span class="section-kicker">CURRENT STATE</span><div><span>Owner</span><strong>${esc(detail.owner||"—")}</strong></div><div><span>Next action</span><strong>${esc(detail.next_action||"No next action recorded")}</strong><small>${esc(detail.next_owner||detail.owner||"")}</small></div></section>
      <section class="responsibility-history"><header><span class="section-kicker">IMMUTABLE HISTORY</span><strong>${history.length} event${history.length===1?"":"s"}</strong></header>${history.map(event=>`<article><time>${esc(responsibilityTime(event.at,true))}</time><i class="${esc(event.status)}"></i><div><div><strong>${esc(responsibilityStatus(event.status))}</strong><span>${esc(event.type)}</span></div><p>${esc(event.summary)}</p>${responsibilityEvidence(event.evidence)}</div></article>`).join("")}</section></div>
      <aside class="responsibility-policy"><span class="section-kicker">POLICY &amp; EVIDENCE</span><div><span>Kind</span><strong>${esc(detail.kind)}</strong></div><div><span>Schedule</span><strong>${esc(detail.schedule||"Not scheduled")}</strong></div><div><span>Due</span><strong>${esc(detail.due_at?responsibilityTime(detail.due_at,true):"No deadline")}</strong></div><div><span>Last run</span><strong>${esc(detail.last_run_at?responsibilityTime(detail.last_run_at,true):"Never")}</strong></div><div><span>Verification</span><strong>${esc(detail.verification||"No condition recorded")}</strong></div></aside></div>`;
}
async function loadResponsibilityDetail(id){
  const target=document.getElementById("responsibility-detail");
  if(!target) return;
  try{
    const response=await fetch("/api/responsibilities?id="+encodeURIComponent(id));
    if(!response.ok) throw new Error(response.status===404?"Responsibility not found":"Could not load Responsibility");
    target.innerHTML=responsibilityDetailView(await response.json());
  }catch(error){
    target.innerHTML=`<div class="responsibility-empty"><span>!</span><strong>${esc(error.message)}</strong><p>Return to Work and try again.</p></div>`;
  }
}

function systemView(d, sub){
  sub=sub||"overview";
  const section=sub.startsWith("tool")||sub==="mcp"?"tools":sub.startsWith("database-")?"database":sub.startsWith("files-")?"files":sub;
  const tabs=[["overview","Overview"],["runtime","Runtime"],["tools","Tools"],["database","Database"],["files","Files"],["settings","Settings"]];
  const h=subtabBar("system",tabs,section);
  if(sub==="runtime") return h+`<section class="system-intro"><h2>Runtime and execution</h2><p>The machinery behind owner outcomes. Trace activity stays here until you choose to inspect it.</p></section><section class="overview-cover">${archSVG(d)}</section>`+VIEWS.loop(d);
  if(sub==="tools") return h+VIEWS.tools(d,"available");
  if(sub==="tool-results") return h+VIEWS.tools(d,"results");
  if(sub==="mcp") return h+VIEWS.tools(d,"mcp");
  if(sub==="database") return h+VIEWS.database(d,"overview");
  if(sub.startsWith("database-")) return h+VIEWS.database(d,decodeURIComponent(sub.slice(9)));
  if(sub==="files") return h+VIEWS.files(d);
  if(sub.startsWith("files-")) return h+VIEWS.files(d,sub.slice(6));
  if(sub==="settings") return h+settingsView(d);
  if(sub==="schedules") return h+activeTasksView(d);
  if(sub==="usage") return h+opsUsage(d);
  if(sub==="traces") return h+opsTraces(d);
  if(sub==="release") return h+opsRelease(d);
  return h+opsOverview(d);
}

const VIEWS = {
  today(d){ return todayView(d); },
  work(d){ return workView(d); },
  responsibility(d, id){
    id=decodeURIComponent(id||"");
    setTimeout(()=>loadResponsibilityDetail(id),0);
    return `<div id="responsibility-detail">${spinner()}</div>`;
  },
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
  conversations(d){ return VIEWS.gateway(d); },
  system(d, sub){ return systemView(d,sub); },
  overview(d){
    return `${overviewResponsibility(d)}<section class="overview-runtime"><div class="cover-intro"><div><span class="section-kicker">RUNTIME INSPECTION</span><h2>Follow the process, live.</h2><p>Inspect every turn from gateway to verified completion, with state, tools, sidecars, and telemetry in one map.</p></div><div class="cover-status"><span><i></i> Operational</span><span class="arch-status"></span><a href="#settings">Runtime settings →</a></div></div>
      <section class="overview-cover">${archSVG(d)}</section></section>`;
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
    const tabs = [["overview","Overview"],["semantic","Knowledge",(d.facts||[]).length],
      ["episodic","Episodes",(d.episodes||[]).length],["graph","Graph"],["skills","Skills",(d.skills||[]).length],
      ["playbooks","Playbooks",(d.playbooks||[]).length],
      ["soul","SOUL"],["consolidation","Consolidation",d.chat_pending]];
    let h = subtabBar("memory", tabs, sub);
    if (sub==="semantic") return h + memSemantic(d);
    if (sub==="episodic") return h + memEpisodic(d);
    if (sub==="graph") return h + VIEWS.graph(d);
    if (sub==="skills") return h + memSkills(d);
    if (sub==="playbooks") return h + memPlaybooks(d);
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

  graph(d){
    const raw = d.graph;
    if (!raw || !raw.facts) return `<section class="graph-hero"><div><span class="section-kicker">KNOWLEDGE GRAPH</span><h2>Memory Graph</h2><p>No graph data yet. Memories are built during conversations.</p></div></section>`;
    setTimeout(() => initGraph(raw), 50);
    return `<section class="graph-hero"><div><span class="section-kicker">KNOWLEDGE GRAPH</span><h2>Memory Graph</h2><p>${Object.keys(raw.facts).length} facts · ${countEdges(raw.facts)} relationships</p></div>
      <div class="graph-controls">
        <div class="graph-query"><input id="graph-search" type="search" placeholder="Query Mino's memories..." oninput="filterGraph()"><button onclick="clearGraphQuery()" title="Clear query">Clear</button></div>
        <label class="graph-toggle"><input type="checkbox" checked onchange="filterGraph()" data-type="semantic"> Semantic</label>
        <label class="graph-toggle"><input type="checkbox" checked onchange="filterGraph()" data-type="episodic"> Episodic</label>
        <span id="graph-query-status" class="graph-query-status">All memories visible</span>
        <span class="graph-hint"><i style="background:#4F8BC9"></i><i style="background:#7C6FD0"></i><i style="background:#39A98A"></i> hover an edge · select a memory</span>
      </div></section>
      <div class="graph-viewport"><canvas id="graph-canvas"></canvas></div>
      <aside id="graph-detail" class="graph-detail"></aside>`;
  },
};

// ---- Live Runtime Spine animation: illuminate only the active trace stage,
// driven by the event stream so any gateway can move the same process map.
const STAGE = {
  turn_start: {nodes:["gateway","session","context"], edges:["e-gw-session","e-session-context"], label:"assembling turn"},
  llm:        {nodes:["loop","provider","remote"],     edges:["e-context-loop","e-loop-provider","e-provider-remote"], label:"model pass"},
  tool:       {nodes:["loop","tools"],                 edges:["e-loop-tools"], label:"executing tool"},
  completion: {nodes:["loop","evaluation"],            edges:[], label:"verifying completion"},
  gate:       {nodes:["retrieval","sqlite"],           edges:["e-tools-db"], label:"evaluating recall"},
  turn_end:   {nodes:["evaluation","trace"],           edges:["e-db-trace"], label:"turn recorded"},
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
    ["context","sqlite","retrieval"].forEach(n => hot(`[data-node="${n}"]`,"hot",1000));
    hot(`[data-edge="e-tools-db"]`,"live",1000);
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
const TITLES = {chat:"Chat & watch", conversations:"Conversations", system:"System", ops:"System",
                today:"Today — owner journal", work:"Work — responsibilities", responsibility:"Responsibility",
                database:"Database — everything Mino stores (state.db)", activetasks:"Active Schedules — playbook runs",
                files:"Files — VPS artifacts and outputs",
                graph:"Memory Graph — interactive visualization",
                onboarding:"Welcome — set up your Mino"};

function canonicalRoute(){
  const parts=(location.hash||"#today").slice(1).split("/"), raw=parts[0]||"today", sub=parts[1]||null;
  let route=[raw,sub];
  if(raw==="overview") route=["today",null];
  else if(raw==="gateway"||raw==="chat") route=["conversations",null];
  else if(raw==="loop") route=["system","runtime"];
  else if(raw==="tools") route=["system",sub==="results"?"tool-results":sub==="mcp"?"mcp":"tools"];
  else if(raw==="database") route=["system",sub?`database-${sub}`:"database"];
  else if(raw==="files") route=["system",sub?`files-${sub}`:"files"];
  else if(raw==="ops") route=["system",sub&&sub!=="overview"?sub:"overview"];
  else if(raw==="settings") route=["system","settings"];
  else if(raw==="activetasks") route=["system","schedules"];
  else if(raw==="graph") route=["memory","graph"];
  const canonical=route[0]+(route[1]?`/${route[1]}`:"");
  if(canonical!==parts.slice(0,2).filter(Boolean).join("/")) history.replaceState(null,"","#"+canonical);
  return route;
}

function render(){
  if (!D) return;
  // onboarding gate: redirect if no API key configured
  if (D.needs_onboarding && !location.hash.startsWith("#onboarding")) {
    location.hash = "#onboarding"; return;
  }
  if (!D.needs_onboarding && location.hash.startsWith("#onboarding")) {
    location.hash = "#today"; return;
  }
  const [v, sub] = canonicalRoute();
  const view = VIEWS[v] ? v : "today";
  document.body.classList.toggle("onboarding-mode", view === "onboarding");
  document.body.dataset.view=view;
  const subChanged = sub !== activeSub || view !== activeView;
  const primary=view==="responsibility"?"work":view;
  document.querySelectorAll("[data-v]").forEach(a=>a.classList.toggle("on", a.dataset.v===primary));
  const title=TITLES[view] || view[0].toUpperCase()+view.slice(1);
  document.getElementById("title").textContent = title;
  document.title=title.replace(/\s+—.*$/,"")+" · Mino";
  if (view === "overview"){
    // don't rebuild mid-animation or the glowing SVG gets wiped
    if (activeView !== "overview" || !animating){ document.getElementById("view").innerHTML = VIEWS.overview(D); }
  } else if ((view === "memory" || view === "settings" || view === "database" || view === "onboarding") && editing && !subChanged){
    // don't wipe an in-progress edit on the 5s refresh — but DO switch sub-tabs
  } else if (view === "graph" && activeView === "graph" && !subChanged) {
    // don't rebuild graph on 5s refresh — force sim keeps running
  } else {
    editing = false;
    document.getElementById("view").innerHTML = VIEWS[view](D, sub);
  }
  activeView = view; activeSub = sub;
  const responsibilityViews=D.responsibilities||{today:[],work:[]};
  const attention=responsibilityViews.today.filter(x=>x.status==="needs_you"||x.status==="blocked").length;
  const open=responsibilityViews.work.filter(x=>!["verified","stopped"].includes(x.status)).length;
  document.getElementById("n-today").textContent = attention||"";
  document.getElementById("n-work").textContent = open||"";
  document.getElementById("mobile-n-today").textContent = attention||"";
}
let lastFetch = 0, refreshFailed = false;
function tickLive(){
  const age=lastFetch?Math.round((Date.now()-lastFetch)/1000):null;
  const stale=refreshFailed||age==null||age>15;
  const responsibilityError=Boolean(D&&D.responsibilities&&D.responsibilities.error);
  const mcp=D&&D.tools&&D.tools.mcp;
  const degraded=!stale&&!responsibilityError&&mcp&&mcp.configured&&!mcp.live;
  const state=stale?"lost":responsibilityError?"attention":degraded?"degraded":"operational";
  const label={lost:"Connection lost",attention:"Attention",degraded:"Degraded",operational:"Operational"}[state];
  const freshness=age==null?"Waiting for current state":`Updated ${age}s ago`;
  document.body.dataset.health=state;
  document.getElementById("sub").textContent=`${label} · ${freshness}`;
  document.getElementById("health-label").textContent=label;
  document.getElementById("health-panel-label").textContent=label;
  document.getElementById("health-freshness").textContent=freshness;
  const details=document.getElementById("health-details");
  if(details&&D) details.innerHTML=`<div><dt>Provider</dt><dd>${esc(D.active_provider||D.provider||"Unavailable")}</dd></div><div><dt>Responsibilities</dt><dd>${responsibilityError?"Unavailable":"Current"}</dd></div><div><dt>Schedules</dt><dd>${(D.active_tasks||[]).length} active</dd></div><div><dt>MCP</dt><dd>${mcp?(mcp.live?"Connected":mcp.configured?"Unavailable":"Not configured"):"Unknown"}</dd></div>`;
}
async function refresh(){
  try {
    const response=await fetch("/api/data");
    if(!response.ok) throw new Error(`dashboard returned ${response.status}`);
    D = await response.json(); lastFetch = Date.now(); refreshFailed=false;
    render(); tickLive();
    syncLiveView();   // live-update an opened conversation (e.g. new phone messages)
  } catch(e){ refreshFailed=true; tickLive(); }
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
  if (i){ i.placeholder = msg; setTimeout(()=>{ i.placeholder = "Hi Mino! What can you do?"; }, 8000); } };

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

// --- Memory Graph: Canvas force-directed graph ---

function countEdges(facts) {
  let n = 0;
  for (const id in facts) {
    const f = facts[id];
    n += (f.edges || f.Edges || []).length;
  }
  return n;
}

let graphState = null;

const graphNodePalette = ["#4F8BC9", "#7C6FD0", "#39A98A", "#D97757", "#C05B89", "#2F9FB3", "#8A9A4A"];
const graphEpisodePalette = ["#D4A855", "#E18A5B", "#C97A9B"];

function stableGraphColor(id, type) {
  let hash = 0;
  for (let i = 0; i < id.length; i++) hash = ((hash << 5) - hash + id.charCodeAt(i)) | 0;
  const palette = type === "episodic" ? graphEpisodePalette : graphNodePalette;
  return palette[Math.abs(hash) % palette.length];
}

function graphRelationColor(rel) {
  const name = (rel || "").toLowerCase();
  if (name === "supersedes") return "#E26464";
  if (name === "requires" || name === "depends_on") return "#8B78D2";
  if (name === "prefers") return "#D76E9E";
  if (name === "maintains" || name === "calls") return "#4F8BC9";
  if (name === "deployed_on" || name === "located_at") return "#39A98A";
  if (name === "attributed_to") return "#D98A45";
  if (name === "scheduled_at") return "#C49A35";
  if (name === "used_in") return "#77A64A";
  return "#7E8794";
}

function initGraph(raw) {
  const canvas = document.getElementById("graph-canvas");
  if (!canvas) return;
  const viewport = canvas.parentElement;
  const detail = document.getElementById("graph-detail");

  // Parse nodes from index.json facts map
  const factMap = raw.facts || {};
  const nodes = [];
  const nodeMap = {};
  for (const id in factMap) {
    const f = factMap[id];
    const node = {
      id: f.id || id,
      type: f.type || "semantic",
      subject: f.subject || id,
      edges: f.edges || f.Edges || [],
      x: (Math.random() - 0.5) * 200,
      y: (Math.random() - 0.5) * 200,
      vx: 0, vy: 0,
      visible: true,
      color: stableGraphColor(f.id || id, f.type || "semantic"),
    };
    nodes.push(node);
    nodeMap[node.id] = node;
  }

  // Build edge list
  const edges = [];
  const neighbors = {};
  for (const node of nodes) neighbors[node.id] = new Set();
  for (const node of nodes) {
    for (const e of node.edges) {
      const target = typeof e === "string" ? e : (e.target || e.Target || "");
      const rel = typeof e === "string" ? "related_to" : (e.rel || e.Rel || "related_to");
      if (nodeMap[target]) {
        edges.push({ source: node.id, target, rel, color: graphRelationColor(rel), visible: true });
        neighbors[node.id].add(target);
        neighbors[target].add(node.id);
      }
    }
  }

  let hoveredNode = null;
  let hoveredEdge = null;
  let selectedNode = null;
  let dragNode = null;
  let panX = 0, panY = 0, zoom = 1;
  let panning = false, panStart = null;
  let queryActive = false, lastQuery = "";

  // Force simulation params
  const repel = 5000;
  const springLen = 120;
  const springK = 0.02;
  const damp = 0.85;
  const centerGrav = 0.02;

  function resize() {
    canvas.width = viewport.clientWidth || 800;
    canvas.height = viewport.clientHeight || 600;
  }
  resize();
  window.addEventListener("resize", () => { resize(); draw(); });

  const ctx = canvas.getContext("2d");

  function nodeRadius(node) {
    if (node === selectedNode) return 14;
    if (node === hoveredNode) return 10;
    const ec = neighbors[node.id].size;
    return Math.max(3, Math.min(8, 3 + ec * 1.5));
  }

  function nodeOpacity(node) {
    if (selectedNode && node !== selectedNode && !neighbors[selectedNode.id].has(node.id)) return 0.13;
    if (queryActive && !node._highlight && !node._queryNeighbor) return 0.13;
    return 1;
  }

  function draw() {
    const w = canvas.width, h = canvas.height;
    ctx.clearRect(0, 0, w, h);
    ctx.save();
    ctx.translate(w / 2 + panX, h / 2 + panY);
    ctx.scale(zoom, zoom);

    // Draw edges
    for (const e of edges) {
      const src = nodeMap[e.source], tgt = nodeMap[e.target];
      if (!src || !tgt || !src.visible || !tgt.visible) continue;
      const selectedEdge = selectedNode && (e.source === selectedNode.id || e.target === selectedNode.id);
      const queryEdge = queryActive && (src._highlight || tgt._highlight) && (src._queryNeighbor || tgt._queryNeighbor || src._highlight || tgt._highlight);
      const edgeActive = e === hoveredEdge || selectedEdge || queryEdge;
      ctx.beginPath();
      ctx.moveTo(src.x, src.y);
      ctx.lineTo(tgt.x, tgt.y);
      ctx.globalAlpha = selectedNode ? (selectedEdge ? 0.9 : 0.05) : queryActive ? (queryEdge ? 0.78 : 0.05) : (e === hoveredEdge ? 1 : 0.34);
      ctx.strokeStyle = e.color;
      ctx.lineWidth = (edgeActive ? 2 : 0.85) / zoom;
      ctx.stroke();
      ctx.globalAlpha = 1;

      // Relationship text appears only when it is useful.
      if (zoom > 0.35 && (e === hoveredEdge || selectedEdge)) {
        const mx = (src.x + tgt.x) / 2, my = (src.y + tgt.y) / 2;
        const fontSize = Math.max(8, 10 / zoom);
        ctx.font = `600 ${fontSize}px system-ui, sans-serif`;
        const tw = ctx.measureText(e.rel).width;
        const pad = 3 / zoom;
        ctx.fillStyle = "rgba(19,22,28,0.88)";
        ctx.fillRect(mx - tw / 2 - pad, my - fontSize / 2 - pad, tw + pad * 2, fontSize + pad * 2.2);
        ctx.fillStyle = "#fff";
        ctx.fillText(e.rel, mx - tw / 2, my + fontSize / 3);
      }
    }

    // Draw nodes
    for (const node of nodes) {
      if (!node.visible) continue;
      const r = nodeRadius(node);
      ctx.globalAlpha = nodeOpacity(node);
      ctx.beginPath();
      ctx.arc(node.x, node.y, r, 0, Math.PI * 2);
      const color = node.color;
      ctx.fillStyle = node === selectedNode ? "#fff" : color;
      ctx.fill();
      ctx.strokeStyle = color;
      ctx.lineWidth = (node === selectedNode || node === hoveredNode) ? 2.5 / zoom : 1.2 / zoom;
      ctx.stroke();
      ctx.globalAlpha = 1;

      // Highlight ring for search matches
      if (node._highlight) {
        ctx.beginPath();
        ctx.arc(node.x, node.y, r + 4 / zoom, 0, Math.PI * 2);
        ctx.strokeStyle = "#FFD700";
        ctx.lineWidth = 2 / zoom;
        ctx.stroke();
      }
    }

    // Draw subject on hovered/selected node
    if (zoom > 0.4) {
      for (const node of [hoveredNode, selectedNode]) {
        if (!node || !node.visible) continue;
        const fontSize = Math.max(7, 10 / zoom);
        ctx.font = `600 ${fontSize}px system-ui, sans-serif`;
        const label = node.subject.length > 40 ? node.subject.slice(0, 38) + "…" : node.subject;
        const tw = ctx.measureText(label).width;
        const r = nodeRadius(node);
        const pad = 3 / zoom;
        ctx.fillStyle = "rgba(0,0,0,0.85)";
        ctx.fillRect(node.x - tw / 2 - pad, node.y + r + 4 / zoom, tw + pad * 2, fontSize + pad * 2);
        ctx.fillStyle = "#fff";
        ctx.fillText(label, node.x - tw / 2, node.y + r + fontSize + 2 / zoom);
      }
    }

    ctx.restore();
  }

  function simulate() {
    const w = canvas.width, h = canvas.height;

    for (const node of nodes) {
      if (!node.visible || node === dragNode) continue;
      let fx = 0, fy = 0;

      // Repel all-to-all
      for (const other of nodes) {
        if (other === node || !other.visible) continue;
        let dx = node.x - other.x, dy = node.y - other.y;
        const dist = Math.sqrt(dx * dx + dy * dy) + 1;
        const force = repel / (dist * dist);
        fx += (dx / dist) * force;
        fy += (dy / dist) * force;
      }

      // Spring attraction along edges
      for (const e of edges) {
        const other = e.source === node.id ? nodeMap[e.target] : (e.target === node.id ? nodeMap[e.source] : null);
        if (!other || !other.visible) continue;
        let dx = other.x - node.x, dy = other.y - node.y;
        const dist = Math.sqrt(dx * dx + dy * dy) + 1;
        const force = (dist - springLen) * springK;
        fx += (dx / dist) * force;
        fy += (dy / dist) * force;
      }

      // Center gravity — pulls toward world origin (0,0),
      // which draw() translates to canvas center
      fx += -node.x * centerGrav;
      fy += -node.y * centerGrav;

      node.vx = (node.vx + fx) * damp;
      node.vy = (node.vy + fy) * damp;
      node.x += node.vx;
      node.y += node.vy;
    }
  }

  function tick() {
    for (let i = 0; i < 3; i++) simulate(); // run multiple sim steps per frame
    draw();
    if (graphState) requestAnimationFrame(tick);
  }

  // Interaction: screen-to-world helper
  function screenToWorld(ex, ey) {
    const rect = canvas.getBoundingClientRect();
    return {
      x: (ex - rect.left - canvas.width / 2 - panX) / zoom,
      y: (ey - rect.top - canvas.height / 2 - panY) / zoom,
    };
  }

  // Find node at world point
  function nodeAt(wx, wy) {
    for (let i = nodes.length - 1; i >= 0; i--) {
      const n = nodes[i];
      if (!n.visible) continue;
      const dx = wx - n.x, dy = wy - n.y;
      if (dx * dx + dy * dy < (nodeRadius(n) + 5) ** 2) return n;
    }
    return null;
  }

  function edgeAt(wx, wy) {
    let closest = null, best = 7 / zoom;
    for (const e of edges) {
      const a = nodeMap[e.source], b = nodeMap[e.target];
      if (!a || !b || !a.visible || !b.visible) continue;
      const dx = b.x - a.x, dy = b.y - a.y;
      const length2 = dx * dx + dy * dy;
      const t = length2 ? Math.max(0, Math.min(1, ((wx - a.x) * dx + (wy - a.y) * dy) / length2)) : 0;
      const px = a.x + t * dx, py = a.y + t * dy;
      const distance = Math.hypot(wx - px, wy - py);
      if (distance < best) { best = distance; closest = e; }
    }
    return closest;
  }

  canvas.onmousedown = (ev) => {
    const p = screenToWorld(ev.clientX, ev.clientY);
    const hit = nodeAt(p.x, p.y);
    if (hit) {
      dragNode = hit;
      selectedNode = hit;
      showDetail(hit);
    } else {
      panning = true;
      panStart = { x: ev.clientX - panX, y: ev.clientY - panY };
    }
    draw();
  };

  canvas.onmousemove = (ev) => {
    if (dragNode) {
      const p = screenToWorld(ev.clientX, ev.clientY);
      dragNode.x = p.x;
      dragNode.y = p.y;
      dragNode.vx = 0;
      dragNode.vy = 0;
      draw();
      return;
    }
    if (panning && panStart) {
      panX = ev.clientX - panStart.x;
      panY = ev.clientY - panStart.y;
      draw();
      return;
    }
    const p = screenToWorld(ev.clientX, ev.clientY);
    const prev = hoveredNode;
    const previousEdge = hoveredEdge;
    hoveredNode = nodeAt(p.x, p.y);
    hoveredEdge = hoveredNode ? null : edgeAt(p.x, p.y);
    canvas.style.cursor = hoveredNode || hoveredEdge ? "pointer" : "grab";
    if (hoveredNode !== prev || hoveredEdge !== previousEdge) draw();
  };

  canvas.onmouseup = () => {
    dragNode = null;
    panning = false;
    panStart = null;
  };
  canvas.onmouseleave = () => {
    hoveredNode = null;
    hoveredEdge = null;
    dragNode = null;
    panning = false;
    canvas.style.cursor = "grab";
    draw();
  };

  canvas.onwheel = (ev) => {
    ev.preventDefault();
    const delta = ev.deltaY > 0 ? 0.9 : 1.1;
    zoom = Math.max(0.1, Math.min(3, zoom * delta));
    draw();
  };

  canvas.ondblclick = (ev) => {
    const p = screenToWorld(ev.clientX, ev.clientY);
    const hit = nodeAt(p.x, p.y);
    if (hit && hit.edges.length > 0) {
      ev.preventDefault();
      // Expand: traverse one hop, show related nodes
      const related = new Set();
      for (const e of hit.edges) {
        const tid = typeof e === "string" ? e : (e.target || e.Target);
        if (nodeMap[tid] && !nodeMap[tid].visible) {
          nodeMap[tid].visible = true;
          related.add(tid);
        }
      }
      if (related.size > 0) {
        filterGraph(); // re-sync visibility
      }
      draw();
    }
  };

  // Detail panel
  async function showDetail(node) {
    const relationships = edges.filter(e => e.source === node.id || e.target === node.id);
    const edgeList = relationships.map(e => {
      const incoming = e.target === node.id;
      const tid = incoming ? e.source : e.target;
      const rel = e.rel;
      const tgt = nodeMap[tid];
      const label = tgt ? tgt.subject : tid;
      return `<div class="graph-edge-row" onclick="graphState.selectNode('${esc(tid)}')"><i style="background:${esc(e.color)}"></i><span class="edge-rel">${esc(rel)}</span><b>${incoming ? "←" : "→"}</b>${esc(label)}</div>`;
    }).join("");

    detail.innerHTML = `
      <button class="graph-detail-close" onclick="graphState.clearSelection()">×</button>
      <div class="graph-detail-type ${esc(node.type)}"><i style="background:${esc(node.color)}"></i>${esc(node.type)}</div>
      <h3 class="graph-detail-subject">${esc(node.subject)}</h3>
      <div class="graph-detail-body" id="graph-detail-body">Loading…</div>
      <div class="graph-detail-edges"><strong>${relationships.length} relationship${relationships.length!==1?"s":""}</strong>${edgeList || `<span class="graph-no-edges">No relationships yet</span>`}</div>
      <div class="graph-detail-actions">
        <button onclick="graphState.editFact('${esc(node.id)}')">Edit</button>
        <span class="graph-detail-id">${esc(node.id)}</span>
      </div>`;
    detail.classList.add("open");

    // Lazy-fetch body from .md file
    try {
      const r = await fetch(`/memories/${encodeURIComponent(node.id)}.md`);
      const md = await r.text();
      const body = parseFrontMatter(md);
      document.getElementById("graph-detail-body").textContent = body || "(no body)";
    } catch(e) {
      document.getElementById("graph-detail-body").textContent = "(could not load body)";
    }
  }

  function parseFrontMatter(md) {
    if (!md.startsWith("---\n")) return md;
    const end = md.indexOf("\n---", 4);
    if (end < 0) return md;
    return md.slice(end + 5).trim();
  }

  graphState = {
    nodes, nodeMap, edges, neighbors,
    selectNode(id) {
      const n = nodeMap[id];
      if (n) { selectedNode = n; showDetail(n); draw(); }
    },
    clearSelection() {
      selectedNode = null;
      detail.classList.remove("open");
      draw();
    },
    setQuery(q, matches) {
      queryActive = Boolean(q);
      if (q && matches.length && q !== lastQuery) {
        const n = nodeMap[matches[0]];
        panX = -n.x * zoom;
        panY = -n.y * zoom;
      }
      lastQuery = q;
      draw();
    },
    editFact(id) {
      const n = nodeMap[id];
      if (!n) return;
      const bodyEl = document.getElementById("graph-detail-body");
      if (!bodyEl) return;
      const current = bodyEl.textContent;
      bodyEl.innerHTML = `<textarea id="graph-edit-ta" style="width:100%;min-height:80px;font:inherit;padding:6px">${esc(current)}</textarea>
        <button onclick="graphState.saveFact('${esc(id)}')" style="margin-top:4px">Save</button>`;
    },
    async saveFact(id) {
      const ta = document.getElementById("graph-edit-ta");
      if (!ta) return;
      await postJSON("/api/memory", { action: "update_fact", id, content: ta.value });
      document.getElementById("graph-detail-body").textContent = ta.value;
      refresh();
    },
    draw,
  };

  // Start simulation
  requestAnimationFrame(tick);
}

function filterGraph() {
  if (!graphState) return;
  const q = (document.getElementById("graph-search")?.value || "").trim().toLowerCase();
  const terms = q.split(/\s+/).filter(Boolean);
  const sem = document.querySelector(".graph-toggle input[data-type=semantic]")?.checked ?? true;
  const epi = document.querySelector(".graph-toggle input[data-type=episodic]")?.checked ?? true;
  const matches = [];

  for (const n of graphState.nodes) {
    n.visible = !((!sem && n.type === "semantic") || (!epi && n.type === "episodic"));
    n._highlight = false;
    n._queryNeighbor = false;
    const haystack = `${n.subject} ${n.id}`.toLowerCase();
    if (q && n.visible && terms.every(term => haystack.includes(term))) {
      n._highlight = true;
      matches.push(n.id);
    }
  }

  if (q) {
    for (const id of matches) {
      for (const neighborID of graphState.neighbors[id] || []) {
        const neighbor = graphState.nodeMap[neighborID];
        if (neighbor?.visible) neighbor._queryNeighbor = true;
      }
    }
  }

  for (const e of graphState.edges) {
    const src = graphState.nodeMap[e.source], tgt = graphState.nodeMap[e.target];
    e.visible = src && tgt && src.visible && tgt.visible;
  }
  const status = document.getElementById("graph-query-status");
  if (status) status.textContent = q ? `${matches.length} matching memor${matches.length===1?"y":"ies"} · full graph preserved` : "All memories visible";
  graphState.setQuery(q, matches);
}

function clearGraphQuery() {
  const input = document.getElementById("graph-search");
  if (!input) return;
  input.value = "";
  filterGraph();
  input.focus();
}

window.addEventListener("hashchange", render);
const THEME_KEY = "mino-theme";
function applyTheme(mode) {
  const value = ["system", "light", "dark"].includes(mode) ? mode : "system";
  document.documentElement.toggleAttribute("data-theme", value !== "system");
  if (value !== "system") document.documentElement.dataset.theme = value;
  localStorage.setItem(THEME_KEY, value);
  const select = document.getElementById("theme-mode");
  if (select) select.value = value;
}
function initTheme() {
  applyTheme(localStorage.getItem(THEME_KEY) || "system");
  document.getElementById("theme-mode")?.addEventListener("change", event => applyTheme(event.target.value));
}
initTheme();
let orbitNarrow = window.innerWidth < 720;
window.addEventListener("resize", () => {
  const narrow = window.innerWidth < 720;
  if (narrow === orbitNarrow) return;
  orbitNarrow = narrow;
  if (D && activeView === "overview") document.getElementById("view").innerHTML = VIEWS.overview(D);
});
window.__hold = (v)=>{ animating = v; };   // test hook: freeze the diagram
wireDock(); wireOperatorShell(); wireMic();
refresh(); setInterval(refresh, 5000); setInterval(tickLive, 1000);
pollEvents(); setInterval(pollEvents, 450);   // live harness animation
