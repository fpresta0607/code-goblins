import test from "node:test";
import assert from "node:assert/strict";
import { questionChoices, questionAnswer, questionSelection } from "./questionChoices.ts";
import { parseSnapshot, parseAction } from "./types.ts";

test("recommendation moves first without changing answer identity; Other stays explicit", () => {
  const q = parseSnapshot({ healthy:true, questions: [{id:"question-1", identity:"cfo-1", options:["Stay", "Isolate", "Wait"], recommended:"Isolate"}] }).questions![0];
  assert.deepEqual(questionChoices(q).map(c=>[c.label,c.value,c.recommended]), [["A", "Isolate",true],["B","Stay",false],["C","Wait",false]]);
  assert.equal(questionChoices({...q, recommended:""}).some(c=>c.recommended), false);
  assert.equal(questionAnswer(q, "", ""), null);
  assert.equal(questionAnswer(q, "other", "  "), null);
  assert.deepEqual(questionAnswer(q, "other", "Use another folder 日本語"), {kind:"cfo_answer",question_id:"question-1",generation:"cfo-1",answer_kind:"other",text:"Use another folder 日本語"});
  assert.equal(questionAnswer(q, "Invented", ""), null);
  assert.equal(questionAnswer(q, "option:Isolate", "")?.text, "Isolate");
});

test("each choice keeps the image the goblin attached to it after the recommendation moves first", () => {
  const q = parseSnapshot({ healthy:true, questions: [{id:"notify-gb-x-7", identity:"goblin-1", task:"gb-x", options:["Postgres", "SQLite", "MySQL"], recommended:"SQLite", image_count:3}] }).questions![0];
  assert.deepEqual(questionChoices(q).map(c=>[c.value,c.image]), [["SQLite","/api/questions/notify-gb-x-7/images/1"],["Postgres","/api/questions/notify-gb-x-7/images/0"],["MySQL","/api/questions/notify-gb-x-7/images/2"]]);
  assert.equal(questionChoices({...q, image_count:0}).some(c=>c.image), false);
});

test("a durable answer from another tab replaces every unsent or edited draft", () => {
  const question = parseSnapshot({healthy:true,questions:[{id:"question-1",identity:"cfo-1",status:"queued",answer_id:"answer-1",answer:"Proceed",answer_kind:"option",options:["Proceed"]}]}).questions![0];
  const draft={selection:"other",written:"Wait"};
  assert.deepEqual(questionSelection(question,draft),{selection:"option:Proceed",written:""});
  assert.deepEqual(questionSelection({...question,answer:"Actual written answer",answer_kind:"other"},draft),{selection:"other",written:"Actual written answer"});
  const pending={...question,status:"pending",answer_id:"",answer:""};
  const receipt=parseAction({id:"answer-1",kind:"cfo_answer",question_id:question.id,generation:question.identity,text:"Proceed",answer_kind:"option"});
  assert.deepEqual(questionSelection(pending,draft,receipt),{selection:"option:Proceed",written:""});
  assert.deepEqual(questionSelection(pending,draft),draft);
  assert.deepEqual(questionSelection({...pending,status:"superseded"},draft),{selection:"",written:""});
  assert.deepEqual(questionSelection({...pending,status:"succeeded",answer:"Proceed. Ship it",answered_option:"Proceed",answered_by:"cfo"},draft),{selection:"option:Proceed",written:""});
  assert.deepEqual(questionSelection({...question,status:"failed"},draft),{selection:"",written:""});
});

test("a goblin's question is answered back to that goblin, never through the CFO", () => {
  const q = parseSnapshot({ healthy:true, questions: [{id:"notify-gb-x-7", identity:"goblin-1", task:"gb-x", options:["Postgres", "SQLite"]}] }).questions![0];
  assert.equal(q.task, "gb-x");
  assert.deepEqual(questionAnswer(q, "option:SQLite", ""), {kind:"goblin_answer",question_id:"notify-gb-x-7",generation:"goblin-1",answer_kind:"option",text:"SQLite"});
  const cfo = parseSnapshot({ healthy:true, questions: [{id:"question-1", identity:"cfo-1", options:["Yes"]}] }).questions![0];
  assert.equal(cfo.task, "");
  assert.equal(questionAnswer(cfo, "option:Yes", "")?.kind, "cfo_answer");
  const receipt = parseAction({id:"answer-1",kind:"goblin_answer",question_id:q.id,generation:q.identity,text:"SQLite",answer_kind:"option"});
  assert.deepEqual(questionSelection({...q, status:"pending"}, {selection:"other",written:"Wait"}, receipt), {selection:"option:SQLite",written:""});
});
