// The Code Goblins app's worker, which the board registers when the Overlord
// turns the app badge on: a push from the supervisor names an item newly
// waiting on him and how many wait, which the app's icon carries while the
// app is closed. Tapping its notification brings the board up at its item
// (#2363): a board already open is told the item's address, which it opens
// in place, so what he was typing there stays; with none open, the board
// opens at that address. The push's tag is the item's alert key, the item's
// key and @ its publishing.
self.addEventListener("push", (event) => {
  const push = event.data ? event.data.json() : {};
  const count = Number(push.count) || 0;
  const badge = !("setAppBadge" in self.navigator) ? Promise.resolve()
    : count ? self.navigator.setAppBadge(count) : self.navigator.clearAppBadge();
  event.waitUntil(Promise.all([
    self.registration.showNotification(push.title || "Code Goblins", { body: push.body || "", tag: push.tag || undefined }),
    badge,
  ]));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const tag = event.notification.tag || "";
  const item = tag.includes("@") ? tag.slice(0, tag.lastIndexOf("@")) : tag;
  const address = item ? "/?" + new URLSearchParams({ item }).toString() : "";
  event.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((boards) => {
    if (!boards.length) return self.clients.openWindow(address || "/");
    if (address) boards[0].postMessage({ open: address });
    return boards[0].focus();
  }));
});
