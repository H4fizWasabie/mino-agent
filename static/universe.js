let U = null;
let universeState = null;
let universePendingRegion = null;
const universeKnown = new Set();
const UNIVERSE_COLORS = {
  memory:"#426fbd", responsibility:"#b96b46", playbook:"#6f8050", schedule:"#879757",
  reminder:"#c44e67", artifact:"#8b5ec7", file:"#c8872f", conversation:"#298d83", skill:"#7560b3", tool:"#597780",
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
  memories:"#426fbd", tools:"#647d83", system:"#65727d", routines:"#6b7c4b", work:"#ad684c",
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

// Camera-bound branch landmarks and their nearby community territories.
// These unit vectors form a calm pentagon on the front hemisphere at rest.
function universeBranchVectors(){
  return {
    memories:[-.78,-.16,.61], tools:[-.24,-.78,.58], system:[.72,-.36,.59],
    routines:[.74,.36,.57], work:[-.28,.77,.57],
  };
}

function universeView(snapshot, lens="universe"){
  lens=UNIVERSE_LENSES[lens]?lens:"universe";
  const c=snapshot?.counts||{}, meta=UNIVERSE_LENSES[lens];
  return `<section class="living-field" data-lens="${lens}">
    <header class="field-summary">
      <div><h2>${lens==="universe"?"The whole field":meta.label}</h2><p>${meta.copy}</p></div>
      <dl><div><dt>Memories</dt><dd>${Number(c.memories||0).toLocaleString()}</dd></div><div><dt>Relationships</dt><dd>${Number(c.relationships||0).toLocaleString()}</dd></div><div><dt>Files</dt><dd>${Number((c.files||0)+(c.artifacts||0)).toLocaleString()}</dd></div><div><dt>Live</dt><dd id="universe-live-count">${(snapshot?.activity||[]).length}</dd></div></dl>
    </header>
    <div class="field-stage">
      <div class="field-toolbar" aria-label="Living Field controls">
        <div class="field-search"><label><span class="sr-only">Find anything in Mino</span><input id="universe-search" type="search" placeholder="Find anything…" autocomplete="off" aria-controls="universe-search-results" aria-expanded="false" aria-autocomplete="list"></label><div class="field-search-results" id="universe-search-results" role="region" aria-label="Galaxy search results" aria-live="polite" hidden></div></div>
        <button id="universe-fit" type="button" title="Reset map view">Fit map</button>
        <span class="field-live" id="universe-live"><i></i> Live</span><span class="field-renderer" id="universe-renderer" aria-live="polite">Detecting renderer</span>
      </div>
      <canvas id="universe-webgl" aria-hidden="true"></canvas>
      <canvas id="universe-canvas" tabindex="0" title="Left-drag to rotate. When zoomed, Shift-left-drag or right-drag to pan." aria-label="Interactive map of Mino's durable universe. Left-drag to rotate; when zoomed, Shift-left-drag or right-drag to pan. Use search or the accessible index to inspect nodes."></canvas>
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
  if(node.kind==="artifact"||node.kind==="file") return "#system/files";
  return "#system/database-reminders";
}

// universeRegionCenters maps a lens to its scaffold anchor for camera focus.
// Values mirror universeBranchAnchors (trunk serves the Now lens); kept
// self-contained so behavior checks can extract it standalone.
function universeRegionCenters(){
  return {now:[.18,.48], memory:[.42,.31], tools:[.59,.19], work:[.42,.67], routines:[.61,.62], system:[.66,.40]};
}
function universeLandmarkCount(nodes,region,visible,currentIDs){
  return nodes.reduce((count,node)=>count+(currentIDs.has(node.id)&&visible(node)&&universeRegion(node)===region?1:0),0);
}
function universeBranchCount(nodes,branch,visible,currentIDs){
  return nodes.reduce((count,node)=>count+(currentIDs.has(node.id)&&visible(node)&&universeBranch(node)===branch?1:0),0);
}
function universeBranchTotal(snapshot,branch,fallback){
  const counts=snapshot?.counts;if(!counts)return fallback;
  if(branch==="memories")return counts.memories||0;
  if(branch==="tools")return counts.tools||0;
  if(branch==="system")return counts.skills||0;
  if(branch==="routines")return (counts.playbooks||0)+(counts.schedules||0)+(counts.reminders||0);
  return (counts.responsibilities||0)+(counts.conversations||0)+(counts.artifacts||0);
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
function universeDensityLevel(zoom){return zoom>=2.1?2:zoom>=1.5?1:0;}
function universeDetailStyle(zoom){const progress=Math.max(0,Math.min(1,(zoom-4)/12));return {labels:zoom>=16,nodeScale:1+progress*.45};}
function universeCanPan(zoom,width){return zoom>universeDefaultZoom(width)*1.05;}
function universeDragMode(zoom,width,pan=false){return pan&&universeCanPan(zoom,width)?"pan":"rotate";}

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

function universeSpherePoint(center,local,angle){
  const up=Math.abs(center[1])>.85?[1,0,0]:[0,1,0],tangent=[center[1]*up[2]-center[2]*up[1],center[2]*up[0]-center[0]*up[2],center[0]*up[1]-center[1]*up[0]],length=Math.hypot(...tangent),t=tangent.map(value=>value/length),b=[center[1]*t[2]-center[2]*t[1],center[2]*t[0]-center[0]*t[2],center[0]*t[1]-center[1]*t[0]],radial=Math.sqrt(Math.max(0,1-local*local));
  return [center[0]*radial+t[0]*Math.cos(angle)*local+b[0]*Math.sin(angle)*local,center[1]*radial+t[1]*Math.cos(angle)*local+b[1]*Math.sin(angle)*local,center[2]*radial+t[2]*Math.cos(angle)*local+b[2]*Math.sin(angle)*local];
}

function universeLayout(nodes,edges=[]){
  const communities=new Map();
  nodes.forEach(node=>{
    const branch=universeBranch(node),key=String(universeClusterKey(node,branch));
    if(!communities.has(key))communities.set(key,{branch,key,nodes:[]});
    communities.get(key).nodes.push(node);
    node._layoutAnchor=false;node._overviewVisible=false;node._overviewLabel=false;node._primaryParent=null;
  });
  const groups=[...communities.values()].sort((a,b)=>b.nodes.length-a.nodes.length||a.key.localeCompare(b.key));
  const core=groups.filter(group=>group.branch==="memories"),rim=groups.filter(group=>group.branch!=="memories"),palette=["#2f72d0","#18a66c","#d34d59","#7d54c5","#d48b2d","#198e9f","#be5a98","#6f8f32"];
  const place=(group,index,total,onRim,clusterIndex)=>{
    const phase=2.399963*index+(onRim?.35:-.7),distance=onRim?.72+.11*((index%3)/2):(index===0?.08:.13+.43*Math.sqrt(index/Math.max(1,total-1))),cx=Math.cos(phase)*distance,cy=Math.sin(phase)*distance,span=Math.min(onRim?.14:.2,(onRim?.025:.03)+.012*Math.sqrt(group.nodes.length)),community=[...group.nodes].sort((a,b)=>(b._degree||0)-(a._degree||0)||a.id.localeCompare(b.id)),hub=community[0],stride=Math.max(1,Math.ceil(community.length/14));
    community.forEach((node,nodeIndex)=>{
      const local=nodeIndex===0?0:span*Math.sqrt((nodeIndex+.35)/community.length),angle=nodeIndex*2.399963+universeRand(node.id)*.65;
      let gx=cx+Math.cos(angle)*local,gy=cy+Math.sin(angle)*local;const radius=Math.hypot(gx,gy);if(radius>.94){gx*=.94/radius;gy*=.94/radius;}
      node._gx=gx;node._gy=gy;node._gz=(universeRand(node.id,2)-.5)*(onRim?.12:.2);node.x=.5+gx*.38;node.y=.51+gy*.38;node._orbitBranch=group.branch;node._clusterIndex=clusterIndex;node._clusterSize=community.length;node._clusterRadius=span;node._layoutCommunity=group.key;node._primaryParent=nodeIndex===0?null:hub.id;node._layoutAnchor=nodeIndex===0;node._overviewVisible=node.attention||clusterIndex<64&&(nodeIndex===0||nodeIndex%stride===0);node._overviewLabel=nodeIndex===0&&clusterIndex<10;
      if(node.kind==="memory")node._communityColor=palette[clusterIndex%palette.length];
    });
  };
  core.forEach((group,index)=>place(group,index,core.length,false,index));
  rim.forEach((group,index)=>place(group,index,rim.length,true,core.length+index));
}

function universeClusterKey(node,branch){
  if(branch==="memories") return `memory:${node.community??universeHash(node.id)%12}`;
  const label=String(node.label||"").trim().toLowerCase(),source=String(node.source||"").trim().toLowerCase();
  if(branch==="tools"){
    const parts=String(node.label||"").split("_").filter(Boolean);
    return `tool:${parts[3]||parts[2]||source||universeHash(node.id)%8}`;
  }
  if(branch==="routines") return `routine:${source.replace(/^scheduled-/g,"")||label.split(/\s+/)[0]||node.kind}`;
  const run=label.match(/run\s+([a-z0-9-]+)/i),artifact=source.match(/^scheduled-([a-z0-9-]+)/);
  return `work:${run?.[1]||artifact?.[1]||source||node.kind}`;
}

// Memory branch: communities are the sub-branches. Reuses the existing
// stable derivation (single-community BFS anchors) with the memories anchor
// as the branch origin. Communities fan outward; leaves ring their hubs.
function layoutMemoryBranch(nodes,edges,adjacency,anchor,branchVector){
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
    return [id,branchVector?universeSpherePoint(branchVector,Math.sin(.12+.18*Math.sqrt((index+.5)/Math.max(1,communities.length))),angle):[anchor[0]+Math.cos(angle)*radius,anchor[1]+Math.sin(angle)*radius*.72]];
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
      if(branchVector){node._gx=center[0];node._gy=center[1];node._gz=center[2];node.x=.5+center[0]*.38;node.y=.51+center[1]*.38;}else{node.x=center[0];node.y=center[1];}
      node._layoutAnchor=true; node._overviewCommunity=overviewCommunities.has(node._layoutCommunity); node._overviewVisible=node._overviewCommunity; node._overviewLabel=overviewLabels.has(node._layoutCommunity); node._primaryParent=null;
      return;
    }
    const index=communityCounts[node._layoutCommunity]||0; communityCounts[node._layoutCommunity]=index+1;
    const angle=index*2.399963+universeRand(node.id)*.45;
    const radius=.05*Math.sqrt(universeRand(node.id,1));
    if(branchVector){const point=universeSpherePoint(center,.035+.045*Math.sqrt(universeRand(node.id,1)),angle);node._gx=point[0];node._gy=point[1];node._gz=point[2];node.x=.5+point[0]*.38;node.y=.51+point[1]*.38;}else{node.x=center[0]+Math.cos(angle)*radius;node.y=center[1]+Math.sin(angle)*radius*.78;}
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
  const branch=universeBranch(node);
  let best=null;
  state.edges.forEach(edge=>{
    let other=null;
    if(edge.source===node.id) other=state.nodeMap[edge.target];
    else if(edge.target===node.id) other=state.nodeMap[edge.source];
    if(other&&other.x!==undefined&&(!best||(other._degree||0)>(best._degree||0))) best=other;
  });
  const seedAngle=universeRand(node.id)*Math.PI*2,seedRadius=branch==="memories"?.46:.74,center=best&&best._gx!==undefined?[best._gx,best._gy,best._gz]:[Math.cos(seedAngle)*seedRadius,Math.sin(seedAngle)*seedRadius,0],up=Math.abs(center[1])>.85?[1,0,0]:[0,1,0],tangent=[center[1]*up[2]-center[2]*up[1],center[2]*up[0]-center[0]*up[2],center[0]*up[1]-center[1]*up[0]],length=Math.hypot(...tangent),t=tangent.map(value=>value/length),b=[center[1]*t[2]-center[2]*t[1],center[2]*t[0]-center[0]*t[2],center[0]*t[1]-center[1]*t[0]],angle=universeRand(node.id,2)*Math.PI*2,local=best ? .035+.025*universeRand(node.id,1) : .04+.025*universeRand(node.id,1),radial=Math.sqrt(Math.max(0,1-local*local)),vector=[center[0]*radial+t[0]*Math.cos(angle)*local+b[0]*Math.sin(angle)*local,center[1]*radial+t[1]*Math.cos(angle)*local+b[1]*Math.sin(angle)*local,center[2]*radial+t[2]*Math.cos(angle)*local+b[2]*Math.sin(angle)*local];
  node._gx=vector[0];node._gy=vector[1];node._gz=vector[2];node.x=.5+vector[0]*.38;node.y=.51+vector[1]*.38;node._orbitBranch=branch;node._layoutAnchor=false;node._primaryParent=best?.id||null;
}

function universeNodeRadius(node){
  if(node.kind==="memory") return node.state==="episodic"?3.1:2.5;
  if(node.kind==="responsibility") return node._layoutAnchor?5.3:4.2;
  if(node.kind==="schedule"||node.kind==="playbook") return 4.4;
  if(node.kind==="conversation"||node.kind==="reminder") return 4;
  return 3.4;
}
function focusUniverseCamera(node,state){
  if(!node||node._gx===undefined)return;
  const rz=Math.hypot(node._gx,node._gz);
  state.rotY=Math.atan2(-node._gx,node._gz);state.rotX=Math.atan2(node._gy,Math.max(.01,rz));state.zoom=1.22;state.panX=0;state.panY=0;
}
function rotateUniverseCamera(state,pointer,x,y){
  state.rotY=pointer.rotY+(x-pointer.x)*.004;state.rotX=Math.max(-1.1,Math.min(1.1,pointer.rotX+(y-pointer.y)*.004));state.panX=0;state.panY=0;
}

function universeSearchResults(text){
  const results=[];
  String(text||"").split("\n").forEach(line=>{
    const value=line.trim();
    if(!value||value.startsWith("→")||value.startsWith("←"))return;
    const match=value.match(/^(.*?)\s+#\s+([^\s]+)(?:\s|$)/);
    if(match)results.push({id:"memory:"+match[2],label:match[1].replace(/\s+\[archived\]$/,""),archived:value.includes("[archived]")});
  });
  return results.slice(0,10);
}

function universeSetNodeURL(id,push=true){
  const url=new URL(location.href);
  if(id)url.searchParams.set("node",id);else url.searchParams.delete("node");
  history[push?"pushState":"replaceState"](null,"",url.pathname+url.search+url.hash);
}

function universeProjectionMerge(base,detail){
  const nodes=new Map([...(base?.nodes||[]),...(detail?.nodes||[])].map(node=>[node.id,node]));
  const edges=new Map([...(base?.edges||[]),...(detail?.edges||[])].map(edge=>[`${edge.source}\0${edge.target}\0${edge.relation}`,edge]));
  return {...base,...detail,nodes:[...nodes.values()],edges:[...edges.values()]};
}
function universeDetailNeedsRefresh(previous,next,state){return Boolean(previous&&next&&previous!==next&&state?.selected&&state.detailNodes.length);}
function universeEntityResponseCurrent(controller,state){return controller===universeEntityController&&universeState===state&&state.canvas.isConnected;}
function universeMergeProjection(projection){
  const state=universeState;if(!state)return;
  state.detailNodes=projection.nodes||[];state.detailEdges=projection.edges||[];
  universeUpdate(universeProjectionMerge(state.overviewSnapshot||state.snapshot,projection));
}

let universeEntityController=null;
async function openUniverseEntity(id,push=true,focus=true){
  const state=universeState;if(!state||!id||!state.canvas.isConnected)return;
  universeEntityController?.abort();const controller=universeEntityController=new AbortController();
  try{
    const response=await fetch(`/api/universe/projection?scope=entity&id=${encodeURIComponent(id)}`,{signal:controller.signal});
    if(!response.ok)throw new Error(`Galaxy returned ${response.status}`);
    const projection=await response.json();if(!universeEntityResponseCurrent(controller,state))return;universeMergeProjection(projection);
    const node=state.nodeMap[id];if(!node)return;
    if(focus)focusUniverseCamera(node,state);selectUniverseNode(id,push);state.requestDraw?.(240);
    const target=document.getElementById("universe-search-results");if(target)target.hidden=true;
  }catch(error){if(error.name==="AbortError")return;
    const target=document.getElementById("universe-search-results");
    if(target){target.hidden=false;target.innerHTML=`<p role="status">${esc(error.message)}. The Galaxy view was preserved.</p>`;}
  }
}

function renderUniverseSearchResults(results,message=""){
  const target=document.getElementById("universe-search-results"),input=document.getElementById("universe-search");if(!target||!input)return;
  target.hidden=false;input.setAttribute("aria-expanded","true");
  target.innerHTML=results.length?results.map(result=>result.archived?`<a href="#memory/semantic"><span>${esc(result.label)}</span><small>Archived · open Memory</small></a>`:`<button type="button" onclick="openUniverseEntity(${JSON.stringify(result.id).replace(/"/g,"&quot;")})"><span>${esc(result.label)}</span><small>Memory</small></button>`).join(""):`<p role="status">${esc(message||"No memories found. The Galaxy view is unchanged.")}</p>`;
}

let universeSearchTimer=null,universeSearchController=null;
function scheduleUniverseSearch(query){
  clearTimeout(universeSearchTimer);universeSearchController?.abort();
  const input=document.getElementById("universe-search"),target=document.getElementById("universe-search-results");
  if(!query){if(target)target.hidden=true;if(input)input.setAttribute("aria-expanded","false");return;}
  universeSearchTimer=setTimeout(async()=>{
    const controller=universeSearchController=new AbortController();renderUniverseSearchResults([],"Searching memories…");
    try{
      const response=await fetch(`/api/memory/remember?q=${encodeURIComponent(query)}`,{signal:controller.signal});
      if(!response.ok)throw new Error(`Search returned ${response.status}`);
      const text=await response.text();
      if(controller!==universeSearchController||input?.value.trim()!==query)return;
      renderUniverseSearchResults(universeSearchResults(text));
    }catch(error){if(error.name!=="AbortError"&&controller===universeSearchController)renderUniverseSearchResults([],"Search is unavailable. The Galaxy view is unchanged.");}
  },275);
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
  const branch={memory:"memories",system:"system",routines:"routines",work:"work"}[region],vector=universeBranchVectors()[branch],gx=vector?.[0]??0,gy=vector?.[1]??0,gz=vector?.[2]??1;
  state.rotY=Math.atan2(-gx,gz);state.rotX=Math.atan2(gy,Math.hypot(gx,gz));state.zoom=state.canvas.clientWidth<720?1.05:1.22;state.panX=0;state.panY=0;
}
function focusUniverseRegion(region){
  const state=universeState;if(!state||!universeRegionCenters()[region])return;
  const routes={now:"#today",memory:"#memory/overview",tools:"#system/tools",work:"#work",routines:"#system/schedules",system:"#system/overview"};
  if(routes[region])location.hash=routes[region];
}

function universeWebGLColor(hex,alpha=1){
  const value=String(hex||"#65727d").replace("#","");
  const full=value.length===3?value.split("").map(x=>x+x).join(""):value;
  return [parseInt(full.slice(0,2),16)/255,parseInt(full.slice(2,4),16)/255,parseInt(full.slice(4,6),16)/255,alpha];
}

function initUniverseWebGL(canvas){
  if(!canvas)return null;
  const gl=canvas.getContext("webgl2",{alpha:true,antialias:true,preserveDrawingBuffer:false});
  if(!gl)return null;
  const vertex=gl.createShader(gl.VERTEX_SHADER);gl.shaderSource(vertex,`#version 300 es
    layout(location=0) in vec2 corner;
    layout(location=1) in vec2 center;
    layout(location=2) in float radius;
    layout(location=3) in vec4 color;
    uniform vec2 viewport;
    out vec2 local;
    out vec4 tint;
    void main(){
      vec2 px=center+corner*radius;
      vec2 clip=px/viewport*2.0-1.0;
      gl_Position=vec4(clip.x,-clip.y,0.0,1.0);
      local=corner;tint=color;
    }`);gl.compileShader(vertex);
  const fragment=gl.createShader(gl.FRAGMENT_SHADER);gl.shaderSource(fragment,`#version 300 es
    precision highp float;
    in vec2 local;
    in vec4 tint;
    out vec4 outColor;
    void main(){
      float d=length(local);
      float aa=max(fwidth(d)*1.5,0.012);
      float alpha=1.0-smoothstep(1.0-aa,1.0+aa,d);
      if(alpha<=0.0)discard;
      vec3 normal=vec3(local,sqrt(max(0.0,1.0-d*d)));
      vec3 lightDirection=normalize(vec3(-0.55,-0.70,1.0));
      float light=max(dot(normal,lightDirection),0.0);
      vec3 face=tint.rgb*(0.58+0.42*light)+vec3(0.24*pow(light,16.0));
      vec3 edge=mix(face,face*0.68,smoothstep(0.76,0.99,d));
      outColor=vec4(edge,tint.a*alpha);
    }`);gl.compileShader(fragment);
  if(!gl.getShaderParameter(vertex,gl.COMPILE_STATUS)||!gl.getShaderParameter(fragment,gl.COMPILE_STATUS)){gl.deleteShader(vertex);gl.deleteShader(fragment);return null;}
  const program=gl.createProgram();gl.attachShader(program,vertex);gl.attachShader(program,fragment);gl.linkProgram(program);gl.deleteShader(vertex);gl.deleteShader(fragment);
  if(!gl.getProgramParameter(program,gl.LINK_STATUS)){gl.deleteProgram(program);return null;}
  gl.enable(gl.BLEND);gl.blendFunc(gl.SRC_ALPHA,gl.ONE_MINUS_SRC_ALPHA);
  const vao=gl.createVertexArray(),quad=gl.createBuffer(),instances=gl.createBuffer();gl.bindVertexArray(vao);
  gl.bindBuffer(gl.ARRAY_BUFFER,quad);gl.bufferData(gl.ARRAY_BUFFER,new Float32Array([-1,-1,1,-1,-1,1,1,1]),gl.STATIC_DRAW);gl.enableVertexAttribArray(0);gl.vertexAttribPointer(0,2,gl.FLOAT,false,0,0);
  gl.bindBuffer(gl.ARRAY_BUFFER,instances);gl.enableVertexAttribArray(1);gl.vertexAttribPointer(1,2,gl.FLOAT,false,28,0);gl.vertexAttribDivisor(1,1);gl.enableVertexAttribArray(2);gl.vertexAttribPointer(2,1,gl.FLOAT,false,28,8);gl.vertexAttribDivisor(2,1);gl.enableVertexAttribArray(3);gl.vertexAttribPointer(3,4,gl.FLOAT,false,28,12);gl.vertexAttribDivisor(3,1);gl.bindVertexArray(null);
  return {gl,program,vao,instances,viewport:gl.getUniformLocation(program,"viewport"),dpr:1,count:0};
}

function drawUniverseWebGL(renderer,state,screen,renderable,overview){
  const {gl}=renderer,canvas=state.glCanvas,dpr=renderer.dpr=Math.min(2,window.devicePixelRatio||1),rect=canvas.getBoundingClientRect();
  const width=Math.max(1,Math.round(rect.width*dpr)),height=Math.max(1,Math.round(rect.height*dpr));
  if(canvas.width!==width||canvas.height!==height){canvas.width=width;canvas.height=height;}
  const data=[],detail=universeDetailStyle(state.zoom);
  state.nodes.forEach(node=>{if(!renderable(node))return;const point=screen(node),depth=Math.max(0,Math.min(1,(point.depth+1)/2)),base=universeNodeRadius(node),scale=.72+depth*.48,emphasis=node===state.selected?1.6:overview&&(node._layoutAnchor||state.hovered===node)?1.14:1,radius=Math.min(node===state.selected?9:6,base*scale*emphasis)*detail.nodeScale*dpr,alpha=node===state.selected||node===state.hovered?1:.5+depth*.46,color=universeWebGLColor(universeNodeColor(node),alpha);data.push(point.x*dpr,point.y*dpr,radius,...color);});
  gl.viewport(0,0,width,height);gl.clearColor(0,0,0,0);gl.clear(gl.COLOR_BUFFER_BIT);gl.useProgram(renderer.program);gl.uniform2f(renderer.viewport,width,height);gl.bindVertexArray(renderer.vao);gl.bindBuffer(gl.ARRAY_BUFFER,renderer.instances);gl.bufferData(gl.ARRAY_BUFFER,new Float32Array(data),gl.DYNAMIC_DRAW);gl.drawArraysInstanced(gl.TRIANGLE_STRIP,0,4,data.length/7);gl.bindVertexArray(null);renderer.count=data.length/7;
}

function universeViewport(canvas,zoom=1){
  const width=canvas.clientWidth,height=canvas.clientHeight,mobile=width<720,top=mobile?184:122,bottom=mobile?208:170,available=Math.max(240,height-top-bottom),radius=Math.max(110,Math.min(width*.42,available*.5))*zoom;
  return {x:width*.5,y:top+available*.5,radius};
}
function constrainUniversePan(state,panX=state.panX,panY=state.panY){
  const canvas=state.canvas,width=canvas.clientWidth,height=canvas.clientHeight;if(!universeCanPan(state.zoom,width)){state.panX=0;state.panY=0;return;}
  const view=universeViewport(canvas,state.zoom),margin=Math.max(48,Math.min(96,Math.min(width,height)*.12));
  state.panX=Math.max(margin-view.x-view.radius,Math.min(width-margin-view.x+view.radius,panX));state.panY=Math.max(margin-view.y-view.radius,Math.min(height-margin-view.y+view.radius,panY));
}
function panUniverseCamera(state,pointer,x,y){constrainUniversePan(state,pointer.panX+x-pointer.x,pointer.panY+y-pointer.y);}

function initUniverse(snapshot,lens="universe"){
  const canvas=document.getElementById("universe-canvas");
  if(!canvas||!snapshot) return;
  const glCanvas=document.getElementById("universe-webgl"),webgl=initUniverseWebGL(glCanvas);
  if(glCanvas&&!webgl)glCanvas.hidden=true;
  if(glCanvas&&webgl)glCanvas.hidden=false;
  const rendererLabel=document.getElementById("universe-renderer");
  if(rendererLabel)rendererLabel.textContent=webgl?"WebGL2":"2D fallback";
  const nodes=(snapshot.nodes||[]).map(node=>({...node,_time:universeTimeValue(node),_born:0}));
  const nodeMap=Object.fromEntries(nodes.map(node=>[node.id,node]));
  const edges=(snapshot.edges||[]).filter(edge=>nodeMap[edge.source]&&nodeMap[edge.target]);
  const degrees=universeDegrees(nodes,edges);nodes.forEach(node=>node._degree=degrees[node.id]||0);universeLayout(nodes,edges);
  const dated=nodes.map(n=>n._time).filter(Number.isFinite);
  const state={canvas,nodes,nodeMap,edges,lens:UNIVERSE_LENSES[lens]?lens:"universe",selected:null,hovered:null,landmarkBoxes:[],currentNodeIDs:new Set(nodes.map(node=>node.id)),
    query:"",timeline:1,playing:false,playStarted:0,earliest:dated.length?Math.min(...dated):Date.now(),latest:dated.length?Math.max(...dated):Date.now(),
    panX:0,panY:0,zoom:universeDefaultZoom(canvas.clientWidth),densityLevel:universeDensityLevel(universeDefaultZoom(canvas.clientWidth)),rotX:0,rotY:0,activities:[],snapshot,overviewSnapshot:snapshot,raf:0,animateUntil:0,pointer:null,degrees,glCanvas,webgl,detailNodes:[],detailEdges:[]};
  universeState=state;
  nodes.forEach(node=>universeKnown.add(node.id));
  if(universePendingRegion===state.lens){universeCenterRegion(state,state.lens);universePendingRegion=null;}

  const resize=()=>{
    const rect=canvas.getBoundingClientRect(), dpr=Math.min(2,window.devicePixelRatio||1);
    const width=Math.max(1,Math.round(rect.width*dpr)),height=Math.max(1,Math.round(rect.height*dpr));
    if(canvas.width!==width||canvas.height!==height){canvas.width=width;canvas.height=height;}
    constrainUniversePan(state);
  };
  const screen=node=>{
    const gx=node._gx??((node.x??.5)-.5)/.38,gy=node._gy??((node.y??.51)-.51)/.38,gz=node._gz??0,cy=Math.cos(state.rotY),sy=Math.sin(state.rotY),cx=Math.cos(state.rotX),sx=Math.sin(state.rotX),rx=gx*cy+gz*sy,rz=-gx*sy+gz*cy,ry=gy*cx-rz*sx,depth=gy*sx+rz*cx,perspective=1/(1-depth*.32),view=universeViewport(canvas,state.zoom),radius=view.radius*perspective;
    return {x:view.x+rx*radius+state.panX,y:view.y+ry*radius+state.panY,depth};
  };
  const cutoff=()=>state.earliest+(state.latest-state.earliest)*state.timeline;
  const visible=node=>{
    if(node._time!==null&&node._time>cutoff()) return false;
    if(state.query&&!`${node.label} ${node.summary||""} ${node.kind}`.toLowerCase().includes(state.query)) return false;
    return true;
  };
  const focused=node=>universeFocus(node,state.lens);
  let overviewAttention=new Set();
  // The desktop overview is intentionally dense: the transport budget is also
  // the visible budget. Mobile keeps its compact branch map below.
  const overviewMode=()=>state.lens==="universe"&&!state.query&&state.zoom<1.5;
  const renderable=node=>visible(node);
  const draw=now=>{
    if(!canvas.isConnected||universeState!==state) return;
    state.raf=0;
    resize();
    const ctx=canvas.getContext("2d"),dpr=canvas.width/canvas.clientWidth;
    ctx.setTransform(dpr,0,0,dpr,0,0);ctx.clearRect(0,0,canvas.clientWidth,canvas.clientHeight);
    ctx.fillStyle="rgba(246,247,244,.78)";ctx.fillRect(0,0,canvas.clientWidth,canvas.clientHeight);
    const incident=state.hovered||state.selected,edgeLayers=[[],[],[]];
    const overview=overviewMode(),mobileOverview=canvas.clientWidth<300&&overview;
    overviewAttention=universeOverviewAttention(nodes);
    if(!mobileOverview)drawUniverseScaffold(ctx,state,screen,visible,overview);
    if(state.webgl)drawUniverseWebGL(state.webgl,state,screen,mobileOverview?()=>false:renderable,overview);
    state.edges.forEach(edge=>{
      const a=nodeMap[edge.source],b=nodeMap[edge.target];if(!renderable(a)||!renderable(b)) return;
      const memoryDependency=a.kind==="memory"&&b.kind==="memory"&&(edge.kind==="explicit"||edge.kind==="inferred"||!edge.kind);
      if(mobileOverview) return;
      const sameMemory=a.kind==="memory"&&b.kind==="memory"&&a._layoutCommunity===b._layoutCommunity;
      const hot=incident&&(a===incident||b===incident);
      const layer=hot?2:(edge.kind==="explicit"||edge.kind==="structural"||memoryDependency?1:0);edgeLayers[layer].push({edge,a,b,hot,memoryDependency});
    });
    edgeLayers.forEach(layer=>layer.forEach(({edge,a,b,hot,memoryDependency})=>{
      const pa=screen(a),pb=screen(b),sameRegion=universeRegion(a)===universeRegion(b),sameMemory=a.kind==="memory"&&b.kind==="memory"&&a._layoutCommunity===b._layoutCommunity;
      const distance=Math.max(1,Math.hypot(pb.x-pa.x,pb.y-pa.y)),bend=((universeHash(a.id+"|"+b.id)%2000)/1000-.5)*Math.min(70,distance*.2),normal={x:-(pb.y-pa.y)/distance*bend,y:(pb.x-pa.x)/distance*bend};
      ctx.beginPath();ctx.moveTo(pa.x,pa.y);ctx.quadraticCurveTo((pa.x+pb.x)/2+normal.x,(pa.y+pb.y)/2+normal.y,pb.x,pb.y);
      if(hot) {ctx.strokeStyle="rgba(36,98,192,.92)";ctx.lineWidth=2;}
      else if(edge.kind==="structural") {ctx.strokeStyle="rgba(174,104,43,.28)";ctx.lineWidth=.8;}
      else if(edge.kind==="explicit") {ctx.strokeStyle=memoryDependency?"rgba(42,92,168,.25)":"rgba(65,78,88,.22)";ctx.lineWidth=.65;}
      else if(edge.kind==="inferred") {ctx.strokeStyle=sameMemory?universeNodeColor(a):"#607078";ctx.lineWidth=.45;}
      else {ctx.strokeStyle=sameRegion?"#5b6d74":"#738087";ctx.lineWidth=.45;}
      ctx.globalAlpha=hot?1:(edge.kind==="inferred"?.12:.55)*(.45+Math.max(0,Math.min(1,(pa.depth+pb.depth+2)/4))*.55);ctx.setLineDash([]);ctx.stroke();ctx.globalAlpha=1;
    }));
    let visibleCount=0,renderedCount=0;const detailStyle=universeDetailStyle(state.zoom),labelBoxes=[];
    nodes.forEach(node=>{
      if(!visible(node)) return;visibleCount++;
      if(mobileOverview) return;
      if(!renderable(node)) return;renderedCount++;const p=screen(node),color=universeNodeColor(node);
      if(!state.webgl){
        const isFocused=focused(node),baseRadius=universeNodeRadius(node),r=baseRadius*(node===state.selected?1.55:overview&&(node._overviewVisible||overviewAttention.has(node.id))?1.45:1)*detailStyle.nodeScale,degree=state.degrees[node.id]||0,prominent=overview?(node._overviewVisible||overviewAttention.has(node.id)):node.kind!=="memory"||degree>=4;
        const active=state.activities.some(a=>a.nodeID===node.id&&now-a.started<4200),born=node._born&&now-node._born<2600,reduced=matchMedia("(prefers-reduced-motion: reduce)").matches;
        if((active||born||node.attention)&&!reduced){
          const pulse=r+(active||born?5:3)+Math.sin(now/(active||born?260:620))*(active||born?2:.8);ctx.beginPath();ctx.arc(p.x,p.y,pulse,0,Math.PI*2);ctx.strokeStyle=active?"rgba(32,126,105,.42)":born?"rgba(53,104,193,.36)":"rgba(181,60,66,.2)";ctx.lineWidth=1;ctx.stroke();
        }
        ctx.globalAlpha=isFocused?1:.2;ctx.beginPath();ctx.arc(p.x,p.y,r,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();ctx.beginPath();ctx.arc(p.x-r*.28,p.y-r*.32,Math.max(.7,r*.2),0,Math.PI*2);ctx.fillStyle="rgba(255,255,255,.72)";ctx.fill();
        if(prominent){ctx.beginPath();ctx.arc(p.x,p.y,r+2.2,0,Math.PI*2);ctx.strokeStyle=`${color}66`;ctx.lineWidth=1;ctx.stroke();}
        if(node.attention){ctx.strokeStyle="#b53c42";ctx.lineWidth=1.5;ctx.stroke();}
        ctx.globalAlpha=1;
      }
      const showLabel=(overview&&(node._overviewLabel||overviewAttention.has(node.id)))||detailStyle.labels;
      if(showLabel&&(!state.hovered&&!state.selected||detailStyle.labels)&&node!==state.hovered&&node!==state.selected){
        const maxLabel=detailStyle.labels?(canvas.clientWidth<600?26:34):overview?(canvas.clientWidth<600?18:24):(canvas.clientWidth<600?17:23),label=node.label.length>maxLabel?node.label.slice(0,maxLabel-1)+"…":node.label;
        ctx.font=`${detailStyle.labels?600:650} 10px ui-sans-serif,system-ui`;const width=ctx.measureText(label).width,right=detailStyle.labels?p.x>canvas.clientWidth*.72:node.x>.66,center=!detailStyle.labels&&node.x>.45&&!right,labelX=right?p.x-11:center?p.x:p.x+11,labelY=!detailStyle.labels&&canvas.clientWidth<600&&node.x>.45&&node.x<.66?p.y+18:p.y+4,box={x:right?labelX-width:center?labelX-width/2:labelX,y:labelY-10,width,height:13},onCanvas=box.x+box.width>0&&box.x<canvas.clientWidth&&box.y+box.height>0&&box.y<canvas.clientHeight,crowded=detailStyle.labels&&labelBoxes.some(other=>box.x<other.x+other.width+5&&box.x+box.width+5>other.x&&box.y<other.y+other.height+3&&box.y+box.height+3>other.y);
        if(onCanvas&&!crowded){ctx.textAlign=right?"right":center?"center":"left";if(detailStyle.labels){ctx.strokeStyle="rgba(246,247,244,.94)";ctx.lineWidth=3;ctx.lineJoin="round";ctx.strokeText(label,labelX,labelY);labelBoxes.push(box);}ctx.fillStyle="#28323a";ctx.fillText(label,labelX,labelY);ctx.textAlign="start";}
      }
      if(node===state.hovered||node===state.selected){
        ctx.font="600 11px ui-sans-serif,system-ui";const label=node.label.length>42?node.label.slice(0,41)+"…":node.label;
        const width=ctx.measureText(label).width+14;ctx.fillStyle="rgba(250,251,249,.96)";ctx.strokeStyle="rgba(105,115,120,.25)";ctx.lineWidth=1;ctx.beginPath();ctx.roundRect(p.x+10,p.y-13,width,24,6);ctx.fill();ctx.stroke();ctx.fillStyle="#172028";ctx.fillText(label,p.x+17,p.y+3);
      }
    });
    if(mobileOverview) drawMobileUniverseOverview(ctx,state,canvas,visible);
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
    if(state.playing||state.activities.length||now<state.animateUntil)state.raf=requestAnimationFrame(draw);
  };
  state.draw=draw;state.screen=screen;state.visible=visible;state.requestDraw=(duration=0)=>{state.animateUntil=Math.max(state.animateUntil,performance.now()+duration);if(!state.raf)state.raf=requestAnimationFrame(draw);};
  let densityTimer=0,densityController=null;
  const requestDensity=()=>{
    const level=universeDensityLevel(state.zoom);if(level===state.densityLevel)return;
    clearTimeout(densityTimer);densityTimer=setTimeout(async()=>{
      densityController?.abort();const controller=densityController=new AbortController();
      try{
        const response=await fetch(`/api/universe/projection?scope=overview&level=${level}`,{signal:controller.signal});
        if(!response.ok)throw new Error(`Galaxy returned ${response.status}`);
        const projection=await response.json();
        if(controller!==densityController||universeState!==state||universeDensityLevel(state.zoom)!==level)return;
        state.densityLevel=level;universeUpdate(projection);
      }catch(error){if(error.name!=="AbortError"&&controller===densityController)state.requestDraw?.();}
    },120);
  };

  const pick=(clientX,clientY)=>{
    const rect=canvas.getBoundingClientRect(),x=clientX-rect.left,y=clientY-rect.top,landmark=state.landmarkBoxes.find(box=>x>=box.x&&x<=box.x+box.width&&y>=box.y&&y<=box.y+box.height);
    if(landmark)return {landmark:landmark.region,node:null};
    let hit=null,distance=12;
    nodes.forEach(node=>{if(!visible(node))return;const p=screen(node),d=Math.hypot(p.x-x,p.y-y);if(d<distance){distance=d;hit=node;}});
    return {landmark:null,node:hit};
  };
  canvas.onpointermove=event=>{if(state.pointer){if(state.pointer.mode==="pan")panUniverseCamera(state,state.pointer,event.clientX,event.clientY);else rotateUniverseCamera(state,state.pointer,event.clientX,event.clientY);state.requestDraw();return;}const hit=pick(event.clientX,event.clientY);state.hovered=hit.node;canvas.style.cursor=hit.node||hit.landmark?"pointer":"grab";state.requestDraw();};
  canvas.onpointerdown=event=>{const hit=pick(event.clientX,event.clientY),panGesture=event.shiftKey||event.button===2;state.hovered=hit.node;if(panGesture&&!universeCanPan(state.zoom,canvas.clientWidth))return;if(panGesture||!hit.node&&!hit.landmark){state.pointer={x:event.clientX,y:event.clientY,rotX:state.rotX,rotY:state.rotY,panX:state.panX,panY:state.panY,mode:universeDragMode(state.zoom,canvas.clientWidth,panGesture)};canvas.setPointerCapture(event.pointerId);canvas.style.cursor="grabbing";}};
  canvas.onpointerup=event=>{if(state.pointer){state.pointer=null;canvas.releasePointerCapture(event.pointerId);canvas.style.cursor="grab";state.requestDraw();return;}const hit=pick(event.clientX,event.clientY);if(hit.landmark)focusUniverseRegion(hit.landmark);else if(hit.node)openUniverseEntity(hit.node.id,true,false);};
  canvas.onpointerleave=()=>{state.hovered=null;state.pointer=null;state.requestDraw();};
  canvas.oncontextmenu=event=>event.preventDefault();
  canvas.onwheel=event=>{event.preventDefault();state.zoom=Math.max(.65,Math.min(16,state.zoom*(event.deltaY>0 ? .92 : 1.08)));constrainUniversePan(state);requestDensity();state.requestDraw();};
  canvas.onkeydown=event=>{if(event.key==="Escape")selectUniverseNode(null);};
  document.getElementById("universe-search").oninput=event=>scheduleUniverseSearch(event.target.value.trim());
  document.getElementById("universe-search").onkeydown=event=>{if(event.key==="Escape"){event.currentTarget.value="";scheduleUniverseSearch("");canvas.focus();}};
  document.getElementById("universe-fit").onclick=()=>{state.panX=0;state.panY=0;state.rotX=0;state.rotY=0;state.zoom=universeDefaultZoom(canvas.clientWidth);requestDensity();state.requestDraw(180);};
  document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);
  document.getElementById("universe-range").oninput=event=>{state.playing=false;state.timeline=Number(event.target.value)/1000;syncUniverseTimeline();state.requestDraw();};
  document.getElementById("universe-play").onclick=()=>playUniverseHistory();
  document.getElementById("universe-now").onclick=()=>{state.playing=false;state.timeline=1;syncUniverseTimeline();state.requestDraw();};
  renderUniverseIndex();syncUniverseTimeline();state.requestDraw(180);
  const deepLink=new URL(location.href).searchParams.get("node");if(deepLink)setTimeout(()=>openUniverseEntity(deepLink,false),0);
}

function drawMobileUniverseOverview(ctx,state,canvas,visible){
  state.landmarkBoxes=[];
  const origin={x:canvas.clientWidth*.18,y:canvas.clientHeight*.49};
  const positions={memories:[.47,.35],tools:[.73,.25],system:[.79,.46],routines:[.73,.64],work:[.45,.64]};
  const screenPoint=point=>({x:canvas.clientWidth*point[0],y:canvas.clientHeight*point[1]});
  const originPoint=screenPoint([.18,.49]);
  const lensOfMobile={memories:"memory",tools:"tools",system:"system",routines:"routines",work:"work"};
  ctx.save();
  UNIVERSE_BRANCHES.forEach(branch=>{
    const point=screenPoint(positions[branch]),color=UNIVERSE_BRANCH_COLORS[branch],shown=universeBranchCount(state.nodes,branch,visible,state.currentNodeIDs),count=universeBranchTotal(state.overviewSnapshot,branch,shown),label=`${UNIVERSE_BRANCH_LABELS[branch].toUpperCase()} · ${count.toLocaleString()}`;
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
  const vectors=universeBranchVectors();
  ctx.save();
  ctx.globalAlpha=.18;
  UNIVERSE_BRANCHES.forEach(branch=>{
    const color=UNIVERSE_BRANCH_COLORS[branch]||"#65727d";
    const vector=vectors[branch],origin=screen({_gx:vector[0],_gy:vector[1],_gz:vector[2]});
    const anchors=state.nodes.filter(node=>node._orbitBranch===branch&&node._layoutAnchor&&node._clusterSize>1&&renderable(node)),sample=Math.max(1,Math.ceil(anchors.length/48));
    anchors.forEach((node,index)=>{
      if(index%sample)return;
      const target=screen(node),distance=Math.max(1,Math.hypot(target.x-origin.x,target.y-origin.y));
      ctx.beginPath();ctx.moveTo(origin.x,origin.y);ctx.quadraticCurveTo((origin.x+target.x)/2,(origin.y+target.y)/2-distance*.12,target.x,target.y);
      ctx.strokeStyle=color;ctx.lineWidth=.7;ctx.stroke();
    });
  });
  ctx.restore();
}

function drawUniverseCommunitySpokes(ctx,state,screen,renderable,incident){
  const sample=Math.max(1,Math.ceil(state.nodes.length/3200));
  ctx.save();
  state.nodes.forEach(node=>{
    if(!node._primaryParent||universeHash(node.id)%sample||!renderable(node))return;
    const hub=state.nodeMap[node._primaryParent];if(!hub||!renderable(hub))return;
    if(node!==incident&&hub!==incident&&node._layoutCommunity!==incident._layoutCommunity)return;
    const a=screen(hub),b=screen(node),depth=Math.max(0,Math.min(1,(a.depth+b.depth+2)/4));
    ctx.beginPath();ctx.moveTo(a.x,a.y);ctx.lineTo(b.x,b.y);ctx.strokeStyle=universeNodeColor(node);ctx.globalAlpha=.035+depth*.085;ctx.lineWidth=.45;ctx.stroke();
  });
  ctx.restore();
}

function drawUniverseOrbit(ctx,screen,u,v,scale=1){
  let previous=null;
  for(let index=0;index<=96;index++){
    const angle=index/96*Math.PI*2,c=Math.cos(angle)*scale,s=Math.sin(angle)*scale,point=screen({_gx:u[0]*c+v[0]*s,_gy:u[1]*c+v[1]*s,_gz:u[2]*c+v[2]*s});
    if(previous){ctx.beginPath();ctx.moveTo(previous.x,previous.y);ctx.lineTo(point.x,point.y);ctx.strokeStyle="#567064";ctx.globalAlpha=.045+Math.max(0,Math.min(1,(previous.depth+point.depth+2)/4))*.14;ctx.lineWidth=.75;ctx.stroke();}
    previous=point;
  }
  ctx.globalAlpha=1;
}

// drawUniverseScaffold renders the identity scaffold (issue #182 layer 2):
// the synthetic Mino trunk, presentation branch anchors, and quiet guide
// lines. Scaffold is presentation-only — never in node counts, the a11y
// index, search, or the durable edge array. Lens landmarks remain clickable.
function drawUniverseScaffold(ctx,state,screen,visible,overview=false){
  state.landmarkBoxes=[];
  const center=screen({_gx:0,_gy:0,_gz:0}),view=universeViewport(state.canvas,state.zoom),anchors=state.nodes.filter(node=>node._layoutAnchor&&visible(node)&&state.currentNodeIDs.has(node.id)).sort((a,b)=>(b._clusterSize||0)-(a._clusterSize||0)||a.id.localeCompare(b.id));
  ctx.save();
  ctx.beginPath();ctx.arc(center.x,center.y,view.radius*.98,0,Math.PI*2);ctx.fillStyle="rgba(255,255,255,.48)";ctx.fill();ctx.strokeStyle="rgba(62,78,72,.2)";ctx.lineWidth=1;ctx.stroke();
  anchors.slice(0,120).forEach((node,index)=>{
    const point=screen(node),radius=Math.max(8,view.radius*(node._clusterRadius||.025)),color=universeNodeColor(node);
    ctx.beginPath();ctx.arc(point.x,point.y,radius,0,Math.PI*2);ctx.fillStyle=`${color}0c`;ctx.fill();ctx.strokeStyle=`${color}${overview?"55":"38"}`;ctx.lineWidth=index<10?1:.65;ctx.stroke();
    if(index<8){const label=node.community_label||(node.kind==="file"?node.source:node.kind);ctx.font="700 8px ui-monospace,SFMono-Regular,Menlo,monospace";ctx.textAlign="center";ctx.fillStyle="#5d6965";ctx.fillText(String(label).toUpperCase(),point.x,point.y-radius-5);}
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
  state.playStarted=performance.now()-state.timeline*24000;state.playing=true;state.requestDraw?.();
  document.getElementById("universe-play").innerHTML='<span aria-hidden="true">Ⅱ</span> Pause';syncUniverseTimeline();
}
function renderUniverseIndex(){
  const target=document.getElementById("universe-node-list"),state=universeState;if(!target||!state)return;
  target.innerHTML=state.nodes.map(node=>`<button type="button" role="listitem" onclick="openUniverseEntity(${JSON.stringify(node.id).replace(/"/g,"&quot;")})">${esc(node.kind)}: ${esc(node.label)}</button>`).join("");
}
function selectUniverseNode(id,push=true){
  const state=universeState;if(!state)return;state.selected=id?state.nodeMap[id]||null:null;
  universeSetNodeURL(state.selected?.id||null,push);state.requestDraw?.(180);
  const panel=document.getElementById("universe-inspector");if(!panel)return;
  if(!state.selected){panel.classList.remove("open");panel.innerHTML='<button class="field-inspector-close" id="universe-inspector-close" type="button" aria-label="Close inspector">×</button><span class="field-inspector-kicker">Living Field</span><h3>Select anything</h3><p>Choose a node to inspect what it is, where it came from, and how it connects.</p>';document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);return;}
  const node=state.selected,relations=state.edges.filter(edge=>edge.source===node.id||edge.target===node.id),when=node.at||node.updated_at,outsideTimeline=node._time!==null&&node._time>state.earliest+(state.latest-state.earliest)*state.timeline;
  panel.innerHTML=`<button class="field-inspector-close" id="universe-inspector-close" type="button" aria-label="Close inspector">×</button><span class="field-inspector-kicker">${esc(node.kind)} · ${esc(universeRegion(node))}</span><h3>${esc(node.label)}</h3><p>${esc(node.summary||"No additional summary recorded.")}</p>${outsideTimeline?'<p class="field-timeline-note">Outside the current history position. Timeline unchanged.</p>':""}<dl>${node.state?`<div><dt>State</dt><dd>${esc(node.state)}</dd></div>`:""}${node.source?`<div><dt>Source</dt><dd>${esc(node.source)}</dd></div>`:""}${when?`<div><dt>Recorded</dt><dd>${esc(new Date(when).toLocaleString("en-MY",{timeZone:U?.timezone||"Asia/Kuala_Lumpur"}))}</dd></div>`:""}<div><dt>Connections</dt><dd>${relations.length}</dd></div></dl>${relations.length?`<div class="field-relations">${relations.slice(0,8).map(edge=>{const other=state.nodeMap[edge.source===node.id?edge.target:edge.source];return `<button type="button" onclick="selectUniverseNode(${JSON.stringify(other.id).replace(/"/g,"&quot;")})"><span>${esc(edge.relation)}</span>${esc(other.label)}</button>`;}).join("")}</div>`:""}<a class="field-open-detail" href="${universeNodeLink(node)}">Open full view <span>→</span></a>`;
  panel.classList.add("open");document.getElementById("universe-inspector-close").onclick=()=>selectUniverseNode(null);
}
function universeUpdate(snapshot){
  U=snapshot;const state=universeState;if(!state||!document.getElementById("universe-canvas"))return;
  const previousRevision=state.overviewSnapshot?.revision;
  if(snapshot.scope==="overview")state.overviewSnapshot=snapshot;
  if(snapshot.scope==="overview"&&state.detailNodes.length){
    snapshot=universeProjectionMerge(snapshot,{nodes:state.detailNodes,edges:state.detailEdges});
  }
  const incoming=new Map((snapshot.nodes||[]).map(node=>[node.id,node])),now=performance.now();
  if(snapshot.scope)for(let index=state.nodes.length-1;index>=0;index--){
    const node=state.nodes[index];if(incoming.has(node.id))continue;
    state.nodes.splice(index,1);delete state.nodeMap[node.id];
  }
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
  renderUniverseIndex();state.requestDraw?.(freshNodes.length?2600:0);
  if(universeDetailNeedsRefresh(previousRevision,state.overviewSnapshot?.revision,state))queueMicrotask(()=>openUniverseEntity(state.selected.id,false));
}
function universeActivity(event){
  const state=universeState;if(!state)return;
  let candidates=state.nodes.filter(node=>node.kind==="responsibility"&&["working","needs_you","blocked"].includes(node.state));
  if(event.tool)candidates=state.nodes.filter(node=>node.kind==="tool"&&event.tool.includes(node.label));
  if(event.type==="gate"&&event.decision==="retrieve")candidates=state.nodes.filter(node=>node.kind==="memory");
  const node=candidates[universeHash(String(event.cursor||event.at||Date.now()))%Math.max(1,candidates.length)]||state.nodes[0];
  if(node)state.activities.push({nodeID:node.id,started:performance.now(),type:event.type});state.requestDraw?.(4200);
  const labels={turn_start:"Assembling turn",llm:"Thinking",tool:event.tool?`Using ${event.tool}`:"Using a tool",completion:"Verifying",gate:"Checking memory",turn_end:"Turn recorded"};
  document.querySelectorAll(".arch-status").forEach(status=>status.textContent=labels[event.type]||"Active");
}

if(typeof window!=="undefined")window.addEventListener("popstate",()=>{
  const id=new URL(location.href).searchParams.get("node");
  if(id)openUniverseEntity(id,false);else selectUniverseNode(null,false);
});
