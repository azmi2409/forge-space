/**
 * WebSocket client for the realtime protocol (CONTRACT §4).
 * Message shapes are the contract's — nothing is invented here.
 */
import type { ChatScope, ClientMsg, ServerMsg, WbPoint } from '../game/types';

export class GameSocket {
  private ws: WebSocket | null = null;
  private intentionalClose = false;

  onMessage: (m: ServerMsg) => void = () => {};
  onClose: (intentional: boolean) => void = () => {};
  onOpen: () => void = () => {};

  constructor(
    private wsUrl: string,
    private ticket: string,
  ) {}

  connect(): void {
    this.intentionalClose = false;
    const sep = this.wsUrl.includes('?') ? '&' : '?';
    this.ws = new WebSocket(`${this.wsUrl}${sep}ticket=${encodeURIComponent(this.ticket)}`);
    this.ws.onopen = () => this.onOpen();
    this.ws.onmessage = (ev: MessageEvent) => {
      try {
        const msg = JSON.parse(String(ev.data)) as ServerMsg;
        this.onMessage(msg);
      } catch {
        // malformed frame from server — ignore, server sends typed errors for bad input
      }
    };
    this.ws.onclose = () => {
      this.ws = null;
      this.onClose(this.intentionalClose);
    };
    this.ws.onerror = () => {
      // onclose follows; nothing to do here
    };
  }

  get connected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  private send(m: ClientMsg): void {
    if (this.connected && this.ws) this.ws.send(JSON.stringify(m));
  }

  sendInput(seq: number, dx: -1 | 0 | 1, dy: -1 | 0 | 1): void {
    this.send({ t: 'input', seq, dx, dy });
  }
  sendChat(scope: ChatScope, to: string | undefined, text: string): void {
    this.send({ t: 'chat', scope, to, text });
  }
  sendInteract(object_id: string): void {
    this.send({ t: 'interact', object_id });
  }
  sendPing(at: number): void {
    this.send({ t: 'ping', at });
  }
  sendWbStroke(object_id: string, stroke: { color: string; width: number; points: WbPoint[] }): void {
    this.send({ t: 'wb_stroke', object_id, stroke });
  }
  sendWbClear(object_id: string): void {
    this.send({ t: 'wb_clear', object_id });
  }

  close(): void {
    this.intentionalClose = true;
    this.ws?.close();
    this.ws = null;
  }
}
