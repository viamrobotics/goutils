import type { Message } from '@bufbuild/protobuf';
import { ConnectionClosedError } from './connection-closed-error';

export class BaseChannel {
  public readonly ready: Promise<unknown>;

  private readonly peerConn: RTCPeerConnection;
  private readonly dataChannel: RTCDataChannel;
  private pResolve: ((value: unknown) => void) | undefined;
  private pReject: ((reason?: unknown) => void) | undefined;

  private closed = false;
  private closedReason: Error | undefined;

  protected maxDataChannelSize = 65_535;

  constructor(peerConn: RTCPeerConnection, dataChannel: RTCDataChannel) {
    this.peerConn = peerConn;
    this.dataChannel = dataChannel;

    this.ready = new Promise<unknown>((resolve, reject) => {
      this.pResolve = resolve;
      this.pReject = reject;
    });

    dataChannel.addEventListener('open', () => this.onChannelOpen());
    dataChannel.addEventListener('close', () => this.onChannelClose());
    dataChannel.addEventListener('error', (ev) => {
      this.onChannelError(ev);
    });

    peerConn.addEventListener('iceconnectionstatechange', () => {
      const state = peerConn.iceConnectionState;
      if (
        !(state === 'failed' || state === 'disconnected' || state === 'closed')
      ) {
        return;
      }
      this.pReject?.(new Error(`ICE connection failed with state: ${state}`));
    });
  }

  public close() {
    this.closeWithReason(undefined);
  }

  public isClosed() {
    return this.closed;
  }

  public isClosedReason() {
    return this.closedReason;
  }

  protected closeWithReason(err?: Error) {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.closedReason = err;
    this.pReject?.(err);
    this.peerConn.close();
  }

  private onChannelOpen() {
    this.pResolve?.(undefined);
  }

  private onChannelClose() {
    this.closeWithReason(new ConnectionClosedError('data channel closed'));
  }

  private onChannelError(ev: Event) {
    console.error('channel error', ev);
    this.closeWithReason(new Error(ev.toString()));
  }

  protected write(msg: Message) {
    this.dataChannel.send(msg.toBinary());
  }
}
