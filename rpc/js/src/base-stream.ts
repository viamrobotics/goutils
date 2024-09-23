import type { PacketMessage, Stream } from './gen/proto/rpc/webrtc/v1/grpc_pb';

// MaxMessageSize (2^25) is the maximum size a gRPC message can be.
const MaxMessageSize = 33_554_432;

export class BaseStream {
  protected readonly grpcStream: Stream;
  private readonly onDone: (id: bigint) => void;
  protected closed = false;
  private readonly packetBuf: Uint8Array[] = [];
  private packetBufSize = 0;

  constructor(grpcStream: Stream, onDone: (id: bigint) => void) {
    this.grpcStream = grpcStream;
    this.onDone = onDone;
  }

  public closeWithRecvError() {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.onDone(this.grpcStream.id);
  }

  protected processPacketMessage(msg: PacketMessage): Uint8Array | undefined {
    const { data } = msg;
    if (data.length + this.packetBufSize > MaxMessageSize) {
      this.packetBuf.length = 0;
      this.packetBufSize = 0;
      console.error(
        `message size larger than max ${MaxMessageSize}; discarding`
      );
      return undefined;
    }
    this.packetBuf.push(data);
    this.packetBufSize += data.length;
    if (msg.eom) {
      const pktData = new Uint8Array(this.packetBufSize);
      let position = 0;
      for (const partialData of this.packetBuf) {
        pktData.set(partialData, position);
        position += partialData.length;
      }
      this.packetBuf.length = 0;
      this.packetBufSize = 0;
      return pktData;
    }
    return undefined;
  }
}
