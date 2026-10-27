import { Icon } from "./Icon";
import { isPicturePath, keptFileAddress } from "./kept-files";

// The files he added to a message, in the box above his words: a picture as
// a thumbnail read back from where the board kept it, any other file by its
// name, each with a button that takes it out of what Send sends.
export function ComposerAttachments({ paths, instance, task, onRemove }: {
  paths: string[]; instance: string; task?: string; onRemove: (path: string) => void;
}) {
  return <ul className="composer-attachments" aria-label="Attached files">
    {paths.map((path, at) => {
      const name = path.slice(Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\")) + 1);
      const label = isPicturePath(path) ? "Picture " + (at + 1) : name;
      return <li key={path} className={isPicturePath(path) ? "composer-attachment picture" : "composer-attachment"}>
        {isPicturePath(path)
          ? <img src={keptFileAddress(instance, task, path)} alt={label} decoding="async" />
          : <span className="composer-attachment-name"><Icon name="file" />{name}</span>}
        <button type="button" className="composer-attachment-remove" aria-label={"Remove " + label} data-tip="Remove" onClick={() => onRemove(path)}><Icon name="close" /></button>
      </li>;
    })}
  </ul>;
}
