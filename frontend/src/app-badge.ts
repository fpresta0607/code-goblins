import { request } from "./api";
import { object, string } from "./types";

// isPushingApp is whether the board runs as an app added to a Home Screen,
// from the private board, where its browser pushes: his phone's Code Goblins
// app, whose App badge the supervisor pushes to. The board on this PC is
// never one: the desktop window and the page tell what waits there.
export const isPushingApp = () => location.hostname !== "127.0.0.1" && location.hostname !== "[::1]" && location.hostname !== "localhost"
  && (matchMedia("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true)
  && "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;

const fromBase64Url = (value: string) => Uint8Array.from(atob(value.replace(/-/g, "+").replace(/_/g, "/")), (char) => char.charCodeAt(0));

// postPush tells the supervisor of an app's badge turned on or off.
export const postPush = (instance: string, body: unknown) => request("/api/push", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(body) });

// subscribeApp subscribes the app to the supervisor's pushes with the board's
// key and tells the supervisor, which keeps the subscription.
export async function subscribeApp(instance: string, registration: ServiceWorkerRegistration, key: Uint8Array<ArrayBuffer>): Promise<void> {
  const { endpoint, keys } = (await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })).toJSON();
  await postPush(instance, { subscribe: { endpoint, keys } });
}

// boardKey is the key the board signs its pushes with.
export const boardKey = async () => fromBase64Url(string(object(await request("/api/push")).key));

// renewAppBadge tells the supervisor again of an app still subscribed, as it
// opens, since the supervisor may have forgotten it; one subscribed with a key
// the board no longer signs with, whose pushes would never arrive, subscribes
// again with the board's key. Notifications he turned off in Settings end the
// badge, so the supervisor stops pushing to it.
export async function renewAppBadge(instance: string): Promise<void> {
  const registration = await navigator.serviceWorker.getRegistration("/");
  const subscription = await registration?.pushManager.getSubscription();
  if (!registration || !subscription) return;
  if (Notification.permission !== "granted") {
    await postPush(instance, { unsubscribe: subscription.endpoint });
    await subscription.unsubscribe();
    return;
  }
  const key = await boardKey();
  const held = subscription.options.applicationServerKey;
  if (held && held.byteLength === key.length && new Uint8Array(held).every((byte, i) => byte === key[i])) {
    const { endpoint, keys } = subscription.toJSON();
    await postPush(instance, { subscribe: { endpoint, keys } });
    return;
  }
  await postPush(instance, { unsubscribe: subscription.endpoint });
  await subscription.unsubscribe();
  await subscribeApp(instance, registration, key);
}
