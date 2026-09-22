import type { Action } from "./types.ts";

export interface Submission {
  id: string;
  payload: string;
}
// The payload, not whether HTTP or SSE arrived first, owns request identity.
export function submissionFor(
  payload: string,
  prior: Submission | null,
  createID: () => string,
): Submission {
  return prior?.payload === payload ? prior : { id: createID(), payload };
}
export function alreadyKnown(
  submission: Submission | null,
  payload: string,
  actions: Action[],
): Action | undefined {
  return submission?.payload === payload
    ? actions.find((action) => action.id === submission.id)
    : undefined;
}
