import { AnyMessage, Message, MethodInfo, PartialMessage, ServiceType } from '@bufbuild/protobuf';
import { ContextValues, StreamResponse, Transport, UnaryResponse } from '@connectrpc/connect';
import { GrpcWebTransportOptions } from '@connectrpc/connect-web';
import { BaseChannel } from './BaseChannel';
import { ClientStream } from './ClientStream';
import { ConnectionClosedError } from './errors';
import {
  Request,
  RequestHeaders,
  RequestMessage,
  Response,
  Stream,
} from './gen/proto/rpc/webrtc/v1/grpc_pb';

// MaxStreamCount is the max number of streams a channel can have.
let MaxStreamCount = 256;

interface activeClienStream {
  cs: ClientStream;
}

// TODO(erd): cross-platform
export type TransportFactory = (
  init: GrpcWebTransportOptions
) => Transport

export class ClientChannel extends BaseChannel implements Transport {
  private streamIDCounter = 0;
  private readonly streams: Record<number, activeClienStream> = {};

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
    let resp: Response;
    try {
      resp = Response.fromBinary(
        new Uint8Array(event.data as ArrayBuffer)
      );
    } catch (e) {
      console.error('error deserializing message', e);
      return;
    }

    const stream = resp.stream;
    if (stream === undefined) {
      console.error('no stream id; discarding');
      return;
    }

    const id = stream.id;
    const activeStream = this.streams[Number(id)];
    if (activeStream === undefined) {
      console.error('no stream for id; discarding', 'id', id);
      return;
    }
    activeStream.cs.onResponse(resp);
  }

  private nextStreamID(): Stream {
    const stream = new Stream();
    stream.id = BigInt(this.streamIDCounter++);
    return stream;
  }

  private newStream(
    stream: Stream,
  ): Transport {
    if (this.isClosed()) {
      return new FailingClientStream(
        new ConnectionClosedError('connection closed'),
      );
    }
    let activeStream = this.streams[Number(stream.id)];
    if (activeStream === undefined) {
      if (Object.keys(this.streams).length > MaxStreamCount) {
        return new FailingClientStream(new Error('stream limit hit'));
      }
      const clientStream = new ClientStream(
        this,
        stream,
        (id: number) => this.removeStreamByID(id)
      );
      activeStream = { cs: clientStream };
      this.streams[Number(stream.id)] = activeStream;
    }
    return activeStream.cs;
  }

  private removeStreamByID(id: number) {
    delete this.streams[id];
  }

  public writeHeaders(stream: Stream, headers: RequestHeaders) {
    const request = new Request();
    request.stream = stream;
    request.type = {
      case: "headers",
      value: headers,
    }
    this.write(request);
  }

  public writeMessage(stream: Stream, msg: RequestMessage) {
    const request = new Request();
    request.stream = stream;
    request.type = {
      case: "message",
      value: msg,
    }
    this.write(request);
  }

  public writeReset(stream: Stream) {
    const request = new Request();
    request.stream = stream;
    request.type = {
      case: "rstStream",
      value: true,
    }
    this.write(request);
  }

  public async unary<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    service: ServiceType,
    method: MethodInfo<I, O>,
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    header: Headers,
    message: PartialMessage<I>,
    contextValues?: ContextValues,
  ): Promise<UnaryResponse<I, O>> {
    return this.
      newStream(this.nextStreamID()).
      unary(service, method, signal, timeoutMs, header, message, contextValues);
  }

  public async stream<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage
  >(
    service: ServiceType, 
    method: MethodInfo<I, O>, 
    signal: AbortSignal | undefined, 
    timeoutMs: number | undefined, 
    header: HeadersInit | undefined, 
    input: AsyncIterable<PartialMessage<I>>, 
    contextValues?: ContextValues,
  ): Promise<StreamResponse<I, O>> {
    return this.
      newStream(this.nextStreamID()).
      stream(service, method, signal, timeoutMs, header, input, contextValues);
  }
}

class FailingClientStream implements Transport {
  private readonly err: Error;

  constructor(err: Error) {
    this.err = err;
  }

  public async unary<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    _service: ServiceType,
    _method: MethodInfo<I, O>,
    _signal: AbortSignal | undefined,
    _timeoutMs: number | undefined,
    _header: Headers,
    _message: PartialMessage<I>,
    _contextValues?: ContextValues,
  ): Promise<UnaryResponse<I, O>> {
    throw this.err;
  }

  public async stream<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage
  >(
    _service: ServiceType, 
    _method: MethodInfo<I, O>, 
    _signal: AbortSignal | undefined, 
    _timeoutMs: number | undefined, 
    _header: HeadersInit | undefined, 
    _input: AsyncIterable<PartialMessage<I>>, 
    _contextValues?: ContextValues,
  ): Promise<StreamResponse<I, O>> {
    throw this.err;
  }
}
