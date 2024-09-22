import { Message, PartialMessage } from '@bufbuild/protobuf';
import {
  ContextValues,
  UnaryRequest,
  UnaryResponse,
  createContextValues,
} from '@connectrpc/connect';
import { runUnaryCall } from '@connectrpc/connect/protocol';
import { ClientStream, toGRPCMetadata } from './ClientStream';
import {
  ResponseHeaders,
  ResponseTrailers,
} from './gen/proto/rpc/webrtc/v1/grpc_pb';

export class UnaryClientStream<
  I extends Message<I>,
  O extends Message<O>,
> extends ClientStream<I, O> {
  private result?: {
    success: (value: UnaryResponse<I, O>) => void;
    failure: (reason?: any) => void;
  };

  private headers?: Headers;
  private message?: O;

  public async run(
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    message: PartialMessage<I>,
    contextValues?: ContextValues
  ): Promise<UnaryResponse<I, O>> {
    let req = {
      stream: false as const,
      url: '',
      init: {},
      service: this.service,
      method: this.method,
      header: new Headers(),
      contextValues: contextValues ?? createContextValues(),
      message,
    };
    type optParams = Parameters<typeof runUnaryCall<I, O>>[0];
    let opt: optParams = {
      req,
      // next is what actually kicks off the request. The run call below will
      // ultimately call this for us.
      next: async (req: UnaryRequest<I, O>): Promise<UnaryResponse<I, O>> => {
        return new Promise((resolve, reject) => {
          this.result = { success: resolve, failure: reject };
          this.startRequest();
          this.sendMessage(req.message.toBinary());
        });
      },
    };
    if (signal) {
      opt.signal = signal;
    }
    if (timeoutMs) {
      opt.timeoutMs = timeoutMs;
    }
    return runUnaryCall<I, O>(opt);
  }

  protected onHeaders(headers: ResponseHeaders): void {
    if (this.headers !== undefined) {
      this.result?.failure(
        new Error('invariant: received headers more than once')
      );
      return;
    }
    this.headers = toGRPCMetadata(headers.metadata);
  }

  protected onTrailers(respTrailers: ResponseTrailers): void {
    let trailers = toGRPCMetadata(respTrailers.metadata);
    if (!respTrailers.status || respTrailers.status.code == 0) {
      if (!this.headers) {
        this.result?.failure(
          new Error(
            'invariant: received trailers for successful unary request without headers'
          )
        );
        return;
      }
      if (this.message === undefined) {
        this.result?.failure(
          new Error(
            'invariant: received trailers for successful unary request without message'
          )
        );
        return;
      }
      this.result?.success({
        stream: false,
        header: this.headers,
        message: this.message,
        trailer: trailers,
        service: this.service,
        method: this.method,
      } satisfies UnaryResponse<I, O>);
      return;
    }
    this.result?.failure(respTrailers.status.message);
  }

  protected onMessage(msgBytes: Uint8Array): void {
    if (this.message !== undefined) {
      this.result?.failure(
        new Error('invariant: received two response messages for unary request')
      );
      return;
    }
    this.message = this.parseMessage(msgBytes);
  }
}
