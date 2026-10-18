// The Code Goblins app's worker, which the board registers when the Overlord
// turns the app badge on: a push from the supervisor names an item newly
// waiting on him and how many wait, which the app's icon carries while the
// app is closed. Tapping its notification brings the board up.
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
  event.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((boards) => boards.length ? boards[0].focus() : self.clients.openWindow("/")));
});
