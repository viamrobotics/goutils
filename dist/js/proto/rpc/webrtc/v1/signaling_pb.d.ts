// package: proto.rpc.webrtc.v1
// file: proto/rpc/webrtc/v1/signaling.proto

import * as jspb from "google-protobuf";
import * as google_api_annotations_pb from "../../../../google/api/annotations_pb";
import * as google_rpc_status_pb from "../../../../google/rpc/status_pb";

export class CallRequest extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): CallRequest.AsObject;
  static toObject(includeInstance: boolean, msg: CallRequest): CallRequest.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: CallRequest, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): CallRequest;
  static deserializeBinaryFromReader(message: CallRequest, reader: jspb.BinaryReader): CallRequest;
}

export namespace CallRequest {
  export type AsObject = {
    sdp: string,
  }
}

export class CallResponse extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): CallResponse.AsObject;
  static toObject(includeInstance: boolean, msg: CallResponse): CallResponse.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: CallResponse, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): CallResponse;
  static deserializeBinaryFromReader(message: CallResponse, reader: jspb.BinaryReader): CallResponse;
}

export namespace CallResponse {
  export type AsObject = {
    sdp: string,
  }
}

export class AnswerRequest extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerRequest.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerRequest): AnswerRequest.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerRequest, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerRequest;
  static deserializeBinaryFromReader(message: AnswerRequest, reader: jspb.BinaryReader): AnswerRequest;
}

export namespace AnswerRequest {
  export type AsObject = {
    sdp: string,
  }
}

export class AnswerResponse extends jspb.Message {
  hasStatus(): boolean;
  clearStatus(): void;
  getStatus(): google_rpc_status_pb.Status | undefined;
  setStatus(value?: google_rpc_status_pb.Status): void;

  getSdp(): string;
  setSdp(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerResponse.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerResponse): AnswerResponse.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerResponse, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerResponse;
  static deserializeBinaryFromReader(message: AnswerResponse, reader: jspb.BinaryReader): AnswerResponse;
}

export namespace AnswerResponse {
  export type AsObject = {
    status?: google_rpc_status_pb.Status.AsObject,
    sdp: string,
  }
}

