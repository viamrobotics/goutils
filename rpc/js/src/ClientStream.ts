import {
  AnyMessage,
  Message,
  MethodInfo,
  ServiceType,
} from '@bufbuild/protobuf';
import { createClientMethodSerializers } from '@connectrpc/connect/protocol';
import { BaseStream } from './BaseStream';
import type { ClientChannel } from './ClientChannel';
import { cloneHeaders } from './dial';
import {
  Metadata,
  PacketMessage,
  RequestHeaders,
  RequestMessage,
  Response,
  ResponseHeaders,
  ResponseMessage,
  ResponseTrailers,
  Stream,
  Strings,
} from './gen/proto/rpc/webrtc/v1/grpc_pb';

// see golang/client_stream.go
const maxRequestMessagePacketDataSize = 16373;

export interface ClientStreamConstructor<
  T extends ClientStream<I, O>,
  I extends Message<I> = AnyMessage,
  O extends Message<O> = AnyMessage,
> {
  // eslint-disable-next-line @typescript-eslint/prefer-function-type -- this works better with ClientChannel
  new (
    channel: ClientChannel,
    stream: Stream,
    onDone: (id: bigint) => void,
    service: ServiceType,
    method: MethodInfo<I, O>,
    header: HeadersInit | undefined
  ): T;
}

/** A ClientStream provides all the facilities needed to invoke and manage a
 * gRPC stream at a low-level. Implementors like UnaryClientStream and StreamClientStream
 * handle the method specific flow of unary/stream operations.
 */
export abstract class ClientStream<
  I extends Message<I> = AnyMessage,
  O extends Message<O> = AnyMessage,
> extends BaseStream {
  protected readonly channel: ClientChannel;
  protected readonly service: ServiceType;
  protected readonly method: MethodInfo<I, O>;
  protected readonly parseMessage: (data: Uint8Array) => O;
  protected readonly requestHeaders: RequestHeaders;

  private headersReceived: boolean = false;
  private trailersReceived: boolean = false;

  protected abstract onHeaders(headers: ResponseHeaders): void;
  protected abstract onTrailers(trailers: ResponseTrailers): void;
  protected abstract onMessage(msgBytes: Uint8Array): void;

  constructor(
    channel: ClientChannel,
    stream: Stream,
    onDone: (id: bigint) => void,
    service: ServiceType,
    method: MethodInfo<I, O>,
    header: HeadersInit | undefined
  ) {
    super(stream, onDone);
    this.channel = channel;
    this.service = service;
    this.method = method;

    const { parse } = createClientMethodSerializers(
      method,
      true,
      undefined,
      undefined
    );
    this.parseMessage = parse;
    const svcMethod = `/${service.typeName}/${method.name}`;
    this.requestHeaders = new RequestHeaders({
      method: svcMethod,
    });
    const metadataProto = fromGRPCMetadata(cloneHeaders(header));
    if (metadataProto) {
      this.requestHeaders.metadata = metadataProto;
    }
  }

  protected startRequest(signal?: AbortSignal) {
    if (signal) {
      signal.onabort = () => {
        this.resetStream();
      };
    }

    try {
      this.channel.writeHeaders(this.grpcStream, this.requestHeaders);
    } catch (error) {
      console.error('error writing headers', error);
      this.closeWithRecvError();
    }
  }

  protected sendMessage(msgBytes?: Uint8Array) {
    if (msgBytes) {
      this.writeMessage(false, msgBytes);
      return;
    }
    this.writeMessage(false, undefined);
  }

  protected resetStream() {
    if (this.closed) {
      return;
    }
    try {
      this.channel.writeReset(this.grpcStream);
    } catch (error) {
      console.error('error writing reset', error);
      this.closeWithRecvError();
    }
  }

  protected writeMessage(eos: boolean, msgBytes?: Uint8Array) {
    try {
      if (!msgBytes || msgBytes.length == 0) {
        const packetMessage = new PacketMessage({
          eom: true,
        });
        const requestMessage = new RequestMessage({
          hasMessage: !!msgBytes,
          packetMessage,
          eos,
        });
        this.channel.writeMessage(this.grpcStream, requestMessage);
        return;
      }

      while (msgBytes.length !== 0) {
        const amountToSend = Math.min(
          msgBytes.length,
          maxRequestMessagePacketDataSize
        );
        const packetMessage = new PacketMessage();
        packetMessage.data = msgBytes.slice(0, amountToSend);
        msgBytes = msgBytes.slice(amountToSend);
        if (msgBytes.length === 0) {
          packetMessage.eom = true;
        }
        const requestMessage = new RequestMessage({
          hasMessage: !!msgBytes,
          packetMessage,
          eos,
        });
        this.channel.writeMessage(this.grpcStream, requestMessage);
      }
    } catch (error) {
      console.error('error writing message', error);
      this.closeWithRecvError();
    }
  }

  public onResponse(resp: Response) {
    switch (resp.type.case) {
      case 'headers':
        if (this.headersReceived) {
          console.error(
            `invariant: headers already received for ${this.grpcStream.id}`
          );
          return;
        }
        if (this.trailersReceived) {
          console.error(
            `invariant: headers received after trailers for ${this.grpcStream.id}`
          );
          return;
        }
        this.processHeaders(resp.type.value);
        break;
      case 'message':
        if (!this.headersReceived) {
          console.error(
            `invariant: headers not yet received for ${this.grpcStream.id}`
          );
          return;
        }
        if (this.trailersReceived) {
          console.error(
            `invariant: headers received after trailers for ${this.grpcStream.id}`
          );
          return;
        }
        this.processMessage(resp.type.value);
        break;
      case 'trailers':
        this.processTrailers(resp.type.value);
        break;
      default:
        console.error('unknown response type', resp.type.case);
        break;
    }
  }

  private processHeaders(headers: ResponseHeaders) {
    this.headersReceived = true;
    if (!this.onHeaders) {
      throw new Error('invariant: onHeaders unset');
    }
    this.onHeaders(headers);
  }

  private processMessage(msg: ResponseMessage) {
    if (!this.onMessage) {
      throw new Error('invariant: onMessage unset');
    }
    if (!msg.packetMessage) {
      return;
    }
    const result = super.processPacketMessage(msg.packetMessage);
    if (!result) {
      return;
    }
    this.onMessage(result);
  }

  private processTrailers(trailers: ResponseTrailers) {
    this.trailersReceived = true;
    if (!this.onTrailers) {
      throw new Error('invariant: onTrailers unset');
    }

    let statusCode;
    const { status } = trailers;
    if (status) {
      statusCode = status.code;
    } else {
      statusCode = 0;
    }

    this.onTrailers(trailers);
    if (statusCode === 0) {
      this.closeWithRecvError();
      return;
    }
    this.closeWithRecvError();
  }
}

// Needs testing
// from https://github.com/jsmouret/grpc-over-webrtc/blob/45cd6d6cf516e78b1e262ea7aa741bc7a7a93dbc/client-improbable/src/grtc/webrtcclient.ts#L7
const fromGRPCMetadata = (headers?: Headers): Metadata | undefined => {
  if (!headers) {
    return undefined;
  }
  const result = new Metadata({
    md: Object.fromEntries(
      [...headers.entries()].map(([key, value]) => [
        key,
        new Strings({ values: [value] }),
      ])
    ),
  });

  return Object.keys(result.md).length > 0 ? result : undefined;
};

// Needs testing
export const toGRPCMetadata = (metadata?: Metadata): Headers => {
  const headers = Object.entries(metadata?.md ?? {}).flatMap(
    ([key, { values }]) => values.map<[string, string]>((value) => [key, value])
  );
  return new Headers(headers);
};
