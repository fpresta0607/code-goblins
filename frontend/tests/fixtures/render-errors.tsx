import { useState } from "react";
import { createRoot } from "react-dom/client";
import { knowInstance } from "../../src/api";
import { FitList } from "../../src/FitList";
import { QueuedTasks } from "../../src/QueuedTasks";
import { RankedCards } from "../../src/RankedCards";
import { parseSnapshot } from "../../src/types";
import { BrokenCard } from "./broken-card";
import "../../src/styles.css";

const snapshot = parseSnapshot({ healthy: true, tasks: [
  { id: "broken", title: "Recovered card", phase: "queued", verified: false },
  { id: "healthy", title: "Healthy neighbor", phase: "queued", verified: false },
] });

// The queued list fails as it orders its cards, where reading the broken
// task's id throws: a failure in one card's own drawing is that card's, so
// the list fails only in what the list itself reads. The broken task comes
// second, since what is next up reads the first task's id outside the list.
const failing = { ...snapshot, tasks: [snapshot.tasks[1], Object.defineProperty({ ...snapshot.tasks[0] }, "id", { get() { throw new Error("Fixture list failed to render"); } })] };

function RenderErrors() {
  const [hasFailure, setHasFailure] = useState(true);
  const mode = location.hash.slice(1);
  const card = (task: typeof snapshot.tasks[number]) => <BrokenCard title={task.title} hasFailure={hasFailure && task.id === "broken"} />;
  return <main>
    <button onClick={() => setHasFailure(false)}>Repair fixture</button>
    <section aria-label="Affected list">
      {mode === "queued" ? <QueuedTasks snapshot={hasFailure ? failing : snapshot} now={0} presentations={[]} onSelect={() => {}} /> : mode === "ranked" ? <RankedCards tasks={snapshot.tasks} list="progress" instance="fixture" revision={0} empty={null} renderCard={card} />
        : <FitList items={snapshot.tasks} keyOf={(task) => task.id} empty={null} renderItem={card} />}
    </section>
    <section aria-label="Unaffected list">Another usable list</section>
  </main>;
}

knowInstance("fixture");
createRoot(document.getElementById("root")!).render(<RenderErrors />);
