// shortModel is the name the box shows for a session's model, short enough
// that the row under the message never cuts it (the CFO's review of step 3,
// 2026-10-27: "Model short (Opus 5.5), never cut at 360 px"): a Claude
// model's family and version, a GPT model's version and tier, and any other
// model's own name without its provider. Its full name stays in the tip.
export function shortModel(model: string): string {
  const name = model.trim().replace(/\[[^\]]*\]$/, "").replace(/^.*\//, "");
  const claude = /^claude-([a-z]+)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$/i.exec(name);
  if (claude) return capital(claude[1]) + " " + claude[2] + (claude[3] ? "." + claude[3] : "");
  const gpt = /^gpt-([\d.]+)(?:-([a-z]+))?$/i.exec(name);
  if (gpt) return "GPT-" + gpt[1] + (gpt[2] ? " " + capital(gpt[2]) : "");
  return name;
}

const capital = (word: string) => word[0].toUpperCase() + word.slice(1).toLowerCase();
