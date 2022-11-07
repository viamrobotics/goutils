import type { ProtobufMessage } from "@improbable-eng/grpc-web/dist/typings/message";
export declare class BaseChannel {
    readonly ready: Promise<unknown>;
    private readonly peerConn;
    private readonly dataChannel;
    private pResolve;
    private pReject;
    private closed;
    private closedReason?;
    protected maxDataChannelSize: number;
    constructor(peerConn: RTCPeerConnection, dataChannel: RTCDataChannel);
    close(): void;
    isClosed(): boolean;
    isClosedReason(): Error | undefined;
    protected closeWithReason(err?: Error): void;
    private onChannelOpen;
    private onChannelClose;
    private onChannelError;
    protected write(msg: ProtobufMessage): void;
}
