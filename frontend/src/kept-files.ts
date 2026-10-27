// The files he pastes into, drops on or adds to the message box, which the
// supervisor keeps where the session reads them (internal/supervisor/
// composer_files.go), and the message names by path, one to a line.

const PICTURE = /\.(?:png|jpe?g|gif|webp)$/i;
// A line of his words that is only the path of a picture the box kept.
const KEPT_PICTURE = /^(?:[A-Za-z]:[\\/]|[\\/]).*[\\/]composer-files[\\/]pasted-[^\\/]+\.(?:png|jpe?g|gif|webp)$/i;

export function isPicturePath(path: string): boolean {
  return PICTURE.test(path);
}

// keptPictures splits his words from the pictures the box kept for them, so
// his bubble shows each picture rather than its path.
export function keptPictures(text: string): { text: string; paths: string[] } {
  const paths: string[] = [];
  const words = text.split("\n").filter((line) => {
    const path = line.trim();
    if (!KEPT_PICTURE.test(path)) return true;
    paths.push(path);
    return false;
  });
  return paths.length ? { text: words.join("\n").trim(), paths } : { text, paths };
}

// keptFileAddress reads a kept picture back from the supervisor, with the
// board's token in the query, as an <img> sends no header.
export function keptFileAddress(instance: string, task: string | undefined, path: string): string {
  const query = new URLSearchParams({ token: instance });
  if (task) query.set("task", task);
  query.set("path", path);
  return "/api/composer/file?" + query.toString();
}

// filesOf is what a paste or a drop holds as files. A screenshot pasted in
// some browsers arrives only as an item of kind "file" with an empty files
// list, so the items are read when the list is empty.
export function filesOf(data: DataTransfer | null): File[] {
  if (!data) return [];
  if (data.files.length) return Array.from(data.files);
  return Array.from(data.items).flatMap((item) => {
    const file = item.kind === "file" ? item.getAsFile() : null;
    return file ? [file] : [];
  });
}
