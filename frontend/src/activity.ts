import type { BoardActivity, Snapshot } from "./types.ts";

interface ActivityState { instance:string; connected:boolean; seen:Set<string>; effects:BoardActivity[] }
export function activityTransition(prior:ActivityState|null,snapshot:Snapshot,connected:boolean,now:number):ActivityState {
  const events=snapshot.activity || [];
  const seen=new Set(prior?.seen || []);
  const baseline=!prior || !prior.connected || prior.instance!==snapshot.instance || !connected;
  const effects:BoardActivity[]=[];
  for(const event of events) {
    const target=snapshot.sessions.find(s=>s.id===event.target);
    if(!baseline&&!seen.has(event.id)&&event.state==="accepted"&&(event.kind==="message"||event.kind==="created")
      &&now>=Date.parse(event.at)&&now-Date.parse(event.at)<10000&&target
      &&target.task_id===event.task_id&&target.generation===event.generation
      &&(!event.source||(target.parent===event.source&&snapshot.sessions.some(s=>s.id===event.source)))) effects.push(event);
    seen.add(event.id);
  }
  return {instance:snapshot.instance,connected,seen:new Set([...seen].slice(-1024)),effects};
}

// Pending receipts survive ordinary snapshots until the next animation frame.
// Hidden/disconnected views establish a fresh baseline and never replay them.
export class ActivityBuffer {
  private prior: ActivityState|null = null;
  private pending = new Map<string,BoardActivity>();
  update(snapshot:Snapshot, connected:boolean, visible:boolean, now:number) {
    const boundary=!connected||!visible||!!this.prior&&this.prior.instance!==snapshot.instance;
    this.prior=activityTransition(this.prior,snapshot,connected&&visible,now);
    if(boundary) this.pending.clear();
    for(const event of this.prior.effects) this.pending.set(event.id,event);
    return boundary;
  }
  drain() {
    const pending=[...this.pending.values()];
    this.pending.clear();
    return pending;
  }
}

export interface ActivityEffect extends BoardActivity { expires:number }
export function mergeActivityEffects(prior:ActivityEffect[], incoming:BoardActivity[], now:number):ActivityEffect[] {
  return [...prior.filter(event=>event.expires>now&&!incoming.some(next=>next.target===event.target)),...incoming.map(event=>({...event,expires:now+3200}))];
}

export function activityDisplay(effects:BoardActivity[],target:string,parent:string) {
  const received=effects.find(event=>event.target===target);
  const communication=parent?effects.find(event=>event.source===parent&&event.target===target):undefined;
  return {received,communication,created:received?.kind==="created"};
}

export function safePresentationURL(raw:string):boolean {
  try {
    const url=new URL(raw);
    return !url.username&&!url.password&&!url.search&&!url.hash
      &&(url.protocol==="https:"||(url.protocol==="http:"&&["localhost","127.0.0.1","[::1]"].includes(url.hostname)))
      &&!/(token|secret|credential|password|signature|github_pat_|ghp_|api_key|apikey)/i.test(decodeURIComponent(url.pathname));
  } catch { return false; }
}

export function livePresentations(snapshot:Snapshot,now:number):BoardActivity[] {
  return (snapshot.activity||[]).filter(event=>{
    if(!["browser","review"].includes(event.kind)||event.state!=="active"||!safePresentationURL(event.url)||Date.parse(event.until)<=now||Date.parse(event.at)>now) return false;
    if(event.cfo_identity) return !!event.live;
    const task=snapshot.tasks.find(t=>t.id===event.task_id),node=snapshot.sessions.find(n=>n.id===event.target);
    return !!task&&!!node&&task.generation===event.generation&&node.generation===event.generation
      &&node.phase!=="ended"&&!!node.runtime&&["active","busy","idle","working","done"].includes(node.runtime.state)
      &&now-Date.parse(node.runtime.at)<120000;
  });
}
