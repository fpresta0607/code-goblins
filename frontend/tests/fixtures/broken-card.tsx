export function BrokenCard({ title, hasFailure }: { title: string; hasFailure: boolean }) {
  if (hasFailure) throw new Error("Fixture card failed to render");
  return <button className="task-card">{title}</button>;
}
