function decodePCM(encoded) {
  const raw = atob(encoded);
  const samples = new Float32Array(Math.floor(raw.length / 2));
  for (let index = 0; index < samples.length; index += 1) {
    const low = raw.charCodeAt(index * 2);
    const high = raw.charCodeAt(index * 2 + 1);
    let value = low | (high << 8);
    if (value >= 0x8000) value -= 0x10000;
    samples[index] = value / 32768;
  }
  return samples;
}

export class AudioPlayer {
  constructor() {
    this.context = null;
    this.sampleRate = 0;
    this.startTime = 0;
    this.sources = new Set();
    this.chunks = [];
  }

  async begin(sampleRate, keep = true) {
    this.stop();
    this.sampleRate = sampleRate;
    if (keep) this.chunks = [];
    this.context ??= new AudioContext();
    await this.context.resume();
    this.startTime = this.context.currentTime;
  }

  push(encoded, keep = true) {
    if (!this.context || !this.sampleRate) return;
    if (keep) this.chunks.push(encoded);
    const samples = decodePCM(encoded);
    const buffer = this.context.createBuffer(1, samples.length, this.sampleRate);
    buffer.copyToChannel(samples, 0);
    const source = this.context.createBufferSource();
    source.buffer = buffer;
    source.connect(this.context.destination);
    const when = Math.max(this.context.currentTime, this.startTime);
    source.start(when);
    this.startTime = when + buffer.duration;
    this.sources.add(source);
    source.addEventListener("ended", () => this.sources.delete(source));
  }

  stop() {
    for (const source of this.sources) {
      try { source.stop(); } catch { /* already ended */ }
    }
    this.sources.clear();
    this.startTime = 0;
  }

  async replay() {
    const chunks = [...this.chunks];
    await this.begin(this.sampleRate, false);
    for (const chunk of chunks) this.push(chunk, false);
  }

  remainingMS() {
    if (!this.context) return 0;
    return Math.max(0, (this.startTime - this.context.currentTime) * 1000);
  }
}
