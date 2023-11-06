import { grpc } from '@improbable-eng/grpc-web';
export interface DialOptions {
  authEntity?: string | undefined;
  credentials?: Credentials | undefined;
  webrtcOptions?: DialWebRTCOptions;
  externalAuthAddress?: string | undefined;
  externalAuthToEntity?: string | undefined;
  accessToken?: string | undefined;
}
export interface DialWebRTCOptions {
  disableTrickleICE: boolean;
  rtcConfig?: RTCConfiguration;
  signalingAuthEntity?: string;
  signalingExternalAuthAddress?: string;
  signalingExternalAuthToEntity?: string;
  signalingCredentials?: Credentials;
  signalingAccessToken?: string;
  additionalSdpFields?: Record<string, string | number>;
}
export interface Credentials {
  type: string;
  payload: string;
}
export declare function dialDirect(
  address: string,
  opts?: DialOptions
): Promise<grpc.TransportFactory>;
interface WebRTCConnection {
  transportFactory: grpc.TransportFactory;
  peerConnection: RTCPeerConnection;
}
export declare function dialWebRTC(
  signalingAddress: string,
  host: string,
  opts?: DialOptions
): Promise<WebRTCConnection>;
export {};
