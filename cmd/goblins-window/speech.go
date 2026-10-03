package main

// speechScript runs in the board's page after each load. WebView2 has the
// browser's SpeechRecognition but no speech service behind it: listening ends
// at once with a network error, and the board would tell the Overlord to check
// his connection. With WebView2's own recognizer taken away the board says
// what is true, that this window has no speech recognition. A dictation
// program that types into whatever has the keyboard dictates here as anywhere.
const speechScript = `for (const name of ["SpeechRecognition", "webkitSpeechRecognition"]) {
  if (/\[native code\]/.test(String(window[name]))) Object.defineProperty(window, name, { configurable: true, writable: true, value: undefined });
}`
