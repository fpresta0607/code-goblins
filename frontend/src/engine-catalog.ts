import { useEffect, useSyncExternalStore } from "react";
import { message, reportToCfo, request } from "./api";
import { array, object, string, strings } from "./types";

export interface EngineSelection { harness: string; model: string; effort: string }
export interface EngineModel { id: string; name: string; efforts: string[]; default_effort: string }
export interface EngineHarness { id: string; name: string; reason: string; models: EngineModel[] }

export function parseEngineCatalog(value: unknown): EngineHarness[] {
  return array(object(value).harnesses).map((item) => {
    const harness = object(item);
    return { id: string(harness.id), name: string(harness.name), reason: string(harness.reason), models: array(harness.models).map((item) => {
      const model = object(item);
      return { id: string(model.id), name: string(model.name), efforts: strings(model.efforts), default_effort: string(model.default_effort) };
    }) };
  });
}

// The board reads the harnesses' catalog once, ahead of the first pane that
// lists it, and keeps it for the page's life, so the model pane opens at once
// with what it lists: the server reads the catalog from each harness's own
// files and programs, which takes a second or two (the Overlord, 2026-10-28:
// "open model modal with effort takes a second or two to load"). A catalog
// that could not be read goes to the CFO, as the board's errors do, and is
// read again by the next box shown or pane opened, or by Retry.
let catalog: { data?: EngineHarness[]; error?: string } = {};
let isReading = false;
const catalogListeners = new Set<() => void>();
const subscribeCatalog = (listener: () => void) => {
  catalogListeners.add(listener);
  return () => { catalogListeners.delete(listener); };
};
const settle = (next: typeof catalog) => {
  catalog = next;
  for (const listener of catalogListeners) listener();
};
const readCatalog = () => {
  if (isReading) return;
  isReading = true;
  if (catalog.error) settle({});
  request("/api/engines").then(parseEngineCatalog).then((data) => {
    isReading = false;
    settle({ data });
  }, (error: unknown) => {
    isReading = false;
    reportToCfo("the harnesses' models", message(error));
    settle({ error: message(error) });
  });
};

// useEngineCatalog is the harnesses' catalog the board has read, reading it
// as shouldRead turns true while none is held, and reload reads it again.
export function useEngineCatalog(shouldRead: boolean) {
  const read = useSyncExternalStore(subscribeCatalog, () => catalog);
  useEffect(() => {
    if (shouldRead && !catalog.data) readCatalog();
  }, [shouldRead]);
  return { ...read, reload: readCatalog };
}
