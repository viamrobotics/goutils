// package: proto.rpc.webrtc.v1
// file: proto/rpc/webrtc/v1/signaling.proto

import * as jspb from "google-protobuf";
import * as google_api_annotations_pb from "../../../../google/api/annotations_pb";
import * as google_rpc_status_pb from "../../../../google/rpc/status_pb";

export class ICECandidate extends jspb.Message {
  getCandidate(): string;
  setCandidate(value: string): void;

  hasSdpMid(): boolean;
  clearSdpMid(): void;
  getSdpMid(): string;
  setSdpMid(value: string): void;

  hasSdpmLineIndex(): boolean;
  clearSdpmLineIndex(): void;
  getSdpmLineIndex(): number;
  setSdpmLineIndex(value: number): void;

  hasUsernameFragment(): boolean;
  clearUsernameFragment(): void;
  getUsernameFragment(): string;
  setUsernameFragment(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): ICECandidate.AsObject;
  static toObject(includeInstance: boolean, msg: ICECandidate): ICECandidate.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: ICECandidate, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): ICECandidate;
  static deserializeBinaryFromReader(message: ICECandidate, reader: jspb.BinaryReader): ICECandidate;
}

export namespace ICECandidate {
  export type AsObject = {
    candidate: string,
    sdpMid: string,
    sdpmLineIndex: number,
    usernameFragment: string,
  }
}

export class CallRequest extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  getDisableTrickle(): boolean;
  setDisableTrickle(value: boolean): void;

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
    disableTrickle: boolean,
  }
}

export class CallResponseInitStage extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): CallResponseInitStage.AsObject;
  static toObject(includeInstance: boolean, msg: CallResponseInitStage): CallResponseInitStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: CallResponseInitStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): CallResponseInitStage;
  static deserializeBinaryFromReader(message: CallResponseInitStage, reader: jspb.BinaryReader): CallResponseInitStage;
}

export namespace CallResponseInitStage {
  export type AsObject = {
    sdp: string,
  }
}

export class CallResponseUpdateStage extends jspb.Message {
  hasCandidate(): boolean;
  clearCandidate(): void;
  getCandidate(): ICECandidate | undefined;
  setCandidate(value?: ICECandidate): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): CallResponseUpdateStage.AsObject;
  static toObject(includeInstance: boolean, msg: CallResponseUpdateStage): CallResponseUpdateStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: CallResponseUpdateStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): CallResponseUpdateStage;
  static deserializeBinaryFromReader(message: CallResponseUpdateStage, reader: jspb.BinaryReader): CallResponseUpdateStage;
}

export namespace CallResponseUpdateStage {
  export type AsObject = {
    candidate?: ICECandidate.AsObject,
  }
}

export class CallResponse extends jspb.Message {
  getUuid(): string;
  setUuid(value: string): void;

  hasInit(): boolean;
  clearInit(): void;
  getInit(): CallResponseInitStage | undefined;
  setInit(value?: CallResponseInitStage): void;

  hasUpdate(): boolean;
  clearUpdate(): void;
  getUpdate(): CallResponseUpdateStage | undefined;
  setUpdate(value?: CallResponseUpdateStage): void;

  getStageCase(): CallResponse.StageCase;
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
    uuid: string,
    init?: CallResponseInitStage.AsObject,
    update?: CallResponseUpdateStage.AsObject,
  }

  export enum StageCase {
    STAGE_NOT_SET = 0,
    INIT = 2,
    UPDATE = 3,
  }
}

export class CallUpdateRequest extends jspb.Message {
  getUuid(): string;
  setUuid(value: string): void;

  hasCandidate(): boolean;
  clearCandidate(): void;
  getCandidate(): ICECandidate | undefined;
  setCandidate(value?: ICECandidate): void;

  hasDone(): boolean;
  clearDone(): void;
  getDone(): boolean;
  setDone(value: boolean): void;

  hasError(): boolean;
  clearError(): void;
  getError(): google_rpc_status_pb.Status | undefined;
  setError(value?: google_rpc_status_pb.Status): void;

  getUpdateCase(): CallUpdateRequest.UpdateCase;
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): CallUpdateRequest.AsObject;
  static toObject(includeInstance: boolean, msg: CallUpdateRequest): CallUpdateRequest.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: CallUpdateRequest, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): CallUpdateRequest;
  static deserializeBinaryFromReader(message: CallUpdateRequest, reader: jspb.BinaryReader): CallUpdateRequest;
}

export namespace CallUpdateRequest {
  export type AsObject = {
    uuid: string,
    candidate?: ICECandidate.AsObject,
    done: boolean,
    error?: google_rpc_status_pb.Status.AsObject,
  }

  export enum UpdateCase {
    UPDATE_NOT_SET = 0,
    CANDIDATE = 2,
    DONE = 3,
    ERROR = 4,
  }
}

export class CallUpdateResponse extends jspb.Message {
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): CallUpdateResponse.AsObject;
  static toObject(includeInstance: boolean, msg: CallUpdateResponse): CallUpdateResponse.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: CallUpdateResponse, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): CallUpdateResponse;
  static deserializeBinaryFromReader(message: CallUpdateResponse, reader: jspb.BinaryReader): CallUpdateResponse;
}

export namespace CallUpdateResponse {
  export type AsObject = {
  }
}

export class ICEServer extends jspb.Message {
  clearUrlsList(): void;
  getUrlsList(): Array<string>;
  setUrlsList(value: Array<string>): void;
  addUrls(value: string, index?: number): string;

  getUsername(): string;
  setUsername(value: string): void;

  getCredential(): string;
  setCredential(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): ICEServer.AsObject;
  static toObject(includeInstance: boolean, msg: ICEServer): ICEServer.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: ICEServer, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): ICEServer;
  static deserializeBinaryFromReader(message: ICEServer, reader: jspb.BinaryReader): ICEServer;
}

export namespace ICEServer {
  export type AsObject = {
    urlsList: Array<string>,
    username: string,
    credential: string,
  }
}

export class WebRTCConfig extends jspb.Message {
  clearAdditionalIceServersList(): void;
  getAdditionalIceServersList(): Array<ICEServer>;
  setAdditionalIceServersList(value: Array<ICEServer>): void;
  addAdditionalIceServers(value?: ICEServer, index?: number): ICEServer;

  getDisableTrickle(): boolean;
  setDisableTrickle(value: boolean): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): WebRTCConfig.AsObject;
  static toObject(includeInstance: boolean, msg: WebRTCConfig): WebRTCConfig.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: WebRTCConfig, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): WebRTCConfig;
  static deserializeBinaryFromReader(message: WebRTCConfig, reader: jspb.BinaryReader): WebRTCConfig;
}

export namespace WebRTCConfig {
  export type AsObject = {
    additionalIceServersList: Array<ICEServer.AsObject>,
    disableTrickle: boolean,
  }
}

export class AnswerRequestInitStage extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  hasOptionalConfig(): boolean;
  clearOptionalConfig(): void;
  getOptionalConfig(): WebRTCConfig | undefined;
  setOptionalConfig(value?: WebRTCConfig): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerRequestInitStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerRequestInitStage): AnswerRequestInitStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerRequestInitStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerRequestInitStage;
  static deserializeBinaryFromReader(message: AnswerRequestInitStage, reader: jspb.BinaryReader): AnswerRequestInitStage;
}

export namespace AnswerRequestInitStage {
  export type AsObject = {
    sdp: string,
    optionalConfig?: WebRTCConfig.AsObject,
  }
}

export class AnswerRequestUpdateStage extends jspb.Message {
  hasCandidate(): boolean;
  clearCandidate(): void;
  getCandidate(): ICECandidate | undefined;
  setCandidate(value?: ICECandidate): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerRequestUpdateStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerRequestUpdateStage): AnswerRequestUpdateStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerRequestUpdateStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerRequestUpdateStage;
  static deserializeBinaryFromReader(message: AnswerRequestUpdateStage, reader: jspb.BinaryReader): AnswerRequestUpdateStage;
}

export namespace AnswerRequestUpdateStage {
  export type AsObject = {
    candidate?: ICECandidate.AsObject,
  }
}

export class AnswerRequestDoneStage extends jspb.Message {
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerRequestDoneStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerRequestDoneStage): AnswerRequestDoneStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerRequestDoneStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerRequestDoneStage;
  static deserializeBinaryFromReader(message: AnswerRequestDoneStage, reader: jspb.BinaryReader): AnswerRequestDoneStage;
}

export namespace AnswerRequestDoneStage {
  export type AsObject = {
  }
}

export class AnswerRequestErrorStage extends jspb.Message {
  hasStatus(): boolean;
  clearStatus(): void;
  getStatus(): google_rpc_status_pb.Status | undefined;
  setStatus(value?: google_rpc_status_pb.Status): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerRequestErrorStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerRequestErrorStage): AnswerRequestErrorStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerRequestErrorStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerRequestErrorStage;
  static deserializeBinaryFromReader(message: AnswerRequestErrorStage, reader: jspb.BinaryReader): AnswerRequestErrorStage;
}

export namespace AnswerRequestErrorStage {
  export type AsObject = {
    status?: google_rpc_status_pb.Status.AsObject,
  }
}

export class AnswerRequest extends jspb.Message {
  getUuid(): string;
  setUuid(value: string): void;

  hasInit(): boolean;
  clearInit(): void;
  getInit(): AnswerRequestInitStage | undefined;
  setInit(value?: AnswerRequestInitStage): void;

  hasUpdate(): boolean;
  clearUpdate(): void;
  getUpdate(): AnswerRequestUpdateStage | undefined;
  setUpdate(value?: AnswerRequestUpdateStage): void;

  hasDone(): boolean;
  clearDone(): void;
  getDone(): AnswerRequestDoneStage | undefined;
  setDone(value?: AnswerRequestDoneStage): void;

  hasError(): boolean;
  clearError(): void;
  getError(): AnswerRequestErrorStage | undefined;
  setError(value?: AnswerRequestErrorStage): void;

  getStageCase(): AnswerRequest.StageCase;
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
    uuid: string,
    init?: AnswerRequestInitStage.AsObject,
    update?: AnswerRequestUpdateStage.AsObject,
    done?: AnswerRequestDoneStage.AsObject,
    error?: AnswerRequestErrorStage.AsObject,
  }

  export enum StageCase {
    STAGE_NOT_SET = 0,
    INIT = 2,
    UPDATE = 3,
    DONE = 4,
    ERROR = 5,
  }
}

export class AnswerResponseInitStage extends jspb.Message {
  getSdp(): string;
  setSdp(value: string): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerResponseInitStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerResponseInitStage): AnswerResponseInitStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerResponseInitStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerResponseInitStage;
  static deserializeBinaryFromReader(message: AnswerResponseInitStage, reader: jspb.BinaryReader): AnswerResponseInitStage;
}

export namespace AnswerResponseInitStage {
  export type AsObject = {
    sdp: string,
  }
}

export class AnswerResponseUpdateStage extends jspb.Message {
  hasCandidate(): boolean;
  clearCandidate(): void;
  getCandidate(): ICECandidate | undefined;
  setCandidate(value?: ICECandidate): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerResponseUpdateStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerResponseUpdateStage): AnswerResponseUpdateStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerResponseUpdateStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerResponseUpdateStage;
  static deserializeBinaryFromReader(message: AnswerResponseUpdateStage, reader: jspb.BinaryReader): AnswerResponseUpdateStage;
}

export namespace AnswerResponseUpdateStage {
  export type AsObject = {
    candidate?: ICECandidate.AsObject,
  }
}

export class AnswerResponseDoneStage extends jspb.Message {
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerResponseDoneStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerResponseDoneStage): AnswerResponseDoneStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerResponseDoneStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerResponseDoneStage;
  static deserializeBinaryFromReader(message: AnswerResponseDoneStage, reader: jspb.BinaryReader): AnswerResponseDoneStage;
}

export namespace AnswerResponseDoneStage {
  export type AsObject = {
  }
}

export class AnswerResponseErrorStage extends jspb.Message {
  hasStatus(): boolean;
  clearStatus(): void;
  getStatus(): google_rpc_status_pb.Status | undefined;
  setStatus(value?: google_rpc_status_pb.Status): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): AnswerResponseErrorStage.AsObject;
  static toObject(includeInstance: boolean, msg: AnswerResponseErrorStage): AnswerResponseErrorStage.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: AnswerResponseErrorStage, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): AnswerResponseErrorStage;
  static deserializeBinaryFromReader(message: AnswerResponseErrorStage, reader: jspb.BinaryReader): AnswerResponseErrorStage;
}

export namespace AnswerResponseErrorStage {
  export type AsObject = {
    status?: google_rpc_status_pb.Status.AsObject,
  }
}

export class AnswerResponse extends jspb.Message {
  getUuid(): string;
  setUuid(value: string): void;

  hasInit(): boolean;
  clearInit(): void;
  getInit(): AnswerResponseInitStage | undefined;
  setInit(value?: AnswerResponseInitStage): void;

  hasUpdate(): boolean;
  clearUpdate(): void;
  getUpdate(): AnswerResponseUpdateStage | undefined;
  setUpdate(value?: AnswerResponseUpdateStage): void;

  hasDone(): boolean;
  clearDone(): void;
  getDone(): AnswerResponseDoneStage | undefined;
  setDone(value?: AnswerResponseDoneStage): void;

  hasError(): boolean;
  clearError(): void;
  getError(): AnswerResponseErrorStage | undefined;
  setError(value?: AnswerResponseErrorStage): void;

  getStageCase(): AnswerResponse.StageCase;
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
    uuid: string,
    init?: AnswerResponseInitStage.AsObject,
    update?: AnswerResponseUpdateStage.AsObject,
    done?: AnswerResponseDoneStage.AsObject,
    error?: AnswerResponseErrorStage.AsObject,
  }

  export enum StageCase {
    STAGE_NOT_SET = 0,
    INIT = 2,
    UPDATE = 3,
    DONE = 4,
    ERROR = 5,
  }
}

export class OptionalWebRTCConfigRequest extends jspb.Message {
  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): OptionalWebRTCConfigRequest.AsObject;
  static toObject(includeInstance: boolean, msg: OptionalWebRTCConfigRequest): OptionalWebRTCConfigRequest.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: OptionalWebRTCConfigRequest, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): OptionalWebRTCConfigRequest;
  static deserializeBinaryFromReader(message: OptionalWebRTCConfigRequest, reader: jspb.BinaryReader): OptionalWebRTCConfigRequest;
}

export namespace OptionalWebRTCConfigRequest {
  export type AsObject = {
  }
}

export class OptionalWebRTCConfigResponse extends jspb.Message {
  hasConfig(): boolean;
  clearConfig(): void;
  getConfig(): WebRTCConfig | undefined;
  setConfig(value?: WebRTCConfig): void;

  serializeBinary(): Uint8Array;
  toObject(includeInstance?: boolean): OptionalWebRTCConfigResponse.AsObject;
  static toObject(includeInstance: boolean, msg: OptionalWebRTCConfigResponse): OptionalWebRTCConfigResponse.AsObject;
  static extensions: {[key: number]: jspb.ExtensionFieldInfo<jspb.Message>};
  static extensionsBinary: {[key: number]: jspb.ExtensionFieldBinaryInfo<jspb.Message>};
  static serializeBinaryToWriter(message: OptionalWebRTCConfigResponse, writer: jspb.BinaryWriter): void;
  static deserializeBinary(bytes: Uint8Array): OptionalWebRTCConfigResponse;
  static deserializeBinaryFromReader(message: OptionalWebRTCConfigResponse, reader: jspb.BinaryReader): OptionalWebRTCConfigResponse;
}

export namespace OptionalWebRTCConfigResponse {
  export type AsObject = {
    config?: WebRTCConfig.AsObject,
  }
}

