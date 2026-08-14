let U = null;
let universeState = null;
let universePendingRegion = null;
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
// Presentation branches (issue #182): identity branches, NOT persisted entities.
// The synthetic Mino trunk and branch anchors are derived in memory only and are
// never added to /api/universe, persisted, counted as nodes, or treated as edges.
const UNIVERSE_BRANCHES = ["memories","tools","system","routines","work"];
const UNIVERSE_BRANCH_LABELS = {
  memories:"Memories", tools:"Tools", system:"System", routines:"Routines", work:"Work",
};
const UNIVERSE_BRANCH_COLORS = {
  memories:"#426fbd", tools:"#75818b", system:"#65727d", routines:"#697a43", work:"#c46f31",
};

// universeBranch maps a durable node to its one primary topology branch.
// Rules: explicit node.region first when meaningful, then the kind table;
// unknown kinds land on Work as an inspectable Other — never silently dropped.
function universeBranch(node){
  const regionBranch = {memory:"memories", system:"system", routines:"routines", work:"work", tools:"tools"};
  if(node.kind==="memory") return "memories";
  if(node.kind==="tool") return "tools";
  if(node.kind==="skill") return "system";
  if(node.kind==="playbook"||node.kind==="schedule"||node.kind==="reminder") return "routines";
  if(node.region && regionBranch[node.region]) return regionBranch[node.region];
  if(node.kind==="responsibility"||node.kind==="conversation") return "work";
  if(node.kind==="artifact") return node.region==="system" ? "system" : "work";
  return "work";
}

// universeBranchAnchors returns the deterministic scaffold positions: the
// synthetic Mino trunk left-of-center, identity branches fanning right and
// outward. Pure presentation geometry — not part of the durable graph.
function universeBranchAnchors(){
  return {
    trunk:[.18,.48],
    memories:[.42,.31],
    tools:[.59,.19],
    system:[.66,.40],
    routines:[.61,.62],
    work:[.42,.67],
  };
}

function universeView(snapshot, lens="universe"){
  lens=UNIVERSE_LENSES[lens]?lens:"universe";
  const c=snapshot?.counts||{}, meta=UNIVERSE_LENSES[lens];
  return `<section class="living-field" data-lens="${lens}">
    <header class="field-summary">
      <div><h2>${lens==="universe"?"The whole field":meta.label}</h2><p>${meta.copy}</p></div>
      <dl><div><dt>Memories</dt><dd>${Number(c.memories||0).toLocaleString()}</dd></div><div><dt>Relationships</dt><dd>${Number(c.relationships||0).toLocaleString()}</dd></div><div><dt>Responsibilities</dt><dd>${Number(c.responsibilities||0).toLocaleString()}</dd></div><div><dt>Live</dt><dd id="universe-live-count">${(snapshot?.activity||[]).length}</dd></div></dl>
    </header>
    <div class="field-stage">
      <div class="field-toolbar" aria-label="Living Field controls">
        <label class="field-search"><span class="sr-only">Find anything in Mino</span><input id="universe-search" type="search" placeholder="Find anything…" autocomplete="off"></label>
        <button id="universe-fit" type="button" title="Reset map view">Fit map</button>
        <span class="field-live" id="universe-live"><i></i> Live</span>
      </div>
      <canvas id="universe-canvas" tabindex="0" aria-label="Interactive map of Mino's durable universe. Use search or the accessible index to inspect nodes."></canvas>
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

// universeRegionCenters maps a lens to its scaffold anchor for camera focus.
// Values mirror universeBranchAnchors (trunk serves the Now lens); kept
// self-contained so behavior checks can extract it standalone.
function universeRegionCenters(){
  return {now:[.18,.48], memory:[.42,.31], work:[.42,.67], routines:[.61,.62], system:[.66,.40]};
}
function universeLandmarkCount(nodes,region,visible,currentIDs){
  return nodes.reduce((count,node)=>count+(currentIDs.has(node.id)&&visible(node)&&universeRegion(node)===region?1:0),0);
}
function universeBranchCount(nodes,branch,visible,currentIDs){
  return nodes.reduce((count,node)=>count+(currentIDs.has(node.id)&&visible(node)&&universeBranch(node)===branch?1:0),0);
}
function universeOverviewAttention(nodes){
  const priority={blocked:0,needs_you:1,working:2,waiting:3};
  return new Set(nodes.filter(node=>node.attention).sort((a,b)=>(priority[a.state]??4)-(priority[b.state]??4)||(b._degree||0)-(a._degree||0)||String(a.id).localeCompare(String(b.id))).slice(0,4).map(node=>node.id));
}
function universeLandmarkStyle(zoom){
  const overview=Math.max(0,Math.min(1,(1-zoom)/.35)),detail=Math.max(0,Math.min(1,(zoom-1)/2));
  return {alpha:.94-detail*.54,radius:11+overview*5-detail*2,fontSize:9+overview*1.5-detail};
}
function universeDefaultZoom(width){return width<720?.78:1;}

// --- Branching field layout (issue #182) ---
// Deterministic, topology-led: synthetic trunk → identity branch anchors →
// real-edge BFS trees per branch. Hubs by connectivity, stable ID tie-breaks,
// hash jitter only (no Math.random, no force simulation). Disconnected
// components get seeded non-overlapping slots. Same snapshot = same positions.

function universeAdjacency(nodes,edges){
  const adjacency=Object.fromEntries(nodes.map(node=>[node.id,[]]));
  edges.forEach(edge=>{
    if(adjacency[edge.source]&&adjacency[edge.target]){
      adjacency[edge.source].push(edge.target);
      adjacency[edge.target].push(edge.source);
    }
  });
  return adjacency;
}

function universeLayout(nodes,edges=[]){
  const anchors=universeBranchAnchors();
  const adjacency=universeAdjacency(nodes,edges);
  nodes.forEach(node=>{ node._layoutAnchor=false; node._overviewVisible=false; node._overviewLabel=false; node._primaryParent=null; });
  layoutMemoryBranch(nodes,edges,adjacency,anchors.memories);
  layoutIdentityBranch(nodes,adjacency,"tools",anchors.tools,-1.25,.9,0);
  layoutIdentityBranch(nodes,adjacency,"system",anchors.system,-.35,.8,0);
  layoutIdentityBranch(nodes,adjacency,"routines",anchors.routines,.35,.9,0);
  layoutWorkBranch(nodes,adjacency,anchors.work);
  // Mark nodes for overview visibility: hubs + top N% by degree
  const totalNodes=nodes.length;
  const overviewThreshold=Math.max(0.15, Math.min(0.25, 150/totalNodes)); // Show ~15-25% of nodes
  nodes.forEach(node=>{
    if(node._layoutAnchor) node._overviewVisible=true;
    else if(node._degree >= totalNodes * overviewThreshold) node._overviewVisible=true;
  });
}

// Memory branch: communities are the sub-branches. Reuses the existing
// stable derivation (single-community BFS anchors) with the memories anchor
// as the branch origin. Communities fan outward; leaves ring their hubs.
function layoutMemoryBranch(nodes,edges,adjacency,anchor){
  const memories=nodes.filter(node=>node.kind==="memory"),stored=[...new Set(memories.map(node=>node.community))];
  if(memories.length>24&&stored.length<=1){
    const memoryIDs=new Set(memories.map(node=>node.id));
    const anchorCount=Math.min(16,Math.max(7,Math.round(Math.sqrt(memories.length)*.7)));
    const anchors=[...memories].sort((a,b)=>adjacency[b.id].length-adjacency[a.id].length||a.id.localeCompare(b.id)).slice(0,anchorCount);
    const queue=[];anchors.forEach((node,index)=>{node._layoutCommunity=index;queue.push(node.id);});
    for(let cursor=0;cursor<queue.length;cursor++){
      const id=queue[cursor],community=nodes.find(node=>node.id===id)?._layoutCommunity;
      adjacency[id].sort().forEach(neighbor=>{const node=memories.find(item=>item.id===neighbor);if(node&&node._layoutCommunity==null){node._layoutCommunity=community;queue.push(neighbor);}});
    }
    memories.forEach(node=>{if(node._layoutCommunity==null)node._layoutCommunity=universeHash(node.id)%anchorCount;});
  }else memories.forEach(node=>{node._layoutCommunity=node.community;});
  const memoryPalette=["#84b58e","#a77bd0","#72b6c2","#e8b65d","#e47f72","#88a5d5","#d29cc6"];
  const communities=[...new Set(memories.map(node=>node._layoutCommunity))].sort((a,b)=>String(a).localeCompare(String(b)));
  const communityCenter=new Map(communities.map((id,index)=>{
    const angle=-1.15+index*2.399963, radius=.09+.2*Math.sqrt((index+.5)/Math.max(1,communities.length));
    return [id,[anchor[0]+Math.cos(angle)*radius,anchor[1]+Math.sin(angle)*radius*.72]];
  }));
  const communityHubs=new Map(communities.map(id=>[id,memories.filter(node=>node._layoutCommunity===id).sort((a,b)=>(b._degree||0)-(a._degree||0)||a.id.localeCompare(b.id))[0]?.id]));
  const communitySizes=new Map(communities.map(id=>[id,memories.filter(node=>node._layoutCommunity===id).length]));
  // Overview shows the eight largest communities; the full graph remains
  // available through lenses, zoom, search, the inspector, and the a11y list.
  const rankedCommunities=[...communities].sort((a,b)=>communitySizes.get(b)-communitySizes.get(a)||String(a).localeCompare(String(b)));
  const overviewCommunities=new Set(rankedCommunities.slice(0,8));
  const overviewLabels=new Set(rankedCommunities.slice(0,3));
  const communityColors=new Map(communities.map((id,index)=>[id,memoryPalette[index%memoryPalette.length]]));
  const communityCounts={};
  memories.forEach(node=>{
    const center=communityCenter.get(node._layoutCommunity)||anchor;
    node._communityColor=communityColors.get(node._layoutCommunity)||memoryPalette[0];
    if(node.id===communityHubs.get(node._layoutCommunity)){
      node.x=center[0]; node.y=center[1]; node._layoutAnchor=true; node._overviewCommunity=overviewCommunities.has(node._layoutCommunity); node._overviewVisible=node._overviewCommunity; node._overviewLabel=overviewLabels.has(node._layoutCommunity); node._primaryParent=null;
      return;
    }
    const index=communityCounts[node._layoutCommunity]||0; communityCounts[node._layoutCommunity]=index+1;
    const angle=index*2.399963+universeRand(node.id)*.45;
    const radius=.05*Math.sqrt(universeRand(node.id,1));
    node.x=center[0]+Math.cos(angle)*radius;
    node.y=center[1]+Math.sin(angle)*radius*.78;
    node._overviewCommunity=overviewCommunities.has(node._layoutCommunity);
    node._overviewVisible=node._overviewCommunity&&node.id===communityHubs.get(node._layoutCommunity);
    node._overviewLabel=node._overviewVisible&&overviewLabels.has(node._layoutCommunity);
    node._primaryParent=communityHubs.get(node._layoutCommunity)||null;
  });
}

// Generic identity branch: degree-ranked hubs fan around the branch anchor;
// a BFS over real edges grows the tree downstream. Disconnected nodes attach
// to a hash-chosen hub; isolated components occupy seeded slots so they never
// overlap. baseAngle orients the fan (0 = right, -π/2 = up, π/2 = down).
function layoutIdentityBranch(nodes,adjacency,branch,anchor,baseAngle,fanRange,slot){
  const branchNodes=nodes.filter(node=>universeBranch(node)===branch);
  if(branchNodes.length===0) return;
  const sorted=[...branchNodes].sort((a,b)=>(b._degree||0)-(a._degree||0)||a.id.localeCompare(b.id));
  const hubCount=Math.min(6,Math.max(1,Math.round(Math.sqrt(branchNodes.length)*.6)));
  const hubs=sorted.slice(0,hubCount);
  const hubSet=new Set(hubs.map(node=>node.id));
  hubs.forEach((hub,index)=>{
    const angle=baseAngle+(index-(hubCount-1)/2)*(fanRange/Math.max(1,hubCount-1));
    hub._fanAngle=angle;
    hub.x=anchor[0]+Math.cos(angle)*.07;
    hub.y=anchor[1]+Math.sin(angle)*.07*.8;
    hub._layoutAnchor=true;
    hub._overviewVisible=index<2;
    hub._overviewLabel=false;
    hub._primaryParent=null;
  });
  const depth={},parent={},fanAngle={};
  const queue=[];
  hubs.forEach(hub=>{ depth[hub.id]=0; parent[hub.id]=null; fanAngle[hub.id]=hub._fanAngle; queue.push(hub.id); });
  for(let cursor=0;cursor<queue.length;cursor++){
    const id=queue[cursor], here=depth[id];
    const kids=[...new Set(adjacency[id])].filter(kid=>depth[kid]===undefined&&branchNodes.some(n=>n.id===kid)).sort();
    kids.forEach(kid=>{ depth[kid]=here+1; parent[kid]=id; queue.push(kid); });
  }
  // Unreachable nodes: attach deterministically to a hash hub; isolated
  // components (no real edges at all) get seeded slots beside the branch.
  const reachable=new Set(Object.keys(depth));
  const isolated=[];
  branchNodes.forEach(node=>{
    if(reachable.has(node.id)) return;
    if(adjacency[node.id].length===0){ isolated.push(node); return; }
    const hub=hubs[universeHash(node.id)%hubCount];
    depth[node.id]=1; parent[node.id]=hub.id; reachable.add(node.id);
  });
  branchNodes.forEach(node=>{
    if(!reachable.has(node.id)) return;
    if(hubSet.has(node.id)) return;
    const p=parent[node.id], d=depth[node.id];
    const siblings=queue.filter(oid=>parent[oid]===p&&depth[oid]===d).length||1;
    const index=queue.filter(oid=>parent[oid]===p&&depth[oid]===d&&oid<node.id).length;
    const spread=Math.max(.14,.55/(d*.8+1));
    const angle=fanAngle[p]+(index-(siblings-1)/2)*spread;
    const radius=.055+Math.min(d,6)*.048;
    node.x=anchor[0]+Math.cos(angle)*radius+(universeRand(node.id)-.5)*.018;
    node.y=anchor[1]+Math.sin(angle)*radius*.8+(universeRand(node.id,1)-.5)*.018;
    fanAngle[node.id]=angle;
    node._primaryParent=p;
  });
  isolated.forEach((node,index)=>{
    const block=Math.floor(index/56),within=index%56;
    const row=Math.floor(within/8),col=within%8;
    node.x=anchor[0]+.05+block*.08+col*.035+(universeRand(node.id)-.5)*.02;
    node.y=anchor[1]+.08+row*.04+(universeRand(node.id,1)-.5)*.02;
    node._primaryParent=null;
  });
}

// Work branch: attention-first responsibilities are the hubs (they are what
// Mino is doing), then connectivity. Same branching geometry as the generic
// identity branches, fanning right and downward from the work anchor.
function layoutWorkBranch(nodes,adjacency,anchor){
  const branchNodes=nodes.filter(node=>universeBranch(node)==="work");
  if(branchNodes.length===0) return;
  const sorted=[...branchNodes].sort((a,b)=>(b.attention?1:0)-(a.attention?1:0)||(b._degree||0)-(a._degree||0)||a.id.localeCompare(b.id));
  const hubCount=Math.min(4,Math.max(1,Math.round(Math.sqrt(branchNodes.length)*.5)));
  const hubs=sorted.slice(0,hubCount);
  const hubSet=new Set(hubs.map(node=>node.id));
  hubs.forEach((hub,index)=>{
    const angle=.15+(index-(hubCount-1)/2)*.8;
    hub._fanAngle=angle;
    hub.x=anchor[0]+Math.cos(angle)*.07;
    hub.y=anchor[1]+Math.sin(angle)*.07*.8;
    hub._layoutAnchor=true;
    hub._overviewVisible=index<2;
    hub._overviewLabel=false;
    hub._primaryParent=null;
  });
  const depth={},parent={},fanAngle={};
  const queue=[];
  hubs.forEach(hub=>{ depth[hub.id]=0; parent[hub.id]=null; fanAngle[hub.id]=hub._fanAngle; queue.push(hub.id); });
  for(let cursor=0;cursor<queue.length;cursor++){
    const id=queue[cursor], here=depth[id];
    const kids=[...new Set(adjacency[id])].filter(kid=>depth[kid]===undefined&&branchNodes.some(n=>n.id===kid)).sort();
    kids.forEach(kid=>{ depth[kid]=here+1; parent[kid]=id; queue.push(kid); });
  }
  const reachable=new Set(Object.keys(depth));
  const isolated=[];
  branchNodes.forEach(node=>{
    if(reachable.has(node.id)) return;
    if(adjacency[node.id].length===0){ isolated.push(node); return; }
    const hub=hubs[universeHash(node.id)%hubCount];
    depth[node.id]=1; parent[node.id]=hub.id; reachable.add(node.id);
  });
  branchNodes.forEach(node=>{
    if(!reachable.has(node.id)||hubSet.has(node.id)) return;
    const p=parent[node.id], d=depth[node.id];
    const siblings=queue.filter(oid=>parent[oid]===p&&depth[oid]===d).length||1;
    const index=queue.filter(oid=>parent[oid]===p&&depth[oid]===d&&oid<node.id).length;
    const spread=Math.max(.14,.55/(d*.8+1));
    const angle=fanAngle[p]+(index-(siblings-1)/2)*spread;
    const radius=.055+Math.min(d,6)*.048;
    node.x=anchor[0]+Math.cos(angle)*radius+(universeRand(node.id)-.5)*.018;
    node.y=anchor[1]+Math.sin(angle)*radius*.8+(universeRand(node.id,1)-.5)*.018;
    fanAngle[node.id]=angle;
    node._primaryParent=p;
  });
  isolated.forEach((node,index)=>{
    const block=Math.floor(index/56),within=index%56;
    const row=Math.floor(within/8),col=within%8;
    node.x=anchor[0]+.05+block*.08+col*.035+(universeRand(node.id)-.5)*.02;
    node.y=anchor[1]+.08+row*.04+(universeRand(node.id,1)-.5)*.02;
    node._primaryParent=null;
  });
}

// universePlaceNode positions ONE new node additively: beside its best
// connected existing neighbor, or on a seeded branch growth ring when it has
// none. Existing positions are never touched, so live merges and playback
// never reorganize the field (issue #182 layout contract rule 7).
function universePlaceNode(node,state){
  const anchors=universeBranchAnchors();
  const branch=universeBranch(node);
  const anchor=anchors[branch]||anchors.work;
  let best=null;
  state.edges.forEach(edge=>{
    let other=null;
    if(edge.source===node.id) other=state.nodeMap[edge.target];
    else if(edge.target===node.id) other=state.nodeMap[edge.source];
    if(other&&other.x!==undefined&&(!best||(other._degree||0)>(best._degree||0))) best=other;
  });
  if(best){
    const angle=universeRand(node.id)*Math.PI*2, distance=.028+.018*universeRand(node.id,1);
    node.x=best.x+Math.cos(angle)*distance;
    node.y=best.y+Math.sin(angle)*distance;
    node._primaryParent=best.id;
  }else{
    const k=universeHash(node.id)%64, ring=.08+.03*Math.floor(k/16);
    node.x=anchor[0]+Math.cos((k%16)/16*Math.PI*2)*ring+(universeRand(node.id)-.5)*.02;
    node.y=anchor[1]+Math.sin((k%16)/16*Math.PI*2)*ring*.8+(universeRand(node.id,1)-.5)*.02;
    node._primaryParent=null;
  }
  node._layoutAnchor=false;
}

function universeNodeRadius(node){
  if(node.kind==="memory") return node.state==="episodic"?3.3:2.5;
  if(node.kind==="responsibility") return node._layoutAnchor?6.2:4.8;
  if(node.kind==="schedule"||node.kind==="playbook") return 5;
  if(node.kind==="conversation"||node.kind==="reminder") return 4.3;
  return 3.7;
}
function universeNodeColor(node){
  if(node.kind==="memory") return node._communityColor||"#3f6fba";
  return UNIVERSE_COLORS[node.kind]||"#65727d";
}
function universeDegrees(nodes,edges){
  const degrees=Object.fromEntries(nodes.map(node=>[node.id,0]));
  edges.forEach(edge=>{if(degrees[edge.source]!=null)degrees[edge.source]++;if(degrees[edge.target]!=null)degrees[edge.target]++;});
  return degrees;
}
function universeCenterRegion(state,region){
  const center=universeRegionCenters()[region];if(!state||!center)return;
  state.zoom=state.canvas.clientWidth<720?1.05:1.28;
  state.panX=-(center[0]*state.canvas.clientWidth-state.canvas.clientWidth/2)*state.zoom;
  state.panY=-(center[1]*state.canvas.clientHeight-state.canvas.clientHeight/2)*state.zoom;
}
function focusUniverseRegion(region){
  const state=universeState;if(!state||!universeRegionCenters()[region])return;
  if(state.lens===region){universeCenterRegion(state,region);return;}
  universePendingRegion=region;location.hash="#universe/"+region;
}

function initUniverse(snapshot,lens="universe"){
  const canvas=document.getElementById("universe-canvas");
  if(!canvas||!snapshot) return;
  const nodes=(snapshot.nodes||[]).map(node=>({...node,_time:universeTimeValue(node),_born:0}));
  const nodeMap=Object.fromEntries(nodes.map(node=>[node.id,node]));
  const edges=(snapshot.edges||[]).filter(edge=>nodeMap[edge.source]&&nodeMap[edge.target]);
  const degrees=universeDegrees(nodes,edges);nodes.forEach(node=>node._degree=degrees[node.id]||0);universeLayout(nodes,edges);
  const dated=nodes.map(n=>n._time).filter(Number.isFinite);
  const state={canvas,nodes,nodeMap,edges,lens:UNIVERSE_LENSES[lens]?lens:"universe",selected:null,hovered:null,landmarkBoxes:[],currentNodeIDs:new Set(nodes.map(node=>node.id)),
    query:"",timeline:1,playing:false,playStarted:0,earliest:dated.length?Math.min(...dated):Date.now(),latest:dated.length?Math.max(...dated):Date.now(),
    panX:0,panY:0,zoom:universeDefaultZoom(canvas.clientWidth),activities:[],snapshot,raf:0,pointer:null,degrees};
  universeState=state;
  nodes.forEach(node=>universeKnown.add(node.id));
  if(universePendingRegion===state.lens){universeCenterRegion(state,state.lens);universePendingRegion=null;}

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
  let overviewAttention=new Set();
  // The overview is one authored map, not 1,117 equally loud dots. Keep the
  // full graph in state, search, the inspector, and the accessible index, but
  // reserve overview pixels for branch hubs and owner-attention nodes. Zoom,
  // lenses, search, and selection progressively disclose the local graph.
  const overviewMode=()=>state.lens==="universe"&&!state.query&&state.zoom<1.5;
  const renderable=node=>visible(node)&&(!overviewMode()||node._overviewVisible||overviewAttention.has(node.id)||node===state.hovered||node===state.selected);
  const draw=now=>{
    if(!canvas.isConnected||universeState!==state) return;
    resize();
    const ctx=canvas.getContext("2d"),dpr=canvas.width/canvas.clientWidth;
    ctx.setTransform(dpr,0,0,dpr,0,0);ctx.clearRect(0,0,canvas.clientWidth,canvas.clientHeight);
    ctx.fillStyle="rgba(246,247,244,.78)";ctx.fillRect(0,0,canvas.clientWidth,canvas.clientHeight);
    const incident=state.hovered||state.selected,edgeLayers=[[],[],[]],communities=new Map();
    const overview=overviewMode(),mobileOverview=canvas.clientWidth<720&&overview;
    overviewAttention=universeOverviewAttention(nodes);
    if(overview&&!mobileOverview) drawUniverseBranchLinks(ctx,state,screen,renderable);
    nodes.forEach(node=>{
      if(mobileOverview||node.kind!=="memory"||!renderable(node)) return;
      const key=String(node._layoutCommunity),group=communities.get(key)||[];group.push(screen(node));communities.set(key,group);
    });
    communities.forEach((points,key)=>{
      if(points.length<4) return;
      const center=points.reduce((sum,point)=>({x:sum.x+point.x/points.length,y:sum.y+point.y/points.length}),{x:0,y:0});
      const bounds=points.reduce((box,point)=>({minX:Math.min(box.minX,point.x),maxX:Math.max(box.maxX,point.x),minY:Math.min(box.minY,point.y),maxY:Math.max(box.maxY,point.y)}),{minX:Infinity,maxX:-Infinity,minY:Infinity,maxY:-Infinity});
      const palette=["rgba(63,111,186,.035)","rgba(129,116,183,.035)","rgba(67,140,133,.032)","rgba(208,122,56,.032)"],index=universeHash(key)%palette.length;
      ctx.beginPath();ctx.ellipse(center.x,center.y,Math.max(34,(bounds.maxX-bounds.minX)/2+24),Math.max(24,(bounds.maxY-bounds.minY)/2+18),0,0,Math.PI*2);ctx.fillStyle=palette[index];ctx.fill();ctx.strokeStyle=palette[index].replace(".035", ".11").replace(".032", ".1");ctx.lineWidth=1;ctx.stroke();
      points.forEach(point=>{ctx.beginPath();ctx.moveTo(center.x,center.y);ctx.lineTo(point.x,point.y);ctx.strokeStyle=palette[index].replace(".035", ".18").replace(".032", ".16");ctx.lineWidth=.65;ctx.stroke();});
    });
    state.edges.forEach(edge=>{
      if(mobileOverview) return;
      const a=nodeMap[edge.source],b=nodeMap[edge.target];if(!renderable(a)||!renderable(b)) return;
      const backbone=a._primaryParent===b.id||b._primaryParent===a.id;
      const sameMemory=a.kind==="memory"&&b.kind==="memory"&&a._layoutCommunity===b._layoutCommunity;
      const endpointProminent=a.kind!=="memory"||b.kind!=="memory"||sameMemory;
      const hot=incident&&(a===incident||b===incident);
      if(!hot&&!backbone&&edge.kind==="inferred") return;
      if(!hot&&!backbone&&edge.kind==="explicit"&&!endpointProminent) return;
      if(overview&&!hot&&!backbone) return;
      const layer=hot?2:(backbone&&(edge.kind==="structural"||edge.kind==="explicit")?1:0);edgeLayers[layer].push({edge,a,b,hot,backbone});
    });
    edgeLayers.forEach((layer,index)=>layer.forEach(({edge,a,b,hot,backbone})=>{
      const pa=screen(a),pb=screen(b),focus=focused(a)&&focused(b),sameRegion=universeRegion(a)===universeRegion(b),sameMemory=a.kind==="memory"&&b.kind==="memory"&&a._layoutCommunity===b._layoutCommunity;
      const distance=Math.max(1,Math.hypot(pb.x-pa.x,pb.y-pa.y)),bend=((universeHash(a.id+"|"+b.id)%2000)/1000-.5)*Math.min(70,distance*.2),normal={x:-(pb.y-pa.y)/distance*bend,y:(pb.x-pa.x)/distance*bend};
      ctx.beginPath();ctx.moveTo(pa.x,pa.y);ctx.quadraticCurveTo((pa.x+pb.x)/2+normal.x,(pa.y+pb.y)/2+normal.y,pb.x,pb.y);
      if(hot) {ctx.strokeStyle="rgba(36,98,192,.92)";ctx.lineWidth=2;}
      else if(backbone&&edge.kind==="structural") {ctx.strokeStyle=focus?"rgba(184,105,47,.64)":"rgba(184,105,47,.22)";ctx.lineWidth=1.25;}
      else if(backbone&&edge.kind==="explicit") {ctx.strokeStyle=focus?"rgba(47,101,184,.28)":"rgba(47,101,184,.08)";ctx.lineWidth=.85;}
      else if(edge.kind==="explicit") {ctx.strokeStyle=focus?"rgba(47,101,184,.16)":"rgba(47,101,184,.05)";ctx.lineWidth=.7;}
      else if(edge.kind==="inferred") {ctx.strokeStyle=sameMemory?"rgba(63,111,186,.05)":sameRegion?"rgba(82,104,125,.03)":"rgba(82,104,125,.018)";ctx.lineWidth=.45;}
      else {ctx.strokeStyle=sameMemory?"rgba(63,111,186,.06)":sameRegion?"rgba(82,104,125,.035)":"rgba(82,104,125,.02)";ctx.lineWidth=sameMemory?.5:.38;}
      ctx.setLineDash([]);ctx.stroke();
    }));
    let visibleCount=0,renderedCount=0;
    nodes.forEach(node=>{
      if(!visible(node)) return;visibleCount++;
      if(mobileOverview) return;
      if(!renderable(node)) return;renderedCount++;
      const p=screen(node),isFocused=focused(node),baseRadius=universeNodeRadius(node),r=baseRadius*(node===state.selected?1.55:overview&&(node._overviewVisible||overviewAttention.has(node.id))?1.45:1),color=universeNodeColor(node),degree=state.degrees[node.id]||0,prominent=overview?(node._overviewVisible||overviewAttention.has(node.id)):node.kind!=="memory"||degree>=4;
      const active=state.activities.some(a=>a.nodeID===node.id&&now-a.started<4200),born=node._born&&now-node._born<2600,reduced=matchMedia("(prefers-reduced-motion: reduce)").matches;
      if((active||born||node.attention)&&!reduced){
        const pulse=r+(active||born?5:3)+Math.sin(now/(active||born?260:620))*(active||born?2:.8);ctx.beginPath();ctx.arc(p.x,p.y,pulse,0,Math.PI*2);ctx.strokeStyle=active?"rgba(32,126,105,.42)":born?"rgba(53,104,193,.36)":"rgba(181,60,66,.2)";ctx.lineWidth=1;ctx.stroke();
      }
      ctx.globalAlpha=isFocused?1:.2;ctx.beginPath();ctx.arc(p.x,p.y,r,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();
      if(prominent){ctx.beginPath();ctx.arc(p.x,p.y,r+2.2,0,Math.PI*2);ctx.strokeStyle=`${color}66`;ctx.lineWidth=1;ctx.stroke();}
      if(node.attention){ctx.strokeStyle="#b53c42";ctx.lineWidth=1.5;ctx.stroke();}
      ctx.globalAlpha=1;
      const showLabel=(overview&&(node._overviewLabel||overviewAttention.has(node.id)))||(!overview&&node._layoutAnchor);
      if(showLabel&&!state.hovered&&!state.selected){
        const maxLabel=overview?(canvas.clientWidth<600?18:24):(canvas.clientWidth<600?17:23),label=node.label.length>maxLabel?node.label.slice(0,maxLabel-1)+"…":node.label;
        ctx.font="650 10px ui-sans-serif,system-ui";ctx.fillStyle="#28323a";
        const labelY=canvas.clientWidth<600&&node.x>.45&&node.x<.66?p.y+18:p.y+4;
        if(node.x>.66){ctx.textAlign="right";ctx.fillText(label,p.x-11,labelY);}else if(node.x>.45){ctx.textAlign="center";ctx.fillText(label,p.x,labelY);}else{ctx.textAlign="left";ctx.fillText(label,p.x+11,labelY);}ctx.textAlign="start";
      }
      if(node===state.hovered||node===state.selected){
        ctx.font="600 11px ui-sans-serif,system-ui";const label=node.label.length>42?node.label.slice(0,41)+"…":node.label;
        const width=ctx.measureText(label).width+14;ctx.fillStyle="rgba(250,251,249,.96)";ctx.strokeStyle="rgba(105,115,120,.25)";ctx.lineWidth=1;ctx.beginPath();ctx.roundRect(p.x+10,p.y-13,width,24,6);ctx.fill();ctx.stroke();ctx.fillStyle="#172028";ctx.fillText(label,p.x+17,p.y+3);
      }
    });
    if(mobileOverview) drawMobileUniverseOverview(ctx,state,canvas,visible);
    else drawUniverseScaffold(ctx,state,screen,visible,overview);
    state.activities=state.activities.filter(activity=>now-activity.started<4200);
    state.activities.forEach(activity=>{
      const node=nodeMap[activity.nodeID];if(!node||!visible(node))return;const end=screen(node),age=(now-activity.started)/1800,t=Math.min(1,age),start={x:canvas.clientWidth*.5,y:16};
      const x=start.x+(end.x-start.x)*t,y=start.y+(end.y-start.y)*t;ctx.beginPath();ctx.moveTo(start.x,start.y);ctx.quadraticCurveTo(canvas.clientWidth*.5,end.y*.6,end.x,end.y);ctx.strokeStyle=`rgba(40,104,216,${Math.max(0,.52-age*.16)})`;ctx.lineWidth=1.5;ctx.setLineDash([4,5]);ctx.stroke();ctx.setLineDash([]);ctx.beginPath();ctx.arc(x,y,3,0,Math.PI*2);ctx.fillStyle="#2868d8";ctx.fill();
    });
    if(state.playing){
      state.timeline=Math.min(1,(now-state.playStarted)/24000);syncUniverseTimeline();
      if(state.timeline>=1){state.playing=false;document.getElementById("universe-play").innerHTML='<span aria-hidden="true">↺</span> Replay history';}
    }
    const count=document.getElementById("universe-visible-count");if(count)count.textContent=mobileOverview?`5 branches · ${visibleCount.toLocaleString()} total`:overview?`${renderedCount.toLocaleString()} shown · ${visibleCount.toLocaleString()} total`:`${visibleCount.toLocaleString()} visible`;
    state.raf=requestAnimationFrame(draw);
  };
  state.draw=draw;state.screen=screen;state.visible=visible;

  const pick=(clientX,clientY)=>{
    const rect=canvas.getBoundingClientRect(),x=clientX-rect.left,y=clientY-rect.top,landmark=state.landmarkBoxes.find(box=>x>=box.x&&x<=box.x+box.width&&y>=box.y&&y<=box.y+box.height);
    if(landmark)return {landmark:landmark.region,node:null};
    let hit=null,distance=12;
    nodes.forEach(node=>{if(!visible(node))return;const p=screen(node),d=Math.hypot(p.x-x,p.y-y);if(d<distance){distance=d;hit=node;}});
    return {landmark:null,node:hit};
  };
  canvas.onpointermove=event=>{
    if(state.pointer){state.panX=state.pointer.panX+event.clientX-state.pointer.x;state.panY=state.pointer.panY+event.clientY-state.pointer.y;return;}
    const hit=pick(event.clientX,event.clientY);state.hovered=hit.node;canvas.style.cursor=hit.node||hit.landmark?"pointer":"grab";
  };
  canvas.onpointerdown=event=>{const hit=pick(event.clientX,event.clientY);state.hovered=hit.node;if(!hit.node&&!hit.landmark){state.pointer={x:event.clientX,y:event.clientY,panX:state.panX,panY:state.panY};canvas.setPointerCapture(event.pointerId);}};
  canvas.onpointerup=event=>{if(state.pointer){state.pointer=null;canvas.releasePointerCapture(event.pointerId);return;}const hit=pick(event.clientX,event.clientY);if(hit.landmark)focusUniverseRegion(hit.landmark);else if(hit.node)selectUniverseNode(hit.node.id);};
  canvas.onpointerleave=()=>{state.hovered=null;state.pointer=null;};
  canvas.onwheel=event=>{event.preventDefault();state.zoom=Math.max(.65,Math.min(3,state.zoom*(event.deltaY>0 ? .9 : 1.1)));};
  canvas.onkeydown=event=>{if(event.key==="Escape")selectUniverseNode(null);};
  document.getElementById("universe-search").oninput=event=>{state.query=event.target.value.trim().toLowerCase();};
  document.getElementById("universe-fit").onclick=()=>{state.panX=0;state.panY=0;state.zoom=universeDefaultZoom(canvas.clientWidth);};
  document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);
  document.getElementById("universe-range").oninput=event=>{state.playing=false;state.timeline=Number(event.target.value)/1000;syncUniverseTimeline();};
  document.getElementById("universe-play").onclick=()=>playUniverseHistory();
  document.getElementById("universe-now").onclick=()=>{state.playing=false;state.timeline=1;syncUniverseTimeline();};
  renderUniverseIndex();syncUniverseTimeline();requestAnimationFrame(draw);
}

function drawMobileUniverseOverview(ctx,state,canvas,visible){
  state.landmarkBoxes=[];
  const origin={x:canvas.clientWidth*.18,y:canvas.clientHeight*.49};
  const positions={memories:[.47,.35],tools:[.73,.25],system:[.79,.46],routines:[.73,.64],work:[.45,.64]};
  const screenPoint=point=>({x:canvas.clientWidth*point[0],y:canvas.clientHeight*point[1]});
  const originPoint=screenPoint([.18,.49]);
  const lensOfMobile={memories:"memory",tools:"system",system:"system",routines:"routines",work:"work"};
  ctx.save();
  UNIVERSE_BRANCHES.forEach(branch=>{
    const point=screenPoint(positions[branch]),color=UNIVERSE_BRANCH_COLORS[branch],count=universeBranchCount(state.nodes,branch,visible,state.currentNodeIDs),label=`${UNIVERSE_BRANCH_LABELS[branch].toUpperCase()} · ${count.toLocaleString()}`;
    ctx.beginPath();ctx.moveTo(originPoint.x,originPoint.y);ctx.quadraticCurveTo((originPoint.x+point.x)/2,point.y,point.x,point.y);ctx.strokeStyle=`${color}66`;ctx.lineWidth=1.4;ctx.setLineDash([4,6]);ctx.stroke();ctx.setLineDash([]);
    ctx.beginPath();ctx.arc(point.x,point.y,13,0,Math.PI*2);ctx.fillStyle="rgba(246,247,244,.96)";ctx.fill();ctx.strokeStyle=color;ctx.lineWidth=1.8;ctx.stroke();ctx.beginPath();ctx.arc(point.x,point.y,4,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();
    ctx.font="700 9px ui-monospace,SFMono-Regular,Menlo,monospace";ctx.textAlign="center";ctx.fillStyle=color;ctx.fillText(label,point.x,point.y+27);
    state.landmarkBoxes.push({region:lensOfMobile[branch],x:point.x-26,y:point.y-26,width:52,height:52});
  });
  ctx.beginPath();ctx.arc(origin.x,origin.y,17,0,Math.PI*2);ctx.fillStyle="rgba(246,247,244,.98)";ctx.fill();ctx.strokeStyle="#28323a";ctx.lineWidth=2;ctx.stroke();ctx.beginPath();ctx.arc(origin.x,origin.y,5,0,Math.PI*2);ctx.fillStyle="#28323a";ctx.fill();
  ctx.font="700 9px ui-monospace,SFMono-Regular,Menlo,monospace";ctx.textAlign="center";ctx.fillStyle="#28323a";ctx.fillText("MINO",origin.x,origin.y-25);
  state.landmarkBoxes.push({region:"now",x:origin.x-28,y:origin.y-28,width:56,height:56});
  ctx.restore();
}

function drawUniverseBranchLinks(ctx,state,screen,renderable){
  const anchors=universeBranchAnchors();
  ctx.save();
  UNIVERSE_BRANCHES.forEach(branch=>{
    const anchor=anchors[branch],origin=screen({x:anchor[0],y:anchor[1]}),color=UNIVERSE_BRANCH_COLORS[branch]||"#65727d";
    state.nodes.filter(node=>universeBranch(node)===branch&&node._overviewVisible&&renderable(node)).forEach(node=>{
      const target=screen(node),distance=Math.max(1,Math.hypot(target.x-origin.x,target.y-origin.y));
      ctx.beginPath();ctx.moveTo(origin.x,origin.y);ctx.quadraticCurveTo((origin.x+target.x)/2,(origin.y+target.y)/2-distance*.12,target.x,target.y);
      ctx.strokeStyle=`${color}55`;ctx.lineWidth=1.2;ctx.setLineDash([]);ctx.stroke();
    });
  });
  ctx.restore();
}

// drawUniverseScaffold renders the identity scaffold (issue #182 layer 2):
// the synthetic Mino trunk, presentation branch anchors, and quiet guide
// lines. Scaffold is presentation-only — never in node counts, the a11y
// index, search, or the durable edge array. Lens landmarks remain clickable.
function drawUniverseScaffold(ctx,state,screen,visible,overview=false){
  state.landmarkBoxes=[];
  const anchors=universeBranchAnchors(),style=universeLandmarkStyle(state.zoom),currentIDs=state.currentNodeIDs;
  const trunk={x:anchors.trunk[0],y:anchors.trunk[1]};
  const guidePoints=[];
  UNIVERSE_BRANCHES.forEach(branch=>{const a=anchors[branch];guidePoints.push(a);});
  ctx.save();
  ctx.globalAlpha=overview?.9:style.alpha*.55;
  guidePoints.forEach((a,index)=>{
    const p0=screen({x:trunk.x,y:trunk.y}),p1=screen({x:a[0],y:a[1]}),branch=UNIVERSE_BRANCHES[index];
    ctx.beginPath();ctx.moveTo(p0.x,p0.y);ctx.quadraticCurveTo(p0.x+(p1.x-p0.x)*.4,p1.y,p1.x,p1.y);
    ctx.strokeStyle=overview?`${UNIVERSE_BRANCH_COLORS[branch]}66`:"rgba(40,50,58,.16)";ctx.lineWidth=overview?1.4:1;ctx.setLineDash(overview?[5,7]:[3,6]);ctx.stroke();
  });
  ctx.setLineDash([]);
  const p=screen({x:trunk.x,y:trunk.y});
  ctx.beginPath();ctx.arc(p.x,p.y,style.radius+5,0,Math.PI*2);ctx.strokeStyle="rgba(40,50,58,.28)";ctx.lineWidth=1;ctx.stroke();
  ctx.beginPath();ctx.arc(p.x,p.y,style.radius,0,Math.PI*2);ctx.fillStyle="rgba(246,247,244,.94)";ctx.fill();ctx.strokeStyle="#28323a";ctx.lineWidth=1.6;ctx.stroke();
  ctx.beginPath();ctx.arc(p.x,p.y,3.4,0,Math.PI*2);ctx.fillStyle="#28323a";ctx.fill();
  ctx.font=`700 ${style.fontSize+1}px ui-monospace,SFMono-Regular,Menlo,monospace`;ctx.textAlign="center";ctx.textBaseline="middle";
  ctx.lineWidth=4;ctx.lineJoin="round";ctx.strokeStyle="rgba(246,247,244,.94)";ctx.strokeText("MINO",p.x,p.y-style.radius-11);ctx.fillStyle="#28323a";ctx.fillText("MINO",p.x,p.y-style.radius-11);
  state.landmarkBoxes.push({region:"now",x:p.x-24,y:p.y-24,width:48,height:48});
  const lensOf={memories:"memory",tools:"system",system:"system",routines:"routines",work:"work"};
  const regionColors={memory:"#426fbd",work:"#c46f31",routines:"#697a43",system:"#65727d"};
  UNIVERSE_BRANCHES.forEach(branch=>{
    const a=anchors[branch],lens=lensOf[branch],q=screen({x:a[0],y:a[1]});
    const count=universeBranchCount(state.nodes,branch,visible,currentIDs);
    const label=`${UNIVERSE_BRANCH_LABELS[branch].toUpperCase()} · ${count.toLocaleString()}`;
    const color=UNIVERSE_BRANCH_COLORS[branch]||regionColors[lens]||"#65727d";
    ctx.beginPath();ctx.arc(q.x,q.y,style.radius+4,0,Math.PI*2);ctx.strokeStyle=`${color}${overview?"55":"38"}`;ctx.lineWidth=overview?1.4:1;ctx.stroke();
    ctx.beginPath();ctx.arc(q.x,q.y,style.radius,0,Math.PI*2);ctx.fillStyle="rgba(246,247,244,.94)";ctx.fill();ctx.strokeStyle=color;ctx.lineWidth=overview?1.8:1.4;ctx.stroke();
    ctx.beginPath();ctx.arc(q.x,q.y,overview?4:3,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();
    ctx.font=`700 ${overview?style.fontSize+1:style.fontSize}px ui-monospace,SFMono-Regular,Menlo,monospace`;ctx.textAlign="center";ctx.textBaseline="middle";
    const labelY=q.y+style.radius+13;
    ctx.lineWidth=4;ctx.lineJoin="round";ctx.strokeStyle="rgba(246,247,244,.94)";ctx.strokeText(label,q.x,labelY);ctx.fillStyle=color;ctx.fillText(label,q.x,labelY);
    state.landmarkBoxes.push({region:lens,x:q.x-26,y:q.y-26,width:52,height:52});
  });
  ctx.restore();
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
  state.currentNodeIDs=new Set(incoming.keys());
  const freshNodes=[];
  incoming.forEach((fresh,id)=>{
    const node=state.nodeMap[id];
    if(node){Object.assign(node,fresh);node._time=universeTimeValue(node);}
    else {const added={...fresh,_time:universeTimeValue(fresh),_born:now};state.nodes.push(added);state.nodeMap[id]=added;universeKnown.add(id);freshNodes.push(added);}
  });
  state.edges=(snapshot.edges||[]).filter(edge=>state.nodeMap[edge.source]&&state.nodeMap[edge.target]);
  state.snapshot=snapshot;state.degrees=universeDegrees(state.nodes,state.edges);state.nodes.forEach(node=>node._degree=state.degrees[node.id]||0);
  // Stable positions (issue #182 rule 7): only nodes without a position are
  // placed; existing nodes never move, so polls and playback never reorganize.
  freshNodes.forEach(node=>{ if(node.x===undefined) universePlaceNode(node,state); });
  state.nodes.forEach(node=>{
    if(node.kind==="memory"&&node._layoutCommunity===undefined){
      const neighbors=state.edges.filter(e=>e.source===node.id||e.target===node.id).map(e=>state.nodeMap[e.source===node.id?e.target:e.source]).filter(n=>n.kind==="memory"&&n._layoutCommunity!==undefined);
      node._layoutCommunity=neighbors[0]?neighbors[0]._layoutCommunity:(node.community!==undefined&&node.community!==null?node.community:universeHash(node.id)%16);
      node._communityColor=["#84b58e","#a77bd0","#72b6c2","#e8b65d","#e47f72","#88a5d5","#d29cc6"][universeHash(String(node._layoutCommunity))%7];
    }
  });
  const dated=state.nodes.map(n=>n._time).filter(Number.isFinite);if(dated.length){state.earliest=Math.min(...dated);state.latest=Math.max(...dated);}
  const live=document.getElementById("universe-live-count");if(live)live.textContent=(snapshot.activity||[]).length;
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
