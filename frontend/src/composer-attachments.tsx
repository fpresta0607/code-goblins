import { Icon } from "./Icon";
import { isPicturePath, keptFileAddress, pastedTextName, pastedTexts } from "./kept-files";
import "./composer-attachments.css";

// The files he added to a message or a written answer, above his words: a
// picture as a thumbnail read back from where the board kept it, any other
// file by its name, and a long paste kept as text by its lines, each with a
// button that takes it out of what Send sends. paths holds each as Send names
// it: a file by its path, a pasted text by its line (pastedTextLine).
export function ComposerAttachments({ paths, instance, task, onRemove }: {
  paths: string[]; instance: string; task?: string; onRemove: (path: string) => void;
}) {
  return <ul className="composer-attachments" aria-label="Attached files">
    {paths.map((path, at) => {
      const paste = pastedTexts(path).pastes[0];
      const name = paste ? pastedTextName(paste.lines) : path.slice(Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\")) + 1);
      const label = isPicturePath(path) ? "Picture " + (at + 1) : name;
      return <li key={path} className={isPicturePath(path) ? "composer-attachment picture" : "composer-attachment"}>
        {isPicturePath(path)
          ? <img src={keptFileAddress(instance, task, path)} alt={label} decoding="async" />
          : <span className="composer-attachment-name"><Icon name="file" />{name}</span>}
        <button type="button" className="composer-attachment-remove" aria-label={"Remove " + label} onClick={() => onRemove(path)}><Icon name="close" /></button>
      </li>;
    })}
  </ul>;
}
