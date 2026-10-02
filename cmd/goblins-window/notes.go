package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// noteMessage is what the board's page sends the window before a notification
// to raise, as JSON.
const noteMessage = "notify:"

// notesScript runs in the board's page after each load. The board raises a
// notification for each alert the Overlord is not looking at, by rules of its
// own: what newly waits on him, and a goblin that is blocked, failed or done.
// WebView2 would draw that itself, outside Windows' notifications and their
// center, so the page's Notification hands each one to the window, which
// raises a Windows notification while he cannot see the window and tells the
// page when it is clicked, so the board opens the item. The window is a
// program he installed, whose notifications Windows' own settings turn off,
// so the board is told it may notify and never asks. A page that changed
// Notification itself, as the board's own tests do, keeps what it set.
const notesScript = `(() => {
  if (window.codeGoblinsNotes) return;
  const untouched = (value) => /\[native code\]/.test(String(value));
  if (!untouched(window.Notification) || !untouched(Object.getOwnPropertyDescriptor(window.Notification, "permission")?.get)) return;
  const notes = new Map();
  window.codeGoblinsNotes = notes;
  window.Notification = class {
    static get permission() { return "granted"; }
    static requestPermission() { return Promise.resolve("granted"); }
    constructor(title, options) {
      this.tag = String(options?.tag || crypto.randomUUID());
      this.onclick = null;
      this.onclose = null;
      notes.set(this.tag, this);
      // A note nobody closed goes once a hundred are newer.
      if (notes.size > 100) notes.delete(notes.keys().next().value);
      window.chrome.webview.postMessage("` + noteMessage + `" + JSON.stringify({ id: this.tag, title: String(title), body: String(options?.body ?? "") }));
    }
    close() {
      if (notes.get(this.tag) === this) notes.delete(this.tag);
      this.onclose?.();
    }
  };
  window.codeGoblinsNoteClicked = (id) => notes.get(id)?.onclick?.();
})();`

// Note is a notification the board's page asks the window to raise.
type Note struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// noteToRaise returns the notification a message from the window's page asks
// for. Only a board's own page may ask, and a note needs an id and a title.
func noteToRaise(message, sender string) (Note, bool) {
	text, ok := strings.CutPrefix(message, noteMessage)
	if !ok || !fromBoard(sender) {
		return Note{}, false
	}
	var note Note
	if err := json.Unmarshal([]byte(text), &note); err != nil || note.ID == "" || note.Title == "" || len(note.ID) > 256 {
		return Note{}, false
	}
	// Windows cuts what a notification shows; this bounds what it is handed.
	note.Title, note.Body = cut(note.Title, 200), cut(note.Body, 1000)
	return note, true
}

// cut returns text, or its first limit characters when it holds more.
func cut(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// repeatWindow is how long a notification's id is known as already raised.
const repeatWindow = 10 * time.Minute

// Raised remembers the notifications the window raised lately, by id. A
// thing that newly waits on the Overlord can reach the window twice: from its
// own look at the board, and from the board's page, whose alert has the same
// id but comes late on a busy PC, since Windows runs a hidden page at the
// lowest priority. The one that comes second is dropped here.
type Raised struct {
	mu   sync.Mutex
	when map[string]time.Time
}

// First reports whether id was not raised within the repeat window before
// now, and notes it as raised now.
func (r *Raised) First(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for known, at := range r.when {
		if now.Sub(at) >= repeatWindow {
			delete(r.when, known)
		}
	}
	if _, raised := r.when[id]; raised {
		return false
	}
	if r.when == nil {
		r.when = map[string]time.Time{}
	}
	r.when[id] = now
	return true
}

// noteClicked is the script that tells the board's page its notification id
// was clicked.
func noteClicked(id string) string {
	// A string always encodes.
	quoted, _ := json.Marshal(id)
	return "window.codeGoblinsNoteClicked?.(" + string(quoted) + ");"
}
