import { grpc } from "@improbable-eng/grpc-web";
import { RequestHeaders, RequestMessage, Stream } from "proto/rpc/webrtc/v1/grpc_pb";
import { BaseChannel } from "./BaseChannel";
export declare class ClientChannel extends BaseChannel {
    private streamIDCounter;
    private readonly streams;
    constructor(pc: RTCPeerConnection, dc: RTCDataChannel);
    transportFactory(): grpc.TransportFactory;
    private onChannelMessage;
    private nextStreamID;
    private newStream;
    private removeStreamByID;
    writeHeaders(stream: Stream, headers: RequestHeaders): void;
    writeMessage(stream: Stream, msg: RequestMessage): void;
}
