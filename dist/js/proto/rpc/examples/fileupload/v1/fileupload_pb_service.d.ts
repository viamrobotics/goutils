// package: proto.rpc.examples.fileupload.v1
// file: proto/rpc/examples/fileupload/v1/fileupload.proto

import * as proto_rpc_examples_fileupload_v1_fileupload_pb from "../../../../../proto/rpc/examples/fileupload/v1/fileupload_pb";
import {grpc} from "@improbable-eng/grpc-web";

type FileUploadServiceUploadFile = {
  readonly methodName: string;
  readonly service: typeof FileUploadService;
  readonly requestStream: true;
  readonly responseStream: true;
  readonly requestType: typeof proto_rpc_examples_fileupload_v1_fileupload_pb.UploadFileRequest;
  readonly responseType: typeof proto_rpc_examples_fileupload_v1_fileupload_pb.UploadFileResponse;
};

export class FileUploadService {
  static readonly serviceName: string;
  static readonly UploadFile: FileUploadServiceUploadFile;
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

export class FileUploadServiceClient {
  readonly serviceHost: string;

  constructor(serviceHost: string, options?: grpc.RpcOptions);
  uploadFile(metadata?: grpc.Metadata): BidirectionalStream<proto_rpc_examples_fileupload_v1_fileupload_pb.UploadFileRequest, proto_rpc_examples_fileupload_v1_fileupload_pb.UploadFileResponse>;
}

