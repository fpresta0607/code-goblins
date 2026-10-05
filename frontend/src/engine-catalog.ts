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
