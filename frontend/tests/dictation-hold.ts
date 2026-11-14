import type { Page, Request } from "./site";

// The bubble shows it listens at the press, while the microphone opens, and
// what is said while it opens is not heard (dictationEngine.ts), so a hold let
// go before then types nothing. A test that speaks for a while counts from
// the moment the recording begins on the open microphone, which the board
// marks by asking the supervisor to warm its speech model. Counted from the
// press, a loaded CI runner let the keys go before the microphone opened and
// the box stayed empty (#2171, #2436).
export function recordingBegins(page: Page): Promise<Request> {
  return page.waitForRequest((request) => new URL(request.url()).pathname === "/api/dictation/warm");
}

// slowMicrophone has the page's microphone take 2 s to open, longer than a
// test speaks for, as a loaded machine's can.
export async function slowMicrophone(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const ask = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getUserMedia = async (constraints) => {
      await new Promise((resolve) => setTimeout(resolve, 2000));
      return ask(constraints);
    };
  });
}
