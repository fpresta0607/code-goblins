import type { Action, Question } from "./types.ts";

export function questionSelection(question: Question, draft?: {selection:string; written:string}, receipt?: Action) {
  if (question.answer_id) return { selection: question.answer_kind === "other" ? "other" : "option:"+question.answer, written: question.answer_kind === "other" ? question.answer : "" };
  if (receipt?.kind === "cfo_answer" && receipt.question_id === question.id && receipt.generation === question.identity) return { selection: receipt.answer_kind === "other" ? "other" : "option:"+receipt.text, written: receipt.answer_kind === "other" ? receipt.text : "" };
  return question.status === "pending" && draft ? draft : { selection:"", written:"" };
}

export function questionChoices(question: Question) {
  const options = [...question.options];
  const index = options.indexOf(question.recommended);
  if (index > 0) options.unshift(...options.splice(index, 1));
  return options.map((value, i) => ({ value, label: String.fromCharCode(65 + i), recommended: value === question.recommended }));
}

export function questionAnswer(question: Question, selection: string, written: string) {
  const other = selection === "other";
  const text = other ? written : selection.startsWith("option:") ? selection.slice(7) : "";
  if (!text.trim() || (!other && !question.options.includes(text))) return null;
  return { kind: "cfo_answer", question_id: question.id, generation: question.identity, answer_kind: other ? "other" : "option", text };
}
