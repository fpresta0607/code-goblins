import type { CloudSighting, Snapshot, Task } from "./types.ts";

// CloudWork is one Cloud work ready item: a cloud pull request into a
// handed-off task's branch, at the head the supervisor saw, that he has not
// left.
export interface CloudWork { key: string; task: Task; sighting: CloudSighting }

export const cloudWork = (snapshot: Snapshot): CloudWork[] => snapshot.tasks.flatMap((task) => (task.cloud_handoff?.cloud || [])
  .filter((sighting) => !sighting.is_left)
  .map((sighting) => ({ key: "cloud:" + task.id + ":" + sighting.pull_request + "@" + sighting.head, task, sighting })));

// What a Cloud work ready item says of its pull request: its title, head and
// commits, and for a teammate's, that bringing it back is his press.
export const cloudWorkSummary = (sighting: CloudSighting) => sighting.title + ", at " + sighting.head.slice(0, 7)
  + ", " + sighting.commits + (sighting.commits === 1 ? " commit" : " commits")
  + (sighting.is_teammates ? ", opened by " + sighting.author + ", not by you: Bring back is your press" : "");

// What its body says the cloud session ran, as it begins, or that it says
// nothing.
export const cloudWorkBody = (sighting: CloudSighting) => sighting.body ? "Its body says: " + sighting.body : "Its body says nothing of what it ran.";
