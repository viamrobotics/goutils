import type { Message, PartialMessage } from '@bufbuild/protobuf';
import type {
  ContextValues,
  StreamRequest,
  StreamResponse,
} from '@connectrpc/connect';
import { createContextValues } from '@connectrpc/connect';
import {
  createWritableIterable,
  runStreamingCall,
} from '@connectrpc/connect/protocol';
import { ClientStream, toGRPCMetadata } from './client-stream';
import {
  ResponseHeaders,
  ResponseTrailers,
} from './gen/proto/rpc/webrtc/v1/grpc_pb';

export class StreamClientStream<
  I extends Message<I>,
  O extends Message<O>,
> extends ClientStream<I, O> {
  private awaitingHeadersResult?: {
    success: (value: Headers) => void;
    failure: (reason?: unknown) => void;
  };

  private gotHeaders = false;

  // trailers will be written to later
  private readonly respStream = createWritableIterable<O>();
  private readonly trailers: Headers = new Headers();
  private respStreamQueue?: Promise<void>;

  public async run(
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    input: AsyncIterable<PartialMessage<I>>,
    contextValues?: ContextValues
  ): Promise<StreamResponse<I, O>> {
    const req = {
      stream: true as const,
      url: '',
      init: {},
      service: this.service,
      method: this.method,
      header: new Headers(),
      contextValues: contextValues ?? createContextValues(),
      message: input,
    };
    type optParams = Parameters<typeof runStreamingCall<I, O>>[0];
    const opt: optParams = {
      req,
      /**
       *  next is what actually kicks off the request. The run call below will
       * ultimately call this for us.
       */
      next: async (
        streamReq: StreamRequest<I, O>
      ): Promise<StreamResponse<I, O>> => {
        const startRequest = new Promise<Headers>((resolve, reject) => {
          this.awaitingHeadersResult = {
            success: resolve,
            failure: reject,
          };
          this.startRequest();
          this.sendMessages(streamReq.message).catch((error) => {
            console.error('error sending streaming message', error);
            this.closeWithRecvError();
          });
        });

        const headers = await startRequest;

        return {
          ...streamReq,
          header: headers,
          trailer: this.trailers,
          message: this.respStream,
        } satisfies StreamResponse<I, O>;
      },
    };
    if (signal) {
      opt.signal = signal;
    }
    if (timeoutMs) {
      opt.timeoutMs = timeoutMs;
    }

    return runStreamingCall<I, O>(opt);
  }

  protected async sendMessages(messages: AsyncIterable<I>) {
    for await (const msg of messages) {
      this.sendMessage(msg.toBinary());
    }
    // end of messages
    this.writeMessage(true, undefined);
  }

  protected onHeaders(respHeaders: ResponseHeaders): void {
    this.gotHeaders = true;
    this.awaitingHeadersResult?.success(toGRPCMetadata(respHeaders.metadata));
  }

  protected onTrailers(respTrailers: ResponseTrailers): void {
    if (respTrailers.metadata?.md) {
      for (const key in respTrailers.metadata.md) {
        if (Object.hasOwn(respTrailers.metadata.md, key)) {
          const value = respTrailers.metadata.md[key];
          for (const val of value?.values ?? []) {
            this.trailers.append(key, val);
          }
        }
      }
    }
    this.respStream.close();

    if (!respTrailers.status || respTrailers.status.code === 0) {
      if (this.gotHeaders) {
        return;
      }
      this.awaitingHeadersResult?.success(new Headers());
      return;
    }
    if (this.gotHeaders) {
      // nothing to fail here
      return;
    }
    this.awaitingHeadersResult?.failure(respTrailers.status.message);
  }

  protected onMessage(msgBytes: Uint8Array) {
    const msg = this.parseMessage(msgBytes);
    this.respStreamQueue = this.respStreamQueue
      ? this.respStreamQueue.then(async () => this.respStream.write(msg))
      : this.respStream.write(msg);
    this.respStreamQueue.catch((error) => {
      console.error(
        `error pushing received message into stream; failing: ${error}`
      );
      this.resetStream();
    });
  }
}
