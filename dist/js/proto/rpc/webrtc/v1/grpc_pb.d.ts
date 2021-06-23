// package: proto.rpc.webrtc.v1
// file: proto/rpc/webrtc/v1/grpc.proto

import * as jspb from "google-protobuf";
import * as google_protobuf_duration_pb from "google-protobuf/google/protobuf/duration_pb";
import * as google_rpc_status_pb from "../../../../google/rpc/status_pb";

export class PacketMessage extends jspb.Message {
  getData(): Uint8Array | string;
  getData_asU8(): Uint8Array;
  getData_asB64(): string;
  setData(value: Uint8Array | string): void;

  getEom(): boolean;
  setEom(value: boolean): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): PacketMessage.AsObject;
  static toObject(includeInstance: boolean, msg: PacketMessage): PacketMessage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: PacketMessage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): PacketMessage;
  static deserializeBinaryFromReader(message: PacketMessage, reader: jspb.BinaryReader): PacketMessage;
}

export namespace PacketMessage {
  export type AsObject = {
    data: Uint8Array | string,
    eom: boolean,
  }
}

export class Stream extends jspb.Message {
  getId(): number;
  setId(value: number): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): Stream.AsObject;
  static toObject(includeInstance: boolean, msg: Stream): Stream.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: Stream, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): Stream;
  static deserializeBinaryFromReader(message: Stream, reader: jspb.BinaryReader): Stream;
}

export namespace Stream {
  export type AsObject = {
    id: number,
  }
}

export class Request extends jspb.Message {
  hasStream(): boolean;
  clearStream(): void;
  getStream(): Stream | undefined;
  setStream(value?: Stream): void;

  hasHeaders(): boolean;
  clearHeaders(): void;
  getHeaders(): RequestHeaders | undefined;
  setHeaders(value?: RequestHeaders): void;

  hasMessage(): boolean;
  clearMessage(): void;
  getMessage(): RequestMessage | undefined;
  setMessage(value?: RequestMessage): void;

  getTypeCase(): Request.TypeCase;
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): Request.AsObject;
  static toObject(includeInstance: boolean, msg: Request): Request.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: Request, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): Request;
  static deserializeBinaryFromReader(message: Request, reader: jspb.BinaryReader): Request;
}

export namespace Request {
  export type AsObject = {
    stream?: Stream.AsObject,
    headers?: RequestHeaders.AsObject,
    message?: RequestMessage.AsObject,
  }

  export enum TypeCase {
    TYPE_NOT_SET = 0,
    HEADERS = 2,
    MESSAGE = 3,
  }
}

export class RequestHeaders extends jspb.Message {
  getMethod(): string;
  setMethod(value: string): void;

  hasMetadata(): boolean;
  clearMetadata(): void;
  getMetadata(): Metadata | undefined;
  setMetadata(value?: Metadata): void;

  hasTimeout(): boolean;
  clearTimeout(): void;
  getTimeout(): google_protobuf_duration_pb.Duration | undefined;
  setTimeout(value?: google_protobuf_duration_pb.Duration): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): RequestHeaders.AsObject;
  static toObject(includeInstance: boolean, msg: RequestHeaders): RequestHeaders.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: RequestHeaders, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): RequestHeaders;
  static deserializeBinaryFromReader(message: RequestHeaders, reader: jspb.BinaryReader): RequestHeaders;
}

export namespace RequestHeaders {
  export type AsObject = {
    method: string,
    metadata?: Metadata.AsObject,
    timeout?: google_protobuf_duration_pb.Duration.AsObject,
  }
}

export class RequestMessage extends jspb.Message {
  getHasMessage(): boolean;
  setHasMessage(value: boolean): void;

  hasPacketMessage(): boolean;
  clearPacketMessage(): void;
  getPacketMessage(): PacketMessage | undefined;
  setPacketMessage(value?: PacketMessage): void;

  getEos(): boolean;
  setEos(value: boolean): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): RequestMessage.AsObject;
  static toObject(includeInstance: boolean, msg: RequestMessage): RequestMessage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: RequestMessage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): RequestMessage;
  static deserializeBinaryFromReader(message: RequestMessage, reader: jspb.BinaryReader): RequestMessage;
}

export namespace RequestMessage {
  export type AsObject = {
    hasMessage: boolean,
    packetMessage?: PacketMessage.AsObject,
    eos: boolean,
  }
}

export class Response extends jspb.Message {
  hasStream(): boolean;
  clearStream(): void;
  getStream(): Stream | undefined;
  setStream(value?: Stream): void;

  hasHeaders(): boolean;
  clearHeaders(): void;
  getHeaders(): ResponseHeaders | undefined;
  setHeaders(value?: ResponseHeaders): void;

  hasMessage(): boolean;
  clearMessage(): void;
  getMessage(): ResponseMessage | undefined;
  setMessage(value?: ResponseMessage): void;

  hasTrailers(): boolean;
  clearTrailers(): void;
  getTrailers(): ResponseTrailers | undefined;
  setTrailers(value?: ResponseTrailers): void;

  getTypeCase(): Response.TypeCase;
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): Response.AsObject;
  static toObject(includeInstance: boolean, msg: Response): Response.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: Response, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): Response;
  static deserializeBinaryFromReader(message: Response, reader: jspb.BinaryReader): Response;
}

export namespace Response {
  export type AsObject = {
    stream?: Stream.AsObject,
    headers?: ResponseHeaders.AsObject,
    message?: ResponseMessage.AsObject,
    trailers?: ResponseTrailers.AsObject,
  }

  export enum TypeCase {
    TYPE_NOT_SET = 0,
    HEADERS = 2,
    MESSAGE = 3,
    TRAILERS = 4,
  }
}

export class ResponseHeaders extends jspb.Message {
  hasMetadata(): boolean;
  clearMetadata(): void;
  getMetadata(): Metadata | undefined;
  setMetadata(value?: Metadata): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): ResponseHeaders.AsObject;
  static toObject(includeInstance: boolean, msg: ResponseHeaders): ResponseHeaders.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: ResponseHeaders, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): ResponseHeaders;
  static deserializeBinaryFromReader(message: ResponseHeaders, reader: jspb.BinaryReader): ResponseHeaders;
}

export namespace ResponseHeaders {
  export type AsObject = {
    metadata?: Metadata.AsObject,
  }
}

export class ResponseMessage extends jspb.Message {
  hasPacketMessage(): boolean;
  clearPacketMessage(): void;
  getPacketMessage(): PacketMessage | undefined;
  setPacketMessage(value?: PacketMessage): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): ResponseMessage.AsObject;
  static toObject(includeInstance: boolean, msg: ResponseMessage): ResponseMessage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: ResponseMessage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): ResponseMessage;
  static deserializeBinaryFromReader(message: ResponseMessage, reader: jspb.BinaryReader): ResponseMessage;
}

export namespace ResponseMessage {
  export type AsObject = {
    packetMessage?: PacketMessage.AsObject,
  }
}

export class ResponseTrailers extends jspb.Message {
  hasStatus(): boolean;
  clearStatus(): void;
  getStatus(): google_rpc_status_pb.Status | undefined;
  setStatus(value?: google_rpc_status_pb.Status): void;

  hasMetadata(): boolean;
  clearMetadata(): void;
  getMetadata(): Metadata | undefined;
  setMetadata(value?: Metadata): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): ResponseTrailers.AsObject;
  static toObject(includeInstance: boolean, msg: ResponseTrailers): ResponseTrailers.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: ResponseTrailers, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): ResponseTrailers;
  static deserializeBinaryFromReader(message: ResponseTrailers, reader: jspb.BinaryReader): ResponseTrailers;
}

export namespace ResponseTrailers {
  export type AsObject = {
    status?: google_rpc_status_pb.Status.AsObject,
    metadata?: Metadata.AsObject,
  }
}

export class Strings extends jspb.Message {
  clearValuesList(): void;
  getValuesList(): Array<string>;
  setValuesList(value: Array<string>): void;
  addValues(value: string, index?: number): string;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): Strings.AsObject;
  static toObject(includeInstance: boolean, msg: Strings): Strings.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: Strings, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): Strings;
  static deserializeBinaryFromReader(message: Strings, reader: jspb.BinaryReader): Strings;
}

export namespace Strings {
  export type AsObject = {
    valuesList: Array<string>,
  }
}

export class Metadata extends jspb.Message {
  getMdMap(): jspb.Map<string, Strings>;
  clearMdMap(): void;
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): Metadata.AsObject;
  static toObject(includeInstance: boolean, msg: Metadata): Metadata.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: Metadata, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): Metadata;
  static deserializeBinaryFromReader(message: Metadata, reader: jspb.BinaryReader): Metadata;
}

export namespace Metadata {
  export type AsObject = {
    mdMap: Array<[string, Strings.AsObject]>,
  }
}

