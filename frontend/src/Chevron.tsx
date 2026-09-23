export function Chevron({ collapsed }: { collapsed: boolean }) {
  return <svg viewBox="0 0 20 20" aria-hidden="true"><path d={collapsed ? "m8 5 5 5-5 5" : "m5 8 5 5 5-5"} fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" /></svg>;
}
