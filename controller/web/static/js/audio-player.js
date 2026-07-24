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
    this.master = null;
    this.sampleRate = 0;
    this.nextStartTime = 0;
    this.sources = new Set();
    this.chunks = [];
  }

  async begin(sampleRate, keep = true) {
    await this.stop();
    this.sampleRate = sampleRate;
    if (keep) this.chunks = [];
    this.context = new AudioContext();
    this.master = this.context.createGain();
    this.master.connect(this.context.destination);
    await this.context.resume();
    this.nextStartTime = this.context.currentTime + 0.02;
  }

  push(encoded, keep = true) {
    if (!this.context || !this.sampleRate) return;
    if (keep) this.chunks.push(encoded);
    const samples = decodePCM(encoded);
    const buffer = this.context.createBuffer(1, samples.length, this.sampleRate);
    buffer.copyToChannel(samples, 0);
    const source = this.context.createBufferSource();
    const gain = this.context.createGain();
    source.buffer = buffer;
    source.connect(gain);
    gain.connect(this.master);
    const when = Math.max(this.nextStartTime, this.context.currentTime + 0.02);
    const fade = Math.min(0.005, buffer.duration / 2);
    gain.gain.setValueAtTime(0, when);
    gain.gain.linearRampToValueAtTime(1, when + fade);
    gain.gain.setValueAtTime(1, when + buffer.duration - fade);
    gain.gain.linearRampToValueAtTime(0, when + buffer.duration);
    source.start(when);
    this.nextStartTime = when + buffer.duration;
    this.sources.add(source);
    source.addEventListener("ended", () => {
      this.sources.delete(source);
      gain.disconnect();
    });
  }

  async stop() {
    const context = this.context;
    const master = this.master;
    const sources = [...this.sources];
    this.context = null;
    this.master = null;
    this.sources.clear();
    this.nextStartTime = 0;
    if (!context) return;
    const now = context.currentTime;
    if (master) {
      master.gain.cancelScheduledValues(now);
      master.gain.setValueAtTime(master.gain.value, now);
      master.gain.linearRampToValueAtTime(0, now + 0.01);
    }
    for (const source of sources) {
      try { source.stop(now + 0.01); } catch { /* already ended */ }
    }
    await new Promise((resolve) => setTimeout(resolve, 12));
    master?.disconnect();
    await context.close();
  }

  async replay() {
    const chunks = [...this.chunks];
    await this.begin(this.sampleRate, false);
    for (const chunk of chunks) this.push(chunk, false);
  }

  remainingMS() {
    if (!this.context) return 0;
    return Math.max(0, (this.nextStartTime - this.context.currentTime) * 1000);
  }
}
