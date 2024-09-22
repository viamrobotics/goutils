import { Transport } from '@connectrpc/connect';

declare global {
  // eslint-disable-next-line vars-on-top,no-var
  var VIAM: {
    GRPC_TRANSPORT_FACTORY: (opts: any) => Transport;
  };
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
