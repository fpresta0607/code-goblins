import { useEffect, useState } from "react";
import { message, reportToCfo } from "./api";
import { boardKey, isPushingApp, postPush, subscribeApp } from "./app-badge";
import { SettingSwitch } from "./setting-switch";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import "./remote-access.css";

// The app badge, as the Overlord asked (CFO decision 1008918): in his phone's
// Home Screen app alone, a switch whose press asks for notifications in the
// app's own words, the system's prompt naming the app, and only then, and
// subscribes the app to the supervisor's pushes, which carry each item newly
// waiting on him and how many wait while the app is closed. Off drops the
// subscription.
export function AppBadgeSetting({ instance }: { instance: string }) {
  const [isOn, setIsOn] = useState<boolean>();
  const [pending, setPending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const isOffered = isPushingApp();
  useEffect(() => {
    if (!isOffered) return;
    navigator.serviceWorker.getRegistration("/").then((registration) => registration?.pushManager.getSubscription())
      .then((subscription) => setIsOn(!!subscription && Notification.permission === "granted"))
      .catch((error: unknown) => reportToCfo("the app badge", message(error)));
  }, [isOffered]);
  if (!isOffered || isOn === undefined) return null;
  const turnOn = async () => {
    if (await Notification.requestPermission() !== "granted") throw new Error("Allow notifications for Code Goblins in Settings");
    const registration = await navigator.serviceWorker.register("/push-worker.js");
    await navigator.serviceWorker.ready;
    await subscribeApp(instance, registration, await boardKey());
  };
  const turnOff = async () => {
    const subscription = await (await navigator.serviceWorker.getRegistration("/"))?.pushManager.getSubscription();
    if (!subscription) return;
    await postPush(instance, { unsubscribe: subscription.endpoint });
    await subscription.unsubscribe();
  };
  const press = async () => {
    setPending(true);
    try {
      if (isOn) await turnOff(); else await turnOn();
      setIsOn(!isOn);
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setPending(false); }
  };
  return <div className="app-badge-setting">
    <SettingSwitch label="App badge" on={isOn} pending={pending} disabled={pending || !instance} onPress={() => void press()} />
    <ClickFeedback text={feedback} />
  </div>;
}
