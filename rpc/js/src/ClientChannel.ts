import {
  AnyMessage,
  Message,
  MethodInfo,
  PartialMessage,
  ServiceType,
} from '@bufbuild/protobuf';
import {
  ContextValues,
  StreamResponse,
  Transport,
  UnaryResponse,
} from '@connectrpc/connect';
import { BaseChannel } from './BaseChannel';
import { ClientStream, ClientStreamConstructor } from './ClientStream';
import { ConnectionClosedError } from './errors';
import {
  Request,
  RequestHeaders,
  RequestMessage,
  Response,
  Stream,
} from './gen/proto/rpc/webrtc/v1/grpc_pb';
import { StreamClientStream } from './StreamClientStream';
import { UnaryClientStream } from './UnaryClientStream';

// MaxStreamCount is the max number of streams a channel can have.
let MaxStreamCount = 256;

interface activeClienStream {
  cs: ClientStream;
}

export class ClientChannel extends BaseChannel implements Transport {
  private streamIDCounter = 0;
  private readonly streams: Record<string, activeClienStream> = {};

  constructor(pc: RTCPeerConnection, dc: RTCDataChannel) {
    super(pc, dc);
    dc.addEventListener('message', (event: MessageEvent<'message'>) => {
      this.onChannelMessage(event);
    });
    pc.addEventListener('iceconnectionstatechange', () => {
      const state = pc.iceConnectionState;
      if (
        !(state === 'failed' || state === 'disconnected' || state === 'closed')
      ) {
        return;
      }
      this.onConnectionTerminated();
    });
    dc.addEventListener('close', () => this.onConnectionTerminated());
  }

  private onConnectionTerminated() {
    // we may call this twice but we know closed will be true at this point.
    this.closeWithReason(new ConnectionClosedError('data channel closed'));
    for (const streamId in this.streams) {
      const stream = this.streams[streamId]!;
      stream.cs.closeWithRecvError();
    }
  }

  private onChannelMessage(event: MessageEvent<any>) {
    let resp = Response.fromBinary(new Uint8Array(event.data as ArrayBuffer));

    const { stream } = resp;
    if (stream === undefined) {
      console.error('no stream id; discarding');
      return;
    }

    const { id } = stream;
    const activeStream = this.streams[id.toString()];
    if (activeStream === undefined) {
      console.error('no stream for id; discarding', 'id', id);
      return;
    }
    activeStream.cs.onResponse(resp);
  }

  private nextStreamID(): Stream {
    return new Stream({
      id: BigInt(this.streamIDCounter++),
    });
  }

  private newStream<
    T extends ClientStream<I, O>,
    I extends Message<I>,
    O extends Message<O>,
  >(
    clientCtor: ClientStreamConstructor<T, I, O>,
    stream: Stream,
    service: ServiceType,
    method: MethodInfo<I, O>,
    header: HeadersInit | undefined
  ): T {
    if (this.isClosed()) {
      throw new ConnectionClosedError('connection closed');
    }
    let activeStream = this.streams[stream.id.toString()];
    if (activeStream !== undefined) {
      throw new Error('invariant: stream should not exist yet');
    }
    if (Object.keys(this.streams).length > MaxStreamCount) {
      throw new Error('stream limit hit');
    }
    const clientStream = new clientCtor(
      this,
      stream,
      (id: bigint) => this.removeStreamByID(id),
      service,
      method,
      header
    );
    activeStream = { cs: clientStream };
    this.streams[stream.id.toString()] = activeStream;
    return clientStream;
  }

  private removeStreamByID(id: bigint) {
    delete this.streams[id.toString()];
  }

  public writeHeaders(stream: Stream, headers: RequestHeaders) {
    this.write(
      new Request({
        stream,
        type: {
          case: 'headers',
          value: headers,
        },
      })
    );
  }

  public writeMessage(stream: Stream, msg: RequestMessage) {
    this.write(
      new Request({
        stream,
        type: {
          case: 'message',
          value: msg,
        },
      })
    );
  }

  public writeReset(stream: Stream) {
    this.write(
      new Request({
        stream,
        type: {
          case: 'rstStream',
          value: true,
        },
      })
    );
  }

  public async unary<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    service: ServiceType,
    method: MethodInfo<I, O>,
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    header: HeadersInit | undefined,
    message: PartialMessage<I>,
    contextValues?: ContextValues
  ): Promise<UnaryResponse<I, O>> {
    return this.newStream<UnaryClientStream<I, O>, I, O>(
      UnaryClientStream<I, O>,
      this.nextStreamID(),
      service,
      method,
      header
    ).run(signal, timeoutMs, message, contextValues);
  }

  public async stream<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    service: ServiceType,
    method: MethodInfo<I, O>,
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    header: HeadersInit | undefined,
    input: AsyncIterable<PartialMessage<I>>,
    contextValues?: ContextValues
  ): Promise<StreamResponse<I, O>> {
    return this.newStream<StreamClientStream<I, O>, I, O>(
      StreamClientStream<I, O>,
      this.nextStreamID(),
      service,
      method,
      header
    ).run(signal, timeoutMs, input, contextValues);
  }
}
