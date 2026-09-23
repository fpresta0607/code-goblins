import { useEffect, useRef, useState } from "react";
import type { Snapshot } from "./types";
import { ActivityBuffer, mergeActivityEffects, type ActivityEffect } from "./activity";

export function useActivity(snapshot:Snapshot|null,connected:boolean) {
  const buffer=useRef(new ActivityBuffer());
  const frame=useRef<number|null>(null);
  const latest=useRef({snapshot,connected});
  const clear=useRef(false);
  const [effects,setEffects]=useState<ActivityEffect[]>([]);
  useEffect(()=>{
    if(!snapshot) return;
    latest.current={snapshot,connected};
    if(buffer.current.update(snapshot,connected,!document.hidden,Date.now())) clear.current=true;
    if(frame.current!==null) return;
    frame.current=requestAnimationFrame(()=>{
      frame.current=null;
      const pending=buffer.current.drain();
      const reset=clear.current;clear.current=false;
      if(!latest.current.connected||document.hidden) setEffects([]);
      else if(pending.length||reset) setEffects(prior=>mergeActivityEffects(reset?[]:prior,pending,Date.now()));
    });
  },[snapshot,connected]);
  useEffect(()=>{
    const visibility=()=>{
      if(frame.current!==null) cancelAnimationFrame(frame.current);
      frame.current=null;
      const {snapshot,connected}=latest.current;
      if(snapshot) buffer.current.update(snapshot,connected,false,Date.now());
      if(snapshot&&!document.hidden) buffer.current.update(snapshot,connected,true,Date.now());
      setEffects([]);
    };
    document.addEventListener("visibilitychange",visibility);
    return()=>{document.removeEventListener("visibilitychange",visibility);if(frame.current!==null)cancelAnimationFrame(frame.current);};
  },[]);
  useEffect(()=>{
    if(!effects.length)return;
    const timer=setTimeout(()=>setEffects(prior=>prior.filter(event=>event.expires>Date.now())),Math.max(0,Math.min(...effects.map(event=>event.expires))-Date.now())+1);
    return()=>clearTimeout(timer);
  },[effects]);
  return effects;
}
