import { grpc } from '@improbable-eng/grpc-web';
import {
  AuthenticatedTransport,
  createAuthenticatedTransportFactory,
} from './authenticated-transport';
import { DialOptions, validateDialOptions } from './dial-options';

export const dialDirect = async (
  address: string,
  options: DialOptions = {}
): Promise<grpc.TransportFactory> => {
  validateDialOptions(options);

  const defaultFactory = (opts: grpc.TransportOptions): grpc.Transport => {
    // eslint-disable-next-line new-cap
    return grpc.CrossBrowserHttpTransport({ withCredentials: false })(opts);
  };

  // Client already has access token with no external auth, skip Authenticate process.
  if (
    options.accessToken &&
    !(options.externalAuthAddress && options.externalAuthToEntity)
  ) {
    const md = new grpc.Metadata();
    md.set('authorization', `Bearer ${options.accessToken}`);
    return (opts: grpc.TransportOptions): grpc.Transport => {
      return new AuthenticatedTransport(opts, defaultFactory, md);
    };
  }

  if (!options.credentials && !options.accessToken) {
    return defaultFactory;
  }

  return createAuthenticatedTransportFactory(address, defaultFactory, options);
};
