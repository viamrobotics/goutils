import type { grpc } from "@improbable-eng/grpc-web";
import type { PacketMessage, Stream } from "./gen/proto/rpc/webrtc/v1/grpc_pb";
export declare class BaseStream {
    protected readonly stream: Stream;
    private readonly onDone;
    protected readonly opts: grpc.TransportOptions;
    protected closed: boolean;
    private readonly packetBuf;
    private packetBufSize;
    private err?;
    constructor(stream: Stream, onDone: (id: number) => void, opts: grpc.TransportOptions);
    closeWithRecvError(err?: Error): void;
    protected processPacketMessage(msg: PacketMessage): Uint8Array | undefined;
}
