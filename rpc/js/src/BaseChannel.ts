import type { ProtobufMessage } from "@improbable-eng/grpc-web/dist/typings/message";
import { ConnectionClosedError } from "./errors";

export class BaseChannel {
    public readonly ready: Promise<unknown>;

    private readonly peerConn: RTCPeerConnection;
    private readonly dataChannel: RTCDataChannel;
    private pResolve: ((value: unknown) => void) | undefined;

    private closed = false;
    private closedReason?: Error;

    protected maxDataChannelSize = 16384;

    constructor(peerConn: RTCPeerConnection, dataChannel: RTCDataChannel) {
        this.peerConn = peerConn;
        this.dataChannel = dataChannel;

        this.ready = new Promise<unknown>(resolve => {
            this.pResolve = resolve;
        })

        dataChannel.onopen = () => this.onChannelOpen();
        dataChannel.onclose = () => this.onChannelClose();
        dataChannel.onerror = (ev: Event) => this.onChannelError(ev as RTCErrorEvent);

        peerConn.oniceconnectionstatechange = () => console.log(peerConn.iceConnectionState);
    }

    public close() {
        this.closeWithReason(undefined);
    }

    public isClosed() {
        return this.closed
    }

    public isClosedReason() {
        return this.closedReason
    }

    protected closeWithReason(err?: Error) {
        if (this.closed) {
            return;
        }
        this.closed = true;
        this.closedReason = err;
        this.peerConn.close();
    }

    private onChannelOpen() {
        this.pResolve?.(undefined);
    }

    private onChannelClose() {
        this.closeWithReason(new ConnectionClosedError("data channel closed"));
    }

    private onChannelError(ev: RTCErrorEvent) {
        console.error("channel error", ev);
        this.closeWithReason(ev.error);
    }

    protected write(msg: ProtobufMessage) {
        this.dataChannel.send(msg.serializeBinary());
    }
}
