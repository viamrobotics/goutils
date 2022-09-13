
import { GrpcTransport, GrpcOptions } from '@protobuf-ts/grpc-transport'
import { Request, RequestHeaders, RequestMessage, Response, Stream } from "./gen/proto/rpc/webrtc/v1/grpc";
import { BaseChannel } from "./BaseChannel";
import { ClientStream } from "./ClientStream";

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
		dc.onmessage = (event: MessageEvent<any>) => this.onChannelMessage(event);
	}

	public transportFactory(): GrpcTransport {
		return (opts: GrpcOptions) => {
			return this.newStream(this.nextStreamID(), opts);
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
		const id = `${this.streamIDCounter++}`;
		return { id };
	}

	private newStream(stream: Stream, opts: grpc.TransportOptions): ClientStream {
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
}
