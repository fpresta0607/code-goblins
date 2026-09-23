import { ownsTaskSession } from "./lineageTree.ts";
import type { BoardActivity, Session, Snapshot, Task } from "./types.ts";

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
  const received=effects.find(event=>event.kind==="message"&&event.target===target);
  const created=effects.find(event=>event.kind==="created"&&event.target===target);
  const communication=parent?effects.find(event=>event.kind==="message"&&event.source===parent&&event.target===target):undefined;
  const creation=parent?effects.find(event=>event.kind==="created"&&event.source===parent&&event.target===target):undefined;
  return {received,communication,created,creation};
}

export function safePresentationURL(raw:string):boolean {
  try {
    const url=new URL(raw), host=url.hostname.toLowerCase(), octets=/^\d{1,3}(\.\d{1,3}){3}$/.test(host)?host.split(".").map(Number):[];
    // Plain http only where it never crosses an untrusted network: this
    // machine, or the tailnet, whose traffic Tailscale encrypts.
    const tailnet=host.endsWith(".ts.net")||octets.length===4&&octets[0]===100&&octets[1]>=64&&octets[1]<=127;
    return !url.username&&!url.password&&!url.search&&!url.hash
      &&(url.protocol==="https:"||(url.protocol==="http:"&&(["localhost","127.0.0.1","[::1]"].includes(host)||tailnet)))
      &&!/(token|secret|credential|password|signature|github_pat_|ghp_|api_key|apikey)/i.test(decodeURIComponent(url.pathname));
  } catch { return false; }
}

// A goblin's pane-proven presentation names no session, so it belongs to its
// task's own card: the session that owns the task, or the task card when no
// session does. One that names a session belongs to that session's card.
export function presentationShownOn(presentation:BoardActivity,session:Session|undefined,task:Task|undefined):boolean {
  return presentation.target?presentation.target===session?.id:!!task&&presentation.task_id===task.id&&ownsTaskSession(session,task);
}

export function livePresentations(snapshot:Snapshot,now:number):BoardActivity[] {
  return (snapshot.activity||[]).filter(event=>{
    if(!["browser","review"].includes(event.kind)||event.state!=="active"||!safePresentationURL(event.url)||Date.parse(event.until)<=now||Date.parse(event.at)>now) return false;
    if(event.cfo_identity) return !!event.live;
    // A goblin proves its presentation by its own pane and names no native
    // session, so the task's own runtime evidence decides whether it is live.
    const task=snapshot.tasks.find(t=>t.id===event.task_id),node=event.target?snapshot.sessions.find(n=>n.id===event.target):undefined;
    const runtime=event.target?node?.runtime:task?.runtime;
    return !!task&&task.generation===event.generation&&(!event.target||!!node&&node.generation===event.generation&&node.phase!=="ended")
      &&!!runtime&&["active","busy","idle","working","done"].includes(runtime.state)
      &&now-Date.parse(runtime.at)<120000;
  });
}
