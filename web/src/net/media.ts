/**
 * LiveKit wrapper — API verified against livekit-client 2.23.0 type definitions
 * (Room.connect, RemoteTrackPublication.setSubscribed, RemoteAudioTrack.setVolume,
 *  LocalParticipant.setMicrophoneEnabled/setCameraEnabled/setScreenShareEnabled,
 *  RoomEvent.*, Track.Source/Kind).
 *
 * The contract's rule (§6): on each `audio` server event, subscribe/unsubscribe
 * remote participants to match the volumes map, and set volume per value.
 */
import {
  LocalAudioTrack,
  LocalVideoTrack,
  RemoteAudioTrack,
  RemoteTrack,
  RemoteTrackPublication,
  Room,
  RoomEvent,
  Track,
} from 'livekit-client';

export type VideoKind = 'camera' | 'screen';

export interface MediaConnectOpts {
  mic: boolean;
  camera: boolean;
  micDeviceId?: string;
  cameraDeviceId?: string;
}

export class MediaManager {
  private room: Room | null = null;
  private lastVolumes: Record<string, number> = {};
  private speakingNow = new Set<string>();
  private levelTimer: number | null = null;
  private analyser: AnalyserNode | null = null;
  private audioCtx: AudioContext | null = null;
  private outputDeviceId: string | null = null;

  onVideoTrack: (identity: string, el: HTMLVideoElement, kind: VideoKind) => void = () => {};
  onVideoLost: (identity: string, kind: VideoKind) => void = () => {};
  onSpeaking: (identity: string, speaking: boolean) => void = () => {};
  /** Local mic level 0..1, throttled — used to drive the `speaking` flag. */
  onLocalLevel: (level: number) => void = () => {};

  get connected(): boolean {
    return this.room !== null;
  }

  async connect(url: string, token: string, opts: MediaConnectOpts): Promise<void> {
    const room = new Room({ adaptiveStream: true, dynacast: true });

    room.on(RoomEvent.TrackSubscribed, (track: RemoteTrack, pub: RemoteTrackPublication, rp) => {
      this.applyToPublication(rp.identity, pub);
      if (track.kind === Track.Kind.Video) {
        const kind: VideoKind = pub.source === Track.Source.ScreenShare ? 'screen' : 'camera';
        const el = track.attach() as HTMLVideoElement;
        this.onVideoTrack(rp.identity, el, kind);
      } else if (track.kind === Track.Kind.Audio) {
        const el = track.attach();
        if (this.outputDeviceId && 'setSinkId' in el) {
          (el as HTMLAudioElement & { setSinkId(id: string): Promise<void> }).setSinkId(this.outputDeviceId).catch(() => {});
        }
      }
    });
    room.on(RoomEvent.TrackUnsubscribed, (track: RemoteTrack, pub: RemoteTrackPublication, rp) => {
      track.detach();
      if (track.kind === Track.Kind.Video) {
        const kind: VideoKind = pub.source === Track.Source.ScreenShare ? 'screen' : 'camera';
        this.onVideoLost(rp.identity, kind);
      }
    });
    room.on(RoomEvent.ParticipantDisconnected, (rp) => {
      if (this.speakingNow.has(rp.identity)) {
        this.speakingNow.delete(rp.identity);
        this.onSpeaking(rp.identity, false);
      }
    });
    room.on(RoomEvent.ActiveSpeakersChanged, (speakers) => {
      const next = new Set(speakers.map((s) => s.identity));
      for (const id of next) {
        if (!this.speakingNow.has(id)) this.onSpeaking(id, true);
      }
      for (const id of this.speakingNow) {
        if (!next.has(id)) this.onSpeaking(id, false);
      }
      this.speakingNow = next;
    });

    await room.connect(url, token);
    this.room = room;

    if (opts.mic) await this.setMic(true, opts.micDeviceId);
    if (opts.camera) await this.setCamera(true, opts.cameraDeviceId);
    this.startLevelMonitor();
  }

  async setMic(on: boolean, deviceId?: string): Promise<void> {
    if (!this.room) return;
    await this.room.localParticipant.setMicrophoneEnabled(
      on,
      deviceId ? { deviceId } : undefined,
    );
    if (on) this.startLevelMonitor();
    else this.stopLevelMonitor();
  }

  async setCamera(on: boolean, deviceId?: string): Promise<void> {
    if (!this.room) return;
    await this.room.localParticipant.setCameraEnabled(on, deviceId ? { deviceId } : undefined);
  }

  async setScreenShare(on: boolean): Promise<void> {
    if (!this.room) return;
    await this.room.localParticipant.setScreenShareEnabled(on);
  }

  /** Switch input devices on already-published tracks. */
  async setDevices(micDeviceId?: string, cameraDeviceId?: string): Promise<void> {
    if (!this.room) return;
    const lp = this.room.localParticipant;
    if (micDeviceId) {
      const pub = lp.getTrackPublication(Track.Source.Microphone);
      const t = pub?.track;
      if (t instanceof LocalAudioTrack) await t.setDeviceId(micDeviceId);
    }
    if (cameraDeviceId) {
      const pub = lp.getTrackPublication(Track.Source.Camera);
      const t = pub?.track;
      if (t instanceof LocalVideoTrack) await t.setDeviceId(cameraDeviceId);
    }
  }

  setOutputDevice(deviceId: string | null): void {
    this.outputDeviceId = deviceId;
  }

  /**
   * CONTRACT §6: for each remote participant NOT in volumes -> unsubscribe;
   * in volumes -> subscribe + setVolume(v). Screen-share tracks count as camera.
   */
  applyAudible(volumes: Record<string, number>): void {
    this.lastVolumes = volumes;
    if (!this.room) return;
    for (const rp of this.room.remoteParticipants.values()) {
      for (const pub of rp.trackPublications.values()) {
        this.applyToPublication(rp.identity, pub as RemoteTrackPublication);
      }
    }
  }

  private applyToPublication(identity: string, pub: RemoteTrackPublication): void {
    const vol = this.lastVolumes[identity];
    if (vol === undefined) {
      pub.setSubscribed(false);
      return;
    }
    pub.setSubscribed(true);
    if (pub.kind === Track.Kind.Audio && pub.track instanceof RemoteAudioTrack) {
      pub.track.setVolume(vol);
    }
  }

  private startLevelMonitor(): void {
    this.stopLevelMonitor();
    const lp = this.room?.localParticipant;
    const pub = lp?.getTrackPublication(Track.Source.Microphone);
    const track = pub?.track;
    if (!(track instanceof LocalAudioTrack)) return;
    try {
      const stream = new MediaStream([track.mediaStreamTrack]);
      const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      this.audioCtx = new Ctx();
      const src = this.audioCtx.createMediaStreamSource(stream);
      this.analyser = this.audioCtx.createAnalyser();
      this.analyser.fftSize = 512;
      src.connect(this.analyser);
      const buf = new Float32Array(this.analyser.fftSize);
      this.levelTimer = window.setInterval(() => {
        if (!this.analyser) return;
        this.analyser.getFloatTimeDomainData(buf);
        let sum = 0;
        for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
        this.onLocalLevel(Math.min(1, Math.sqrt(sum / buf.length) * 3));
      }, 150);
    } catch {
      this.stopLevelMonitor();
    }
  }

  private stopLevelMonitor(): void {
    if (this.levelTimer !== null) {
      window.clearInterval(this.levelTimer);
      this.levelTimer = null;
    }
    this.analyser?.disconnect();
    this.analyser = null;
    if (this.audioCtx) {
      this.audioCtx.close().catch(() => {});
      this.audioCtx = null;
    }
  }

  async disconnect(): Promise<void> {
    this.stopLevelMonitor();
    if (this.room) {
      await this.room.disconnect().catch(() => {});
      this.room = null;
    }
    this.speakingNow.clear();
    this.lastVolumes = {};
  }
}
