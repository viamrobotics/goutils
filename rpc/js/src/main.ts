declare global {
  interface Window { VIAM: any; }
}

export {
  dialDirect,
  dialWebRTC,
  type Credentials,
  type DialOptions,
  type DialWebRTCOptions,
  type WebRTCConnection,
} from './dial';

export { ConnectionClosedError, GRPCError } from './errors';
