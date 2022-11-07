import type { grpc } from "@improbable-eng/grpc-web";
import { BaseChannel } from "./BaseChannel";
import { ClientStream } from "./ClientStream";
import { ConnectionClosedError } from "./errors";
import { Request, RequestHeaders, RequestMessage, Response, Stream } from "./gen/proto/rpc/webrtc/v1/grpc_pb";

// MaxStreamCount is the max number of streams a channel can have.
let MaxStreamCount = 256;

interface activeClienStream {
    cs: ClientStream;
}

export class ClientChannel extends BaseChannel {
    private streamIDCounter = 0;
    private readonly streams: Record<number, activeClienStream> = {}; 

    constructor(pc: RTCPeerConnection, dc: RTCDataChannel) {
        super(pc, dc);
        dc.onmessage = (event: MessageEvent<unknown>) => this.onChannelMessage(event);
        pc.addEventListener("iceconnectionstatechange", () => {
            const state = pc.iceConnectionState;
            if (!(state === "failed" || state === "disconnected" || state === "closed")) {
                return;
            }
            this.onConnectionTerminated();
        });
        dc.addEventListener("close", () => this.onConnectionTerminated());
    }

    public transportFactory(): grpc.TransportFactory {
        return (opts: grpc.TransportOptions) => {
            return this.newStream(this.nextStreamID(), opts);
        }
    }

    private onConnectionTerminated() {
        // we may call this twice but we know closed will be true at this point.
        this.closeWithReason(new ConnectionClosedError("data channel closed"));
        const err = new ConnectionClosedError("connection terminated");
        for (const streamId in this.streams) {
            const stream = this.streams[streamId]!;
            stream.cs.closeWithRecvError(err);
        }
    }

    private onChannelMessage(event: MessageEvent<any>) {
        let resp: Response;
        try {
            resp = Response.deserializeBinary(event.data);
        } catch (e) {
            console.error("error deserializing message", e);
            return;
        }

        const stream = resp.getStream();
        if (stream === undefined) {
            console.error("no stream id; discarding");
            return;
        }

        const id = stream.getId();
        const activeStream = this.streams[id];
        if (activeStream === undefined) {
            console.error("no stream for id; discarding", "id", id);
            return;
        }
        activeStream.cs.onResponse(resp);
    }

    private nextStreamID(): Stream {
        const stream = new Stream();
        stream.setId(this.streamIDCounter++);
        return stream;
    }

    private newStream(stream: Stream, opts: grpc.TransportOptions): ClientStream {
        if (this.isClosed()) {
            throw new ConnectionClosedError("connection closed");
        }
        let activeStream = this.streams[stream.getId()];
        if (activeStream === undefined) {
            if (Object.keys(this.streams).length > MaxStreamCount) {
                throw new Error("stream limit hit");
            }
            const clientStream = new ClientStream(this, stream, (id: number) => this.removeStreamByID(id), opts);
            activeStream = { cs: clientStream };
            this.streams[stream.getId()] = activeStream;
        }
        return activeStream.cs;
    }

    private removeStreamByID(id: number) {
        delete this.streams[id];
    }

    public writeHeaders(stream: Stream, headers: RequestHeaders) {
        const request = new Request();
        request.setStream(stream);
        request.setHeaders(headers);
        this.write(request);
    }

    public writeMessage(stream: Stream, msg: RequestMessage) {
        const request = new Request();
        request.setStream(stream);
        request.setMessage(msg);
        this.write(request);
    }

    public writeReset(stream: Stream) {
        const request = new Request();
        request.setStream(stream);
        request.setRstStream(true);
        this.write(request);
    }
}
