import test from "node:test";
import assert from "node:assert/strict";
import { ActivityBuffer, activityDisplay, activityTransition, livePresentations, mergeActivityEffects, presentationShownOn, safePresentationURL } from "./activity.ts";
import { parseSnapshot } from "./types.ts";

test("live receipts animate once on actual recursive links, never initial load or reconnect", () => {
  const now=Date.now(), at=new Date(now).toISOString();
  const base=parseSnapshot({healthy:true,instance:"one",sessions:[{id:"cfo",role:"cfo"},{id:"g",parent:"cfo",task_id:"work",generation:"g1"},{id:"child",parent:"g",task_id:"work",generation:"g1"}],tasks:[{id:"work",generation:"g1",session:"g",verified:false}]});
  let state=activityTransition(null,base,true,now);
  assert.equal(state.effects.length,0);
  const active={...base,activity:[{id:"send-1",kind:"message",task_id:"work",generation:"g1",source:"cfo",target:"g",state:"accepted",at,url:"",until:""},{id:"start-child",kind:"created",task_id:"work",generation:"g1",source:"g",target:"child",state:"accepted",at,url:"",until:""}]};
  state=activityTransition(state,active,true,now);
  assert.deepEqual(state.effects.map(e=>[e.source,e.target,e.kind]),[["cfo","g","message"],["g","child","created"]]);
  assert.equal(activityTransition(state,active,true,now).effects.length,0);
  assert.equal(activityTransition(null,active,true,now).effects.length,0);
  state=activityTransition(state,active,false,now);
  assert.equal(activityTransition(state,active,true,now).effects.length,0);
  const wrong={...active,activity:[{...active.activity[0],id:"wrong-parent",source:"child"}]};
  assert.equal(activityTransition(state,wrong,true,now).effects.length,0);
});

test("two snapshots before a frame preserve receipts; hiding cancels pending and returning never replays",()=>{
 const now=Date.now(), base=parseSnapshot({healthy:true,instance:"one",sessions:[{id:"g",task_id:"work",generation:"g1"}]});
 const receipt={id:"receipt-1",kind:"message",task_id:"work",generation:"g1",target:"g",source:"",state:"accepted",at:new Date(now).toISOString(),url:"",until:""};
 const active={...base,activity:[receipt]},buffer=new ActivityBuffer();
 buffer.update(base,true,true,now);buffer.update(active,true,true,now);buffer.update({...active,revision:5},true,true,now);
 assert.deepEqual(buffer.drain().map(e=>e.id),["receipt-1"]);
 buffer.update({...active,revision:6},true,true,now);assert.equal(buffer.drain().length,0);
 const next={...active,activity:[{...receipt,id:"receipt-2"}]};
 buffer.update(next,true,true,now);buffer.update(next,true,false,now);assert.equal(buffer.drain().length,0);
 buffer.update(next,true,true,now+2000);assert.equal(buffer.drain().length,0);
 buffer.update({...active,activity:[{...receipt,id:"receipt-3"}]},true,true,now+2100);
 assert.equal(buffer.update({...active,instance:"replacement"},true,true,now+2200),true);
 assert.equal(buffer.drain().length,0);
});

test("new receipts never extend another card or connector deadline",()=>{
 const a={id:"activity-a",kind:"message",task_id:"work",generation:"g1",source:"cfo",target:"a",state:"accepted",at:"",url:"",until:""};
 let effects=mergeActivityEffects([], [a], 0);
 for(let now=1000;now<=4000;now+=1000) effects=mergeActivityEffects(effects,[{...a,id:"b-"+now,target:"b"}],now);
 assert.equal(effects.some(event=>event.id===a.id),false);
 assert.equal(effects.length,1);
 assert.equal(effects[0].expires,7200);
});

test("compact activity displays only truthful connector motion and created entrances",()=>{
 const effects=[
  {id:"message",kind:"message",task_id:"work",generation:"g1",source:"parent",target:"child",state:"accepted",at:"",url:"",until:""},
  {id:"created",kind:"created",task_id:"work",generation:"g1",source:"child",target:"nested",state:"accepted",at:"",url:"",until:""},
 ];
 assert.deepEqual(activityDisplay(effects,"child","parent"),{received:effects[0],communication:effects[0],created:undefined,creation:undefined});
 assert.deepEqual(activityDisplay(effects,"nested","child"),{received:undefined,communication:undefined,created:effects[1],creation:effects[1]});
 const unlinked=activityDisplay(effects,"nested","different");
 assert.equal(unlinked.communication,undefined);
 assert.equal(unlinked.creation,undefined);
 assert.equal(unlinked.created,effects[1]);
});

test("verified primary presentation needs no task and disappears with native evidence",()=>{
 const now=Date.now(), snapshot=parseSnapshot({healthy:true,activity:[{id:"primary-review",kind:"review",cfo_identity:"exact-primary",target:"primary-cfo",state:"active",live:true,url:"http://localhost:4387/session/review",at:new Date(now).toISOString(),until:new Date(now+60000).toISOString()}]});
 assert.equal(livePresentations(snapshot,now).length,1);
 assert.equal(livePresentations({...snapshot,activity:[{...snapshot.activity![0],live:false}]},now).length,0);
});

test("browser notice and indicator expire, reject unsafe links and require current liveness", () => {
  const now=Date.now();
  const snapshot=parseSnapshot({healthy:true,activity:[{id:"present-1",kind:"browser",task_id:"work",generation:"g1",target:"g",state:"active",url:"http://127.0.0.1:5173/",at:new Date(now).toISOString(),until:new Date(now+60000).toISOString()}],tasks:[{id:"work",generation:"g1",session:"g",verified:false}],sessions:[{id:"g",task_id:"work",generation:"g1",runtime:{state:"busy",at:new Date(now).toISOString()}}]});
  assert.equal(livePresentations(snapshot,now).length,1);
  assert.equal(livePresentations(snapshot,now+60001).length,0);
  assert.equal(livePresentations({...snapshot,sessions:[{...snapshot.sessions[0],runtime:{state:"unavailable",reason:"",at:""}}]},now).length,0);
  assert.equal(livePresentations({...snapshot,activity:[{...snapshot.activity![0],url:"javascript:alert(1)"}]},now).length,0);
});

test("a goblin's pane-proven presentation names no session and lives on its task's runtime", () => {
  const now=Date.now();
  const snapshot=parseSnapshot({healthy:true,activity:[{id:"task-2-review",kind:"review",task_id:"work",generation:"g7",target:"",state:"active",url:"http://127.0.0.1:4387/session/abc",at:new Date(now).toISOString(),until:new Date(now+60000).toISOString()}],tasks:[{id:"work",generation:"g7",verified:false,runtime:{state:"idle",at:new Date(now).toISOString()}}],sessions:[]});
  assert.equal(livePresentations(snapshot,now).length,1);
  assert.equal(livePresentations({...snapshot,tasks:[{...snapshot.tasks[0],generation:"g8"}]},now).length,0);
  assert.equal(livePresentations({...snapshot,tasks:[{...snapshot.tasks[0],runtime:{state:"stale",reason:"",at:new Date(now).toISOString()}}]},now).length,0);
  assert.equal(livePresentations(snapshot,now+120001).length,0);
});

test("a goblin's review page stays live while its question waits on the Overlord", () => {
  const now=Date.now();
  const snapshot=parseSnapshot({healthy:true,activity:[{id:"task-2-review",kind:"review",task_id:"work",generation:"g7",target:"",state:"active",url:"http://127.0.0.1:4387/session/abc",at:new Date(now).toISOString(),until:new Date(now+60000).toISOString()}],tasks:[{id:"work",generation:"g7",verified:false,runtime:{state:"stale",reason:"Herdr monitor: awaiting_answer",at:new Date(now).toISOString()}}],sessions:[],questions:[{id:"notify-work-1",identity:"x",text:"Which layout?",options:["A","B"],task:"work",status:"pending",created_at:new Date(now).toISOString()}]});
  assert.equal(livePresentations(snapshot,now).length,1);
  assert.equal(livePresentations({...snapshot,questions:[{...snapshot.questions![0],status:"succeeded"}]},now).length,0);
  assert.equal(livePresentations({...snapshot,questions:[{...snapshot.questions![0],task:"other"}]},now).length,0);
  assert.equal(livePresentations(snapshot,now+60001).length,0);
});

test("a Lavish tailnet link opens as returned, and plain http elsewhere stays refused", () => {
  for (const raw of ["http://sermon.tailcc4238.ts.net:4387/session/f26e","http://100.122.0.50:4387/session/f26e","http://127.0.0.1:4387/session/f26e","https://example.com/review"]) assert.equal(safePresentationURL(raw),true,raw);
  for (const raw of ["http://192.0.2.10:4387/session/f26e","http://100.128.0.1:4387/session/f26e","http://100.64.evil.example/review","http://[::ffff:100.64.0.1]/review","http://example.com/review","javascript:alert(1)"]) assert.equal(safePresentationURL(raw),false,raw);
});

test("a presentation naming a session marks only that card, and a pane-proven one only its task's own card", () => {
  const snapshot=parseSnapshot({healthy:true,tasks:[{id:"work",generation:"g7",session:"g",verified:false},{id:"quiet",generation:"g1",verified:false}],
    sessions:[{id:"g",role:"goblin",task_id:"work",generation:"g7"},{id:"child",role:"goblin",parent:"g",task_id:"work",generation:"g7"},{id:"old",role:"goblin",task_id:"work",generation:"g6"}]});
  const [work,quiet]=snapshot.tasks, [owner,child,old]=snapshot.sessions;
  const paneProven={id:"task-2-review",kind:"review",task_id:"work",generation:"g7",source:"",target:"",state:"active",url:"http://127.0.0.1:4387/session/abc",at:"",until:""};
  assert.equal(presentationShownOn(paneProven,owner,work),true);
  assert.equal(presentationShownOn(paneProven,undefined,work),true);
  assert.equal(presentationShownOn(paneProven,child,work),false);
  assert.equal(presentationShownOn(paneProven,old,work),false);
  assert.equal(presentationShownOn(paneProven,undefined,quiet),false);
  assert.equal(presentationShownOn(paneProven,undefined,undefined),false);
  const named={...paneProven,target:"child"};
  assert.equal(presentationShownOn(named,child,work),true);
  assert.equal(presentationShownOn(named,owner,work),false);
  assert.equal(presentationShownOn(named,undefined,work),false);
});
