// package: proto.rpc.v1
// file: proto/rpc/v1/auth.proto

import * as proto_rpc_v1_auth_pb from "../../../proto/rpc/v1/auth_pb";
import {grpc} from "@improbable-eng/grpc-web";

type AuthServiceAuthenticate = {
  readonly methodName: string;
  readonly service: typeof AuthService;
  readonly requestStream: false;
  readonly responseStream: false;
  readonly requestType: typeof proto_rpc_v1_auth_pb.AuthenticateRequest;
  readonly responseType: typeof proto_rpc_v1_auth_pb.AuthenticateResponse;
};

export class AuthService {
  static readonly serviceName: string;
  static readonly Authenticate: AuthServiceAuthenticate;
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

export class AuthServiceClient {
  readonly serviceHost: string;

  constructor(serviceHost: string, options?: grpc.RpcOptions);
  authenticate(
    requestMessage: proto_rpc_v1_auth_pb.AuthenticateRequest,
    metadata: grpc.Metadata,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_v1_auth_pb.AuthenticateResponse|null) => void
  ): UnaryResponse;
  authenticate(
    requestMessage: proto_rpc_v1_auth_pb.AuthenticateRequest,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_v1_auth_pb.AuthenticateResponse|null) => void
  ): UnaryResponse;
}

