// package: proto.rpc.webrtc.v1
// file: proto/rpc/webrtc/v1/signaling.proto

import * as proto_rpc_webrtc_v1_signaling_pb from "../../../../proto/rpc/webrtc/v1/signaling_pb";
import {grpc} from "@improbable-eng/grpc-web";

type SignalingServiceCall = {
  readonly methodName: string;
  readonly service: typeof SignalingService;
  readonly requestStream: false;
  readonly responseStream: true;
  readonly requestType: typeof proto_rpc_webrtc_v1_signaling_pb.CallRequest;
  readonly responseType: typeof proto_rpc_webrtc_v1_signaling_pb.CallResponse;
};

type SignalingServiceCallUpdate = {
  readonly methodName: string;
  readonly service: typeof SignalingService;
  readonly requestStream: false;
  readonly responseStream: false;
  readonly requestType: typeof proto_rpc_webrtc_v1_signaling_pb.CallUpdateRequest;
  readonly responseType: typeof proto_rpc_webrtc_v1_signaling_pb.CallUpdateResponse;
};

type SignalingServiceAnswer = {
  readonly methodName: string;
  readonly service: typeof SignalingService;
  readonly requestStream: true;
  readonly responseStream: true;
  readonly requestType: typeof proto_rpc_webrtc_v1_signaling_pb.AnswerResponse;
  readonly responseType: typeof proto_rpc_webrtc_v1_signaling_pb.AnswerRequest;
};

type SignalingServiceOptionalWebRTCConfig = {
  readonly methodName: string;
  readonly service: typeof SignalingService;
  readonly requestStream: false;
  readonly responseStream: false;
  readonly requestType: typeof proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigRequest;
  readonly responseType: typeof proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigResponse;
};

export class SignalingService {
  static readonly serviceName: string;
  static readonly Call: SignalingServiceCall;
  static readonly CallUpdate: SignalingServiceCallUpdate;
  static readonly Answer: SignalingServiceAnswer;
  static readonly OptionalWebRTCConfig: SignalingServiceOptionalWebRTCConfig;
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

export class SignalingServiceClient {
  readonly serviceHost: string;

  constructor(serviceHost: string, options?: grpc.RpcOptions);
  call(requestMessage: proto_rpc_webrtc_v1_signaling_pb.CallRequest, metadata?: grpc.Metadata): ResponseStream<proto_rpc_webrtc_v1_signaling_pb.CallResponse>;
  callUpdate(
    requestMessage: proto_rpc_webrtc_v1_signaling_pb.CallUpdateRequest,
    metadata: grpc.Metadata,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_webrtc_v1_signaling_pb.CallUpdateResponse|null) => void
  ): UnaryResponse;
  callUpdate(
    requestMessage: proto_rpc_webrtc_v1_signaling_pb.CallUpdateRequest,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_webrtc_v1_signaling_pb.CallUpdateResponse|null) => void
  ): UnaryResponse;
  answer(metadata?: grpc.Metadata): BidirectionalStream<proto_rpc_webrtc_v1_signaling_pb.AnswerResponse, proto_rpc_webrtc_v1_signaling_pb.AnswerRequest>;
  optionalWebRTCConfig(
    requestMessage: proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigRequest,
    metadata: grpc.Metadata,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigResponse|null) => void
  ): UnaryResponse;
  optionalWebRTCConfig(
    requestMessage: proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigRequest,
    callback: (error: ServiceError|null, responseMessage: proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigResponse|null) => void
  ): UnaryResponse;
}

