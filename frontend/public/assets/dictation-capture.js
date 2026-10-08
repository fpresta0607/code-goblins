// The board's dictation records through this audio worklet: it hands the
// page the microphone's samples as they arrive, 2048 at a time, so the words
// said so far are recognised while the Overlord is still speaking. Asked to
// flush, it hands over what it holds and then null.
class DictationCapture extends AudioWorkletProcessor {
  constructor() {
    super();
    this.batch = new Float32Array(2048);
    this.filled = 0;
    this.port.onmessage = () => {
      this.port.postMessage(this.batch.slice(0, this.filled));
      this.filled = 0;
      this.port.postMessage(null);
    };
  }

  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    if (!channel) return true;
    for (const sample of channel) {
      this.batch[this.filled++] = sample;
      if (this.filled === this.batch.length) {
        this.port.postMessage(this.batch.slice());
        this.filled = 0;
      }
    }
    return true;
  }
}

registerProcessor("dictation-capture", DictationCapture);
