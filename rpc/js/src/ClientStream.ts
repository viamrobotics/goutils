import { AnyMessage, Message, MethodInfo, PartialMessage, ServiceType } from '@bufbuild/protobuf';
import { ContextValues, createContextValues, Transport, UnaryRequest, UnaryResponse } from '@connectrpc/connect';
import { GrpcWebTransportOptions } from '@connectrpc/connect-web';
import { createClientMethodSerializers, runUnaryCall } from '@connectrpc/connect/protocol';
import { BaseStream } from './BaseStream';
import type { ClientChannel } from './ClientChannel';
import { GRPCError } from './errors';
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

export class ClientStream extends BaseStream implements Transport {
  private readonly channel: ClientChannel;
  private headersReceived: boolean = false;
  private trailersReceived: boolean = false;

  constructor(
    channel: ClientChannel,
    stream: Stream,
    onDone: (id: number) => void,
    opts: GrpcWebTransportOptions
  ) {
    super(stream, onDone, opts);
    this.channel = channel;
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
    const { serialize, parse } = createClientMethodSerializers(
      method,
      true,
      this.opts.jsonOptions,
      this.opts.binaryOptions,
    );

    return await runUnaryCall<I, O>({
      signal,
      interceptors: this.opts.interceptors,
      timeoutMs: timeoutMs ?? 0,
      req: {
        stream: false,
        url: '',
        init: {},
        service,
        method,
        header,
        contextValues: contextValues ?? createContextValues(),
        message
      },
      next: async (req: UnaryRequest<I, O>): Promise<UnaryResponse<I, O>> => {
        // TODO(erd): correct?
        const svcMethod = `/${service.typeName}/${method.name}`;
        console.log("METHOD", method);
        const requestHeaders = new RequestHeaders();
        requestHeaders.method = svcMethod;
        const metadataProto = fromGRPCMetadata(header);
        if (metadataProto) {
          requestHeaders.metadata = metadataProto;
        }

        try {
          this.channel.writeHeaders(this.stream, requestHeaders);
        } catch (error) {
          console.error('error writing headers', error);
          this.closeWithRecvError();
        }

        // TODO(erd): around here https://github.com/connectrpc/examples-es/blob/main/react-native/app/custom-transport.ts#L111

        this.sendMessage(serialize(req.message));

        throw "ah";
      },
    });
  }

  public sendMessage(msgBytes?: Uint8Array) {
    // skip frame header bytes
    if (msgBytes) {
      this.writeMessage(false, msgBytes.slice(5));
      return;
    }
    this.writeMessage(false, undefined);
  }

  public resetStream() {
    try {
      this.channel.writeReset(this.stream);
    } catch (error) {
      console.error('error writing reset', error);
      this.closeWithRecvError(error as Error);
    }
  }

  public finishSend() {
    if (!this.opts.methodDefinition.requestStream) {
      return;
    }
    this.writeMessage(true, undefined);
  }

  public cancel() {
    if (this.closed) {
      return;
    }
    this.resetStream();
  }

  private writeMessage(eos: boolean, msgBytes?: Uint8Array) {
    try {
      if (!msgBytes || msgBytes.length == 0) {
        const packet = new PacketMessage();
        packet.eom = true;
        const requestMessage = new RequestMessage();
        requestMessage.hasMessage = !!msgBytes;
        requestMessage.packetMessage = packet;
        requestMessage.eos = eos;
        this.channel.writeMessage(this.stream, requestMessage);
        return;
      }

      while (msgBytes.length !== 0) {
        const amountToSend = Math.min(
          msgBytes.length,
          maxRequestMessagePacketDataSize
        );
        const packet = new PacketMessage();
        packet.data = msgBytes.slice(0, amountToSend);
        msgBytes = msgBytes.slice(amountToSend);
        if (msgBytes.length === 0) {
          packet.eom = true;
        }
        const requestMessage = new RequestMessage();
        requestMessage.hasMessage = !!msgBytes;
        requestMessage.packetMessage = packet;
        requestMessage.eos = eos;
        this.channel.writeMessage(this.stream, requestMessage);
      }
    } catch (error) {
      console.error('error writing message', error);
      this.closeWithRecvError();
    }
  }

  public onResponse(resp: Response) {
    switch (resp.getTypeCase()) {
      case Response.TypeCase.HEADERS:
        if (this.headersReceived) {
          this.closeWithRecvError(new Error('headers already received'));
          return;
        }
        if (this.trailersReceived) {
          this.closeWithRecvError(new Error('headers received after trailers'));
          return;
        }
        this.processHeaders(resp.getHeaders()!);
        break;
      case Response.TypeCase.MESSAGE:
        if (!this.headersReceived) {
          this.closeWithRecvError(new Error('headers not yet received'));
          return;
        }
        if (this.trailersReceived) {
          this.closeWithRecvError(new Error('headers received after trailers'));
          return;
        }
        this.processMessage(resp.getMessage()!);
        break;
      case Response.TypeCase.TRAILERS:
        this.processTrailers(resp.getTrailers()!);
        break;
      default:
        console.error('unknown response type', resp.getTypeCase());
        break;
    }
  }

  private processHeaders(headers: ResponseHeaders) {
    this.headersReceived = true;
    this.opts.onHeaders(toGRPCMetadata(headers.getMetadata()), 200);
  }

  private processMessage(msg: ResponseMessage) {
    const result = super.processPacketMessage(msg.getPacketMessage()!);
    if (!result) {
      return;
    }
    const chunk = new ArrayBuffer(result.length + 5);
    new DataView(chunk, 1, 4).setUint32(0, result.length, false);
    new Uint8Array(chunk, 5).set(result);
    this.opts.onChunk(new Uint8Array(chunk));
  }

  private processTrailers(trailers: ResponseTrailers) {
    this.trailersReceived = true;
    const headers = toGRPCMetadata(trailers.metadata);
    let statusCode, statusMessage;
    const status = trailers.status;
    if (status) {
      statusCode = status.code;
      statusMessage = status.message;
      headers.set('grpc-status', `${status.code}`);
      if (statusMessage !== undefined) {
        headers.set('grpc-message', status.message);
      }
    } else {
      statusCode = 0;
      headers.set('grpc-status', '0');
      statusMessage = '';
    }

    const headerBytes = headersToBytes(headers);
    const chunk = new ArrayBuffer(headerBytes.length + 5);
    new DataView(chunk, 0, 1).setUint8(0, 1 << 7);
    new DataView(chunk, 1, 4).setUint32(0, headerBytes.length, false);
    new Uint8Array(chunk, 5).set(headerBytes);
    this.opts.onChunk(new Uint8Array(chunk));
    if (statusCode === 0) {
      this.closeWithRecvError();
      return;
    }
    this.closeWithRecvError();
  }
}

// from https://github.com/improbable-eng/grpc-web/blob/6fb683f067bd56862c3a510bc5590b955ce46d2a/ts/src/ChunkParser.ts#L22
export function encodeASCII(input: string): Uint8Array {
  const encoded = new Uint8Array(input.length);
  for (let i = 0; i !== input.length; ++i) {
    const charCode = input.charCodeAt(i);
    if (!isValidHeaderAscii(charCode)) {
      throw new Error('Metadata contains invalid ASCII');
    }
    encoded[i] = charCode;
  }
  return encoded;
}

const isAllowedControlChars = (char: number) =>
  char === 0x9 || char === 0xa || char === 0xd;

function isValidHeaderAscii(val: number): boolean {
  return isAllowedControlChars(val) || (val >= 0x20 && val <= 0x7e);
}

function headersToBytes(headers: Headers): Uint8Array {
  let asString = '';
  headers.forEach((key, value) => {
    asString += `${key}: ${value}\r\n`;
  });
  return encodeASCII(asString);
}

// from https://github.com/jsmouret/grpc-over-webrtc/blob/45cd6d6cf516e78b1e262ea7aa741bc7a7a93dbc/client-improbable/src/grtc/webrtcclient.ts#L7
const fromGRPCMetadata = (headers?: Headers): Metadata | undefined => {
  if (!headers) {
    return undefined;
  }
  const result = new Metadata({
    md: {}
  });
  const md = result.md;
  headers.forEach((key, value) => {
    const strings = new Strings();
    strings.values = [value];
    md[key] = strings;
  });
  if (Object.keys(result.md).length === 0) {
    return undefined;
  }
  return result;
};

const toGRPCMetadata = (metadata?: Metadata): Headers => {
  const result = new Headers();
  if (metadata && metadata.md) {
    for (let key in metadata.md) {
      let value = metadata.md[key];
      for (let val in value?.values) {
        result.append(key, val);
      }
    }
  }
  return result;
};
