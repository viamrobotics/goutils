import { AnyMessage, Message, MethodInfo, PartialMessage, ServiceType } from '@bufbuild/protobuf';
import { ContextValues, StreamRequest, StreamResponse, Transport, UnaryRequest, UnaryResponse, createContextValues } from '@connectrpc/connect';
import { createClientMethodSerializers, createWritableIterable, runStreamingCall, runUnaryCall } from '@connectrpc/connect/protocol';
import { BaseStream } from './BaseStream';
import type { ClientChannel } from './ClientChannel';
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
  private onHeaders?: (headers: ResponseHeaders) => void;
  private onTrailers?: (trailers: ResponseTrailers) => void;
  private onMessage?: (msgBytes: Uint8Array) => void;

  constructor(
    channel: ClientChannel,
    stream: Stream,
    onDone: (id: number) => void,
  ) {
    super(stream, onDone);
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
    const { parse } = createClientMethodSerializers(
      method,
      true,
      undefined,
      undefined,
    );
    const svcMethod = `/${service.typeName}/${method.name}`;
    const requestHeaders = new RequestHeaders();
    requestHeaders.method = svcMethod;
    const metadataProto = fromGRPCMetadata(header);
    if (metadataProto) {
      requestHeaders.metadata = metadataProto;
    }

    return await runUnaryCall<I, O>({
      signal,
      timeoutMs,
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
        return new Promise((resolve, reject) => {
          let headers: Headers | undefined;
          this.onHeaders = (respHeaders: ResponseHeaders) => {
            headers = toGRPCMetadata(respHeaders.metadata);
          }

          let message: O;
          this.onMessage = (msgBytes: Uint8Array) => {
            if (message !== undefined) {
              reject("invariant: received two response messages for unary request");
              return;
            }
            message = parse(msgBytes);
          }

          this.onTrailers = (respTrailers: ResponseTrailers) => {
            let trailers = toGRPCMetadata(respTrailers.metadata);
            if (!respTrailers.status || respTrailers.status.code == 0) {
              if (!headers) {
                reject("received trailers for successful unary request without headers");
                return;
              }
              if (message === undefined) {
                reject("received trailers for successful unary request without message");
                return;
              }
              resolve({
                stream: false,
                header: headers,
                message: message,
                trailer: trailers,
                service,
                method,
              } satisfies UnaryResponse<I, O>);
              return;
            }
            reject(respTrailers.status.message);
          }

          if (signal) {
            signal.onabort = () => {
              this.resetStream();
            }
          }

          try {
            this.channel.writeHeaders(this.grpcStream, requestHeaders);
          } catch (error) {
            console.error('error writing headers', error);
            this.closeWithRecvError();
          }

          this.sendMessage(req.message.toBinary());
        });
      },
    });
  }

  public async stream<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage
  >(
    service: ServiceType, 
    method: MethodInfo<I, O>, 
    signal: AbortSignal | undefined, 
    timeoutMs: number | undefined, 
    header: Headers, 
    input: AsyncIterable<PartialMessage<I>>, 
    contextValues?: ContextValues,
  ): Promise<StreamResponse<I, O>> {
    const { parse } = createClientMethodSerializers(
      method,
      true,
      undefined,
      undefined,
    );
    const svcMethod = `/${service.typeName}/${method.name}`;
    const requestHeaders = new RequestHeaders();
    requestHeaders.method = svcMethod;
    const metadataProto = fromGRPCMetadata(header);
    if (metadataProto) {
      requestHeaders.metadata = metadataProto;
    }

    return await runStreamingCall<I, O>({
      signal,
      timeoutMs,
      req: {
        stream: true,
        url: '',
        init: {},
        service,
        method,
        header,
        contextValues: contextValues ?? createContextValues(),
        message: input,
      },
      next: async (req: StreamRequest<I, O>): Promise<StreamResponse<I, O>> => {
        const respStream = createWritableIterable<O>();

        let trailers = new Headers(); // will be written to later
        let startRequest = new Promise<Headers>((resolve, reject) => {
          let gotHeaders = false;
          this.onHeaders = (respHeaders: ResponseHeaders) => {
            gotHeaders = true;
            resolve(toGRPCMetadata(respHeaders.metadata));
          }

          this.onMessage = (msgBytes: Uint8Array) => {
            respStream.write(parse(msgBytes));
          }

          this.onTrailers = (respTrailers: ResponseTrailers) => {
            if (respTrailers.metadata && respTrailers.metadata.md) {
              for (let key in respTrailers.metadata.md) {
                let value = respTrailers.metadata.md[key];
                for (let val in value?.values) {
                  trailers.append(key, val);
                }
              }
            }
            respStream.close();

            if (!respTrailers.status || respTrailers.status.code == 0) {
              if (gotHeaders) {
                return;
              }
              resolve(new Headers());
              return;
            }
            if (gotHeaders) {
              // nothing to reject here
              return;
            }
            reject(respTrailers.status.message);
          }

          if (signal) {
            signal.onabort = () => {
              this.resetStream();
            }
          }

          try {
            this.channel.writeHeaders(this.grpcStream, requestHeaders);
          } catch (error) {
            console.error('error writing headers', error);
            this.closeWithRecvError();
          }

          // should we have a way to wait for this to end?
          new Promise(async (resolve, reject) => {
            try {
              for await (const msg of req.message) {
                this.sendMessage(msg.toBinary());
              }
              this.writeMessage(true, undefined);
            } catch (err) {
              reject(err);
            }
            resolve(undefined);
          }).catch((err) => {
            console.error("error sending streaming message", err);
            this.closeWithRecvError();
          });
        });

        const headers = await startRequest;

        return {
          ...req,
          header: headers,
          trailer: trailers,
          message: respStream,
        } satisfies StreamResponse<I, O>;
      },
    });
  }

  private sendMessage(msgBytes?: Uint8Array) {
    if (msgBytes) {
      this.writeMessage(false, msgBytes);
      return;
    }
    this.writeMessage(false, undefined);
  }

  private resetStream() {
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

  private writeMessage(eos: boolean, msgBytes?: Uint8Array) {
    try {
      if (!msgBytes || msgBytes.length == 0) {
        const packet = new PacketMessage();
        packet.eom = true;
        const requestMessage = new RequestMessage();
        requestMessage.hasMessage = !!msgBytes;
        requestMessage.packetMessage = packet;
        requestMessage.eos = eos;
        this.channel.writeMessage(this.grpcStream, requestMessage);
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
        this.channel.writeMessage(this.grpcStream, requestMessage);
      }
    } catch (error) {
      console.error('error writing message', error);
      this.closeWithRecvError();
    }
  }

  public onResponse(resp: Response) {
    switch (resp.type.case) {
      case "headers":
        if (this.headersReceived) {
          console.error(`headers already received for ${this.grpcStream.id}`);
          return;
        }
        if (this.trailersReceived) {
          console.error(`headers received after trailers for ${this.grpcStream.id}`);
          return;
        }
        this.processHeaders(resp.type.value);
        break;
      case "message":
        if (!this.headersReceived) {
          console.error(`headers not yet received for ${this.grpcStream.id}`);
          return;
        }
        if (this.trailersReceived) {
          console.error(`headers received after trailers for ${this.grpcStream.id}`);
          return;
        }
        this.processMessage(resp.type.value);
        break;
      case "trailers":
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
      throw Error("invariant: onHeaders unset");
    }
    this.onHeaders(headers);
  }

  private processMessage(msg: ResponseMessage) {
    if (!this.onMessage) {
      throw Error("invariant: onMessage unset");
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
      throw Error("invariant: onTrailers unset");
    }

    let statusCode;
    const status = trailers.status;
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

