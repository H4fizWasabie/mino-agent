let U = null;
let universeState = null;
const universeKnown = new Set();
const UNIVERSE_COLORS = {
  memory:"#426fbd", responsibility:"#d07a38", playbook:"#697a43", schedule:"#91a14d",
  reminder:"#c44f55", artifact:"#8c5ea9", conversation:"#3f8d87", skill:"#7169a8", tool:"#75818b",
};
const UNIVERSE_LENSES = {
  universe:{label:"Universe",copy:"Everything Mino can truthfully account for."},
  now:{label:"Now",copy:"Current attention, pending reminders, and live work."},
  work:{label:"Work",copy:"Responsibilities, outcomes, artifacts, and conversations."},
  memory:{label:"Memory",copy:"Every authoritative semantic and episodic memory."},
  routines:{label:"Routines",copy:"Playbooks, schedules, and recurring responsibility."},
  system:{label:"System",copy:"Skills, tools, and runtime foundations."},
};

function universeView(snapshot, lens="universe"){
  lens=UNIVERSE_LENSES[lens]?lens:"universe";
  const c=snapshot?.counts||{}, meta=UNIVERSE_LENSES[lens];
  return `<section class="living-field" data-lens="${lens}">
    <header class="field-summary">
      <div><span class="field-kicker">${meta.label} lens</span><h2>${meta.copy}</h2></div>
      <dl><div><dt>Memories</dt><dd>${Number(c.memories||0).toLocaleString()}</dd></div><div><dt>Relationships</dt><dd>${Number(c.relationships||0).toLocaleString()}</dd></div><div><dt>Responsibilities</dt><dd>${Number(c.responsibilities||0).toLocaleString()}</dd></div><div><dt>Live</dt><dd id="universe-live-count">${(snapshot?.activity||[]).length}</dd></div></dl>
    </header>
    <div class="field-stage">
      <div class="field-toolbar" aria-label="Living Field controls">
        <label class="field-search"><span class="sr-only">Find anything in Mino</span><input id="universe-search" type="search" placeholder="Find anything…" autocomplete="off"></label>
        <button id="universe-fit" type="button" title="Reset map view">Fit map</button>
        <span class="field-live" id="universe-live"><i></i> Live</span>
      </div>
      <canvas id="universe-canvas" tabindex="0" aria-label="Interactive map of Mino's durable universe. Use search or the accessible index to inspect nodes."></canvas>
      <div class="field-region-labels" aria-hidden="true"><span data-region="now">Now</span><span data-region="memory">Memory</span><span data-region="work">Work</span><span data-region="routines">Routines</span><span data-region="system">System</span></div>
      <aside class="field-inspector" id="universe-inspector" aria-live="polite">
        <button class="field-inspector-close" id="universe-inspector-close" type="button" aria-label="Close inspector">×</button>
        <span class="field-inspector-kicker">Living Field</span><h3>Select anything</h3><p>Choose a node to inspect what it is, where it came from, and how it connects.</p>
      </aside>
      <div class="field-a11y-index" id="universe-node-list" role="list" aria-label="Universe nodes"></div>
    </div>
    <footer class="field-timeline">
      <button id="universe-play" type="button"><span aria-hidden="true">▶</span> Play history</button>
      <time id="universe-time">Now</time>
      <input id="universe-range" type="range" min="0" max="1000" value="1000" aria-label="Universe history position">
      <button id="universe-now" type="button">Now</button>
      <span id="universe-visible-count">${(snapshot?.nodes||[]).length} visible</span>
    </footer>
  </section>`;
}

function universeHash(text){
  let h=2166136261;
  for(let i=0;i<text.length;i++){ h^=text.charCodeAt(i); h=Math.imul(h,16777619); }
  return h>>>0;
}
function universeRand(id,salt=0){ return (universeHash(id+":"+salt)%10000)/10000; }
function universeTimeValue(node){
  const value=node.at||node.updated_at||"", parsed=Date.parse(value);
  return Number.isFinite(parsed)?parsed:null;
}
function universeRegion(node){
  if(node.kind==="memory") return "memory";
  if(node.kind==="reminder") return "now";
  if(node.kind==="playbook"||node.kind==="schedule") return "routines";
  if(node.kind==="skill"||node.kind==="tool") return "system";
  if(node.kind==="conversation") return "work";
  return node.region||"work";
}
function universeFocus(node,lens){
  if(lens==="universe") return true;
  const region=universeRegion(node);
  if(lens==="now") return node.attention||node.kind==="reminder"||["working","blocked","needs_you"].includes(node.state);
  if(lens==="work") return region==="work"||node.kind==="artifact"||node.kind==="conversation";
  return region===lens;
}
function universeNodeLink(node){
  const raw=node.id.slice(node.id.indexOf(":")+1);
  if(node.kind==="memory") return "#memory/semantic";
  if(node.kind==="responsibility") return "#responsibility/"+encodeURIComponent(raw);
  if(node.kind==="playbook") return "#memory/playbooks";
  if(node.kind==="schedule") return "#system/schedules";
  if(node.kind==="skill") return "#memory/skills";
  if(node.kind==="tool") return "#system/tools";
  if(node.kind==="conversation") return "#conversations";
  if(node.kind==="artifact") return "#system/files";
  return "#system/database-reminders";
}
function universeLayout(nodes,edges=[]){
  const centers={now:[.49,.17],memory:[.48,.52],work:[.73,.29],routines:[.76,.70],system:[.23,.70],conversations:[.25,.27]};
  const memories=nodes.filter(node=>node.kind==="memory"),stored=[...new Set(memories.map(node=>node.community))];
  if(memories.length>24&&stored.length<=1){
    const memoryIDs=new Set(memories.map(node=>node.id)),adjacency=Object.fromEntries(memories.map(node=>[node.id,[]]));
    edges.forEach(edge=>{if(memoryIDs.has(edge.source)&&memoryIDs.has(edge.target)){adjacency[edge.source].push(edge.target);adjacency[edge.target].push(edge.source);}});
    const anchorCount=Math.min(16,Math.max(7,Math.round(Math.sqrt(memories.length)*.7)));
    const anchors=[...memories].sort((a,b)=>adjacency[b.id].length-adjacency[a.id].length||a.id.localeCompare(b.id)).slice(0,anchorCount);
    const queue=[];anchors.forEach((node,index)=>{node._layoutCommunity=index;queue.push(node.id);});
    for(let cursor=0;cursor<queue.length;cursor++){
      const id=queue[cursor],community=nodes.find(node=>node.id===id)?._layoutCommunity;
      adjacency[id].sort().forEach(neighbor=>{const node=memories.find(item=>item.id===neighbor);if(node._layoutCommunity==null){node._layoutCommunity=community;queue.push(neighbor);}});
    }
    memories.forEach(node=>{if(node._layoutCommunity==null)node._layoutCommunity=universeHash(node.id)%anchorCount;});
  }else memories.forEach(node=>{node._layoutCommunity=node.community;});
  const communities=[...new Set(memories.map(node=>node._layoutCommunity))].sort((a,b)=>a-b);
  const communityCenter=new Map(communities.map((id,index)=>{
    const angle=index*2.399963, radius=.07+.27*Math.sqrt((index+.5)/Math.max(1,communities.length));
    return [id,[centers.memory[0]+Math.cos(angle)*radius,centers.memory[1]+Math.sin(angle)*radius*.72]];
  }));
  const regionCounts={};
  const communityCounts={};
  nodes.forEach(node=>{
    const region=universeRegion(node), index=regionCounts[region]||0;
    regionCounts[region]=index+1;
    let center=centers[region]||centers.work, spread=region==="memory"?.055:.115, localIndex=index;
    if(node.kind==="memory"){
      center=communityCenter.get(node._layoutCommunity)||centers.memory;
      localIndex=communityCounts[node._layoutCommunity]||0;communityCounts[node._layoutCommunity]=localIndex+1;
    }
    const angle=localIndex*2.399963+universeRand(node.id)*.45;
    const radius=spread*Math.sqrt(universeRand(node.id,1));
    node.x=center[0]+Math.cos(angle)*radius;
    node.y=center[1]+Math.sin(angle)*radius*.78;
  });
}
function universeNodeRadius(node){
  if(node.kind==="memory") return node.state==="episodic"?2.8:2.2;
  if(node.kind==="responsibility") return 5.1;
  if(node.kind==="schedule"||node.kind==="playbook") return 4.3;
  return 3.5;
}

function initUniverse(snapshot,lens="universe"){
  const canvas=document.getElementById("universe-canvas");
  if(!canvas||!snapshot) return;
  const nodes=(snapshot.nodes||[]).map(node=>({...node,_time:universeTimeValue(node),_born:0}));
  const nodeMap=Object.fromEntries(nodes.map(node=>[node.id,node]));
  const edges=(snapshot.edges||[]).filter(edge=>nodeMap[edge.source]&&nodeMap[edge.target]);
  universeLayout(nodes,edges);
  const dated=nodes.map(n=>n._time).filter(Number.isFinite);
  const state={canvas,nodes,nodeMap,edges,lens:UNIVERSE_LENSES[lens]?lens:"universe",selected:null,hovered:null,
    query:"",timeline:1,playing:false,playStarted:0,earliest:dated.length?Math.min(...dated):Date.now(),latest:dated.length?Math.max(...dated):Date.now(),
    panX:0,panY:0,zoom:1,activities:[],snapshot,raf:0,pointer:null};
  universeState=state;
  nodes.forEach(node=>universeKnown.add(node.id));

  const resize=()=>{
    const rect=canvas.getBoundingClientRect(), dpr=Math.min(2,window.devicePixelRatio||1);
    const width=Math.max(1,Math.round(rect.width*dpr)),height=Math.max(1,Math.round(rect.height*dpr));
    if(canvas.width!==width||canvas.height!==height){canvas.width=width;canvas.height=height;}
  };
  const screen=node=>({x:(node.x*canvas.clientWidth-canvas.clientWidth/2)*state.zoom+canvas.clientWidth/2+state.panX,y:(node.y*canvas.clientHeight-canvas.clientHeight/2)*state.zoom+canvas.clientHeight/2+state.panY});
  const cutoff=()=>state.earliest+(state.latest-state.earliest)*state.timeline;
  const visible=node=>{
    if(node._time!==null&&node._time>cutoff()) return false;
    if(state.query&&!`${node.label} ${node.summary||""} ${node.kind}`.toLowerCase().includes(state.query)) return false;
    return true;
  };
  const focused=node=>universeFocus(node,state.lens);
  const draw=now=>{
    if(!canvas.isConnected||universeState!==state) return;
    resize();
    const ctx=canvas.getContext("2d"),dpr=canvas.width/canvas.clientWidth;
    ctx.setTransform(dpr,0,0,dpr,0,0);ctx.clearRect(0,0,canvas.clientWidth,canvas.clientHeight);
    ctx.fillStyle="rgba(246,247,244,.78)";ctx.fillRect(0,0,canvas.clientWidth,canvas.clientHeight);
    const incident=state.hovered||state.selected;
    state.edges.forEach(edge=>{
      const a=nodeMap[edge.source],b=nodeMap[edge.target];if(!visible(a)||!visible(b)) return;
      const pa=screen(a),pb=screen(b),hot=incident&&(a===incident||b===incident),focus=focused(a)&&focused(b),sameMemory=a.kind==="memory"&&b.kind==="memory"&&a._layoutCommunity===b._layoutCommunity;
      ctx.beginPath();ctx.moveTo(pa.x,pa.y);ctx.lineTo(pb.x,pb.y);
      ctx.strokeStyle=hot?"rgba(42,94,180,.78)":edge.kind==="structural"?"rgba(174,104,52,.4)":sameMemory&&focus?"rgba(52,99,180,.32)":focus?"rgba(52,99,180,.1)":"rgba(82,104,125,.06)";
      ctx.lineWidth=hot?1.8:edge.kind==="structural"?1.15:sameMemory?.82:.65;ctx.stroke();
    });
    let visibleCount=0;
    nodes.forEach(node=>{
      if(!visible(node)) return;visibleCount++;
      const p=screen(node),isFocused=focused(node),r=universeNodeRadius(node)*(node===state.selected?1.55:1),color=UNIVERSE_COLORS[node.kind]||"#65727d";
      const active=state.activities.some(a=>a.nodeID===node.id&&now-a.started<4200),born=node._born&&now-node._born<2600,reduced=matchMedia("(prefers-reduced-motion: reduce)").matches;
      if((active||born||node.attention)&&!reduced){
        const pulse=r+(active||born?5:3)+Math.sin(now/(active||born?260:620))*(active||born?2:.8);ctx.beginPath();ctx.arc(p.x,p.y,pulse,0,Math.PI*2);ctx.strokeStyle=active?"rgba(32,126,105,.42)":born?"rgba(53,104,193,.36)":"rgba(181,60,66,.2)";ctx.lineWidth=1;ctx.stroke();
      }
      ctx.globalAlpha=isFocused?1:.25;ctx.beginPath();ctx.arc(p.x,p.y,r,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();
      if(node.attention){ctx.strokeStyle="#b53c42";ctx.lineWidth=1.5;ctx.stroke();}
      ctx.globalAlpha=1;
      if(node===state.hovered||node===state.selected){
        ctx.font="600 11px ui-sans-serif,system-ui";const label=node.label.length>42?node.label.slice(0,41)+"…":node.label;
        const width=ctx.measureText(label).width+14;ctx.fillStyle="rgba(250,251,249,.96)";ctx.strokeStyle="rgba(105,115,120,.25)";ctx.lineWidth=1;ctx.beginPath();ctx.roundRect(p.x+10,p.y-13,width,24,6);ctx.fill();ctx.stroke();ctx.fillStyle="#172028";ctx.fillText(label,p.x+17,p.y+3);
      }
    });
    state.activities=state.activities.filter(activity=>now-activity.started<4200);
    state.activities.forEach(activity=>{
      const node=nodeMap[activity.nodeID];if(!node||!visible(node))return;const end=screen(node),age=(now-activity.started)/1800,t=Math.min(1,age),start={x:canvas.clientWidth*.5,y:16};
      const x=start.x+(end.x-start.x)*t,y=start.y+(end.y-start.y)*t;ctx.beginPath();ctx.moveTo(start.x,start.y);ctx.quadraticCurveTo(canvas.clientWidth*.5,end.y*.6,end.x,end.y);ctx.strokeStyle=`rgba(34,123,103,${Math.max(0,.38-age*.12)})`;ctx.lineWidth=1.4;ctx.stroke();ctx.beginPath();ctx.arc(x,y,3,0,Math.PI*2);ctx.fillStyle="#267f6b";ctx.fill();
    });
    if(state.playing){
      state.timeline=Math.min(1,(now-state.playStarted)/24000);syncUniverseTimeline();
      if(state.timeline>=1){state.playing=false;document.getElementById("universe-play").innerHTML='<span aria-hidden="true">↺</span> Replay history';}
    }
    const count=document.getElementById("universe-visible-count");if(count)count.textContent=`${visibleCount.toLocaleString()} visible`;
    state.raf=requestAnimationFrame(draw);
  };
  state.draw=draw;state.screen=screen;state.visible=visible;

  canvas.onpointermove=event=>{
    if(state.pointer){state.panX=state.pointer.panX+event.clientX-state.pointer.x;state.panY=state.pointer.panY+event.clientY-state.pointer.y;return;}
    const rect=canvas.getBoundingClientRect(),x=event.clientX-rect.left,y=event.clientY-rect.top;
    let hit=null,distance=12;
    nodes.forEach(node=>{if(!visible(node))return;const p=screen(node),d=Math.hypot(p.x-x,p.y-y);if(d<distance){distance=d;hit=node;}});
    state.hovered=hit;canvas.style.cursor=hit?"pointer":"grab";
  };
  canvas.onpointerdown=event=>{if(!state.hovered){state.pointer={x:event.clientX,y:event.clientY,panX:state.panX,panY:state.panY};canvas.setPointerCapture(event.pointerId);}};
  canvas.onpointerup=event=>{if(state.pointer){state.pointer=null;canvas.releasePointerCapture(event.pointerId);return;} if(state.hovered)selectUniverseNode(state.hovered.id);};
  canvas.onpointerleave=()=>{state.hovered=null;state.pointer=null;};
  canvas.onwheel=event=>{event.preventDefault();state.zoom=Math.max(.65,Math.min(3,state.zoom*(event.deltaY>0 ? .9 : 1.1)));};
  canvas.onkeydown=event=>{if(event.key==="Escape")selectUniverseNode(null);};
  document.getElementById("universe-search").oninput=event=>{state.query=event.target.value.trim().toLowerCase();};
  document.getElementById("universe-fit").onclick=()=>{state.panX=0;state.panY=0;state.zoom=1;};
  document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);
  document.getElementById("universe-range").oninput=event=>{state.playing=false;state.timeline=Number(event.target.value)/1000;syncUniverseTimeline();};
  document.getElementById("universe-play").onclick=()=>playUniverseHistory();
  document.getElementById("universe-now").onclick=()=>{state.playing=false;state.timeline=1;syncUniverseTimeline();};
  renderUniverseIndex();syncUniverseTimeline();requestAnimationFrame(draw);
}

function syncUniverseTimeline(){
  const state=universeState;if(!state)return;
  const range=document.getElementById("universe-range"),label=document.getElementById("universe-time"),live=document.getElementById("universe-live");
  if(range)range.value=Math.round(state.timeline*1000);
  if(label)label.textContent=state.timeline>=1?"Now":new Intl.DateTimeFormat("en-MY",{year:"numeric",month:"short",day:"numeric",timeZone:U?.timezone||"Asia/Kuala_Lumpur"}).format(new Date(state.earliest+(state.latest-state.earliest)*state.timeline));
  if(live)live.classList.toggle("historical",state.timeline<1);
}
function playUniverseHistory(){
  const state=universeState;if(!state)return;
  if(state.playing){state.playing=false;document.getElementById("universe-play").innerHTML='<span aria-hidden="true">▶</span> Resume';return;}
  if(state.timeline>=1)state.timeline=0;
  state.playStarted=performance.now()-state.timeline*24000;state.playing=true;
  document.getElementById("universe-play").innerHTML='<span aria-hidden="true">Ⅱ</span> Pause';syncUniverseTimeline();
}
function renderUniverseIndex(){
  const target=document.getElementById("universe-node-list"),state=universeState;if(!target||!state)return;
  target.innerHTML=state.nodes.map(node=>`<button type="button" role="listitem" onclick="selectUniverseNode(${JSON.stringify(node.id).replace(/"/g,"&quot;")})">${esc(node.kind)}: ${esc(node.label)}</button>`).join("");
}
function selectUniverseNode(id){
  const state=universeState;if(!state)return;state.selected=id?state.nodeMap[id]||null:null;
  const panel=document.getElementById("universe-inspector");if(!panel)return;
  if(!state.selected){panel.classList.remove("open");panel.innerHTML='<button class="field-inspector-close" id="universe-inspector-close" type="button" aria-label="Close inspector">×</button><span class="field-inspector-kicker">Living Field</span><h3>Select anything</h3><p>Choose a node to inspect what it is, where it came from, and how it connects.</p>';document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);return;}
  const node=state.selected,relations=state.edges.filter(edge=>edge.source===node.id||edge.target===node.id),when=node.at||node.updated_at;
  panel.innerHTML=`<button class="field-inspector-close" id="universe-inspector-close" type="button" aria-label="Close inspector">×</button><span class="field-inspector-kicker">${esc(node.kind)} · ${esc(universeRegion(node))}</span><h3>${esc(node.label)}</h3><p>${esc(node.summary||"No additional summary recorded.")}</p><dl>${node.state?`<div><dt>State</dt><dd>${esc(node.state)}</dd></div>`:""}${node.source?`<div><dt>Source</dt><dd>${esc(node.source)}</dd></div>`:""}${when?`<div><dt>Recorded</dt><dd>${esc(new Date(when).toLocaleString("en-MY",{timeZone:U?.timezone||"Asia/Kuala_Lumpur"}))}</dd></div>`:""}<div><dt>Connections</dt><dd>${relations.length}</dd></div></dl>${relations.length?`<div class="field-relations">${relations.slice(0,8).map(edge=>{const other=state.nodeMap[edge.source===node.id?edge.target:edge.source];return `<button type="button" onclick="selectUniverseNode(${JSON.stringify(other.id).replace(/"/g,"&quot;")})"><span>${esc(edge.relation)}</span>${esc(other.label)}</button>`;}).join("")}</div>`:""}<a class="field-open-detail" href="${universeNodeLink(node)}">Open full view <span>→</span></a>`;
  panel.classList.add("open");document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);
}
function universeUpdate(snapshot){
  U=snapshot;const state=universeState;if(!state||!document.getElementById("universe-canvas"))return;
  const incoming=new Map((snapshot.nodes||[]).map(node=>[node.id,node])),now=performance.now();
  incoming.forEach((fresh,id)=>{
    const node=state.nodeMap[id];
    if(node){Object.assign(node,fresh);node._time=universeTimeValue(node);}
    else {const added={...fresh,_time:universeTimeValue(fresh),_born:now};state.nodes.push(added);state.nodeMap[id]=added;universeKnown.add(id);}
  });
  state.edges=(snapshot.edges||[]).filter(edge=>state.nodeMap[edge.source]&&state.nodeMap[edge.target]);
  state.snapshot=snapshot;const dated=state.nodes.map(n=>n._time).filter(Number.isFinite);if(dated.length){state.earliest=Math.min(...dated);state.latest=Math.max(...dated);}
  universeLayout(state.nodes,state.edges);const live=document.getElementById("universe-live-count");if(live)live.textContent=(snapshot.activity||[]).length;
  renderUniverseIndex();
}
function universeActivity(event){
  const state=universeState;if(!state)return;
  let candidates=state.nodes.filter(node=>node.kind==="responsibility"&&["working","needs_you","blocked"].includes(node.state));
  if(event.tool)candidates=state.nodes.filter(node=>node.kind==="tool"&&event.tool.includes(node.label));
  if(event.type==="gate"&&event.decision==="retrieve")candidates=state.nodes.filter(node=>node.kind==="memory");
  const node=candidates[universeHash(String(event.cursor||event.at||Date.now()))%Math.max(1,candidates.length)]||state.nodes[0];
  if(node)state.activities.push({nodeID:node.id,started:performance.now(),type:event.type});
  const labels={turn_start:"Assembling turn",llm:"Thinking",tool:event.tool?`Using ${event.tool}`:"Using a tool",completion:"Verifying",gate:"Checking memory",turn_end:"Turn recorded"};
  document.querySelectorAll(".arch-status").forEach(status=>status.textContent=labels[event.type]||"Active");
}
