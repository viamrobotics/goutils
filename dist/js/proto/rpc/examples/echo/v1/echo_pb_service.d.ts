// package: proto.rpc.examples.echo.v1
// file: proto/rpc/examples/echo/v1/echo.proto

import * as proto_rpc_examples_echo_v1_echo_pb from "../../../../../proto/rpc/examples/echo/v1/echo_pb";
import {grpc} from "@improbable-eng/grpc-web";

type EchoServiceEcho = {
  readonly methodName: string;
  readonly service: typeof EchoService;
  readonly requestStream: false;
  readonly responseStream: false;
  readonly requestType: typeof proto_rpc_examples_echo_v1_echo_pb.EchoRequest;
  readonly responseType: typeof proto_rpc_examples_echo_v1_echo_pb.EchoResponse;
};

type EchoServiceEchoMultiple = {
  readonly methodName: string;
  readonly service: typeof EchoService;
  readonly requestStream: false;
  readonly responseStream: true;
  readonly requestType: typeof proto_rpc_examples_echo_v1_echo_pb.EchoMultipleRequest;
  readonly responseType: typeof proto_rpc_examples_echo_v1_echo_pb.EchoMultipleResponse;
};

type EchoServiceEchoBiDi = {
  readonly methodName: string;
  readonly service: typeof EchoService;
  readonly requestStream: true;
  readonly responseStream: true;
  readonly requestType: typeof proto_rpc_examples_echo_v1_echo_pb.EchoBiDiRequest;
  readonly responseType: typeof proto_rpc_examples_echo_v1_echo_pb.EchoBiDiResponse;
};

export class EchoService {
  static readonly serviceName: string;
  static readonly Echo: EchoServiceEcho;
  static readonly EchoMultiple: EchoServiceEchoMultiple;
  static readonly EchoBiDi: EchoServiceEchoBiDi;
}

export type ServiceError = { message: string, code: number; metadata: grpc.Metadata }
export type Status = { details: string, code: number; metadata: grpc.Metadata }

interface UnaryResponse {
  cancel(): void;
}
interface ResponseStream<T> {
  cancel(): void;
  on(type: 'data', handler: (message: T) => void): ResponseStream<T>;
  on(type: 'end', handler: (status?: Status) => void): ResponseStream<T>;
  on(type: 'status', handler: (status: Status) => void): ResponseStream<T>;
}
interface RequestStream<T> {
  write(message: T): RequestStream<T>;
  end(): void;
  cancel(): void;
  on(type: 'end', handler: (status?: Status) => void): RequestStream<T>;
  on(type: 'status', handler: (status: Status) => void): RequestStream<T>;
}
interface BidirectionalStream<ReqT, ResT> {
  write(message: ReqT): BidirectionalStream<ReqT, ResT>;
  end(): void;
  cancel(): void;
  on(type: 'data', handler: (message: ResT) => void): BidirectionalStream<ReqT, ResT>;
  on(type: 'end', handler: (status?: Status) => void): BidirectionalStream<ReqT, ResT>;
  on(type: 'status', handler: (status: Status) => void): BidirectionalStream<ReqT, ResT>;
}

export class EchoServiceClient {
  readonly serviceHost: string;

  constructor(serviceHost: string, options?: grpc.RpcOptions);
  echo(
    requestMessage: proto_rpc_examples_echo_v1_echo_pb.EchoRequest,
    metadata: grpc.Metadata,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_examples_echo_v1_echo_pb.EchoResponse|null) => void
  ): UnaryResponse;
  echo(
    requestMessage: proto_rpc_examples_echo_v1_echo_pb.EchoRequest,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_examples_echo_v1_echo_pb.EchoResponse|null) => void
  ): UnaryResponse;
  echoMultiple(requestMessage: proto_rpc_examples_echo_v1_echo_pb.EchoMultipleRequest, metadata?: grpc.Metadata): ResponseStream<proto_rpc_examples_echo_v1_echo_pb.EchoMultipleResponse>;
  echoBiDi(metadata?: grpc.Metadata): BidirectionalStream<proto_rpc_examples_echo_v1_echo_pb.EchoBiDiRequest, proto_rpc_examples_echo_v1_echo_pb.EchoBiDiResponse>;
}

