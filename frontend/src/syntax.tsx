// Language selection adapted from Cline Kanban shared/diff-renderer.tsx.
// Copyright 2026 Cline Bot Inc. Apache-2.0. See public/assets/NOTICE.txt.
// CFO uses React text nodes for every Prism token, with no raw HTML injection.
import Prism from "prismjs";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-jsx";
import "prismjs/components/prism-tsx";
import "prismjs/components/prism-go";
import "prismjs/components/prism-json";
import "prismjs/components/prism-python";
import "prismjs/components/prism-rust";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-markdown";
import "prismjs/components/prism-sql";
import { useMemo } from "react";

const languages: Record<string, string> = {
  js: "javascript",
  cjs: "javascript",
  mjs: "javascript",
  ts: "typescript",
  tsx: "tsx",
  jsx: "jsx",
  go: "go",
  json: "json",
  py: "python",
  rs: "rust",
  sh: "bash",
  ps1: "powershell",
  yaml: "yaml",
  yml: "yaml",
  md: "markdown",
  html: "markup",
  svg: "markup",
  css: "css",
  sql: "sql",
};
export function resolvePrismLanguage(path: string): string | null {
  const basename =
    path.replaceAll("\\", "/").split("/").pop()?.toLowerCase() ?? "";
  const language =
    basename === "dockerfile"
      ? "bash"
      : languages[basename.split(".").pop() ?? ""];
  return language && Prism.languages[language] ? language : null;
}
interface Segment {
  text: string;
  classes: string;
}
export function highlightLines(code: string, path: string): Segment[][] {
  const language = resolvePrismLanguage(path);
  const tokens = language
    ? Prism.tokenize(code, Prism.languages[language])
    : [code];
  const lines: Segment[][] = [[]];
  const visit = (token: string | Prism.Token, parents: string[]) => {
    if (typeof token === "string") {
      token.split("\n").forEach((part, i) => {
        if (i) lines.push([]);
        lines[lines.length - 1].push({
          text: part,
          classes: parents.join(" "),
        });
      });
    } else {
      const classes = [...parents, "token", token.type];
      if (typeof token.content === "string") visit(token.content, classes);
      else if (Array.isArray(token.content))
        token.content.forEach((child) => visit(child, classes));
      else visit(token.content, classes);
    }
  };
  tokens.forEach((token) => visit(token, []));
  return lines;
}
export function SyntaxLine({ text, path }: { text: string; path: string }) {
  const lines = useMemo(() => highlightLines(text, path), [text, path]);
  return (
    <code>
      {lines[0].map((segment, i) => (
        <span className={segment.classes} key={i}>
          {segment.text || " "}
        </span>
      ))}
    </code>
  );
}
export function CodePreview({
  code,
  path,
  limit,
}: {
  code: string;
  path: string;
  limit: number;
}) {
  const lines = useMemo(() => highlightLines(code, path), [code, path]);
  return (
    <div className="code-preview" aria-label={`Source code for ${path}`}>
      {lines.slice(0, limit).map((line, i) => (
        <div className="code-line" key={i}>
          <span className="line-number" aria-hidden="true">
            {i + 1}
          </span>
          <code>
            {line.map((part, n) => (
              <span className={part.classes} key={n}>
                {part.text || " "}
              </span>
            ))}
          </code>
        </div>
      ))}
    </div>
  );
}
