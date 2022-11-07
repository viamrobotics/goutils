import type { grpc } from "@improbable-eng/grpc-web";
import type { PacketMessage, Stream } from "./gen/proto/rpc/webrtc/v1/grpc_pb";

// MaxMessageSize is the maximum size a gRPC message can be.
let MaxMessageSize = 1 << 25;

export class BaseStream {
	protected readonly stream: Stream;
	private readonly onDone: (id: number) => void;
	protected readonly opts: grpc.TransportOptions;
	protected closed: boolean = false;
	private readonly packetBuf: Array<Uint8Array> = [];
	private packetBufSize = 0;
	private err?: Error;

	constructor(stream: Stream, onDone: (id: number) => void, opts: grpc.TransportOptions) {
		this.stream = stream;
		this.onDone = onDone;
		this.opts = opts;
	}

	public closeWithRecvError(err?: Error) {
		if (this.closed) {
			return;
		}
		this.closed = true;
		this.err = err;
		this.onDone(this.stream.getId());
		// pretty sure passing the error does nothing.
		this.opts.onEnd(this.err);
	}

	protected processPacketMessage(msg: PacketMessage): Uint8Array | undefined {
		const data = msg.getData_asU8();
		if (data.length + this.packetBufSize > MaxMessageSize) {
			this.packetBuf.length = 0;
			this.packetBufSize = 0;
			console.error(`message size larger than max ${MaxMessageSize}; discarding`)
			return undefined;
		}
		this.packetBuf.push(data);
		this.packetBufSize += data.length;
		if (msg.getEom()) {
			const data = new Uint8Array(this.packetBufSize);
			let position = 0;
			for (let i = 0; i < this.packetBuf.length; i++) {
				const partialData = this.packetBuf[i]!;
				data.set(partialData, position);
				position += partialData.length;
			}
			this.packetBuf.length = 0;
			this.packetBufSize = 0;
			return data;
		}
		return undefined;
	}
}