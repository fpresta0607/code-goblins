// The board's dictation records through this audio worklet. While the
// microphone is open it keeps the last second it heard, so a recording can
// begin at the moment the keys went down, which the page names as a frame of
// the audio context's clock. Asked to record from a frame, it hands the page
// what it kept from that frame on, then the samples as they arrive, 2048 at a
// time, so the words said so far are recognised while the Overlord is still
// speaking. Asked to flush, it hands over what it holds and then null, and
// records no more until it is asked again.
class DictationCapture extends AudioWorkletProcessor {
  constructor() {
    super();
    this.kept = new Float32Array(sampleRate);
    // How many samples the ring holds, and the frame after the newest.
    this.held = 0;
    this.end = 0;
    this.batch = new Float32Array(2048);
    this.filled = 0;
    this.recording = false;
    this.port.onmessage = (event) => {
      if (event.data === "flush") {
        if (this.recording) this.port.postMessage(this.batch.slice(0, this.filled));
        this.filled = 0;
        this.recording = false;
        this.port.postMessage(null);
        return;
      }
      this.recording = true;
      this.filled = 0;
      for (let frame = Math.max(event.data.from, this.end - this.held); frame < this.end; frame++) this.take(this.kept[frame % this.kept.length]);
    };
  }

  take(sample) {
    this.batch[this.filled++] = sample;
    if (this.filled === this.batch.length) {
      this.port.postMessage(this.batch.slice());
      this.filled = 0;
    }
  }

  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    // With the microphone closed nothing comes in, and what the ring kept
    // was heard before it closed.
    if (!channel) {
      this.held = 0;
      return true;
    }
    for (let index = 0; index < channel.length; index++) {
      this.kept[(currentFrame + index) % this.kept.length] = channel[index];
      if (this.recording) this.take(channel[index]);
    }
    this.end = currentFrame + channel.length;
    this.held = Math.min(this.kept.length, this.held + channel.length);
    return true;
  }
}

registerProcessor("dictation-capture", DictationCapture);
