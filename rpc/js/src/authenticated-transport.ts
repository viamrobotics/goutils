import { grpc } from '@improbable-eng/grpc-web';
import { DialOptions } from './dial-options';
import {
  AuthenticateRequest,
  AuthenticateResponse,
  AuthenticateToRequest,
  AuthenticateToResponse,
  Credentials,
} from './gen/proto/rpc/v1/auth_pb';
import {
  AuthService,
  ExternalAuthService,
} from './gen/proto/rpc/v1/auth_pb_service';

export class AuthenticatedTransport implements grpc.Transport {
  protected readonly options: grpc.TransportOptions;
  protected readonly transport: grpc.Transport;
  protected readonly extraMetadata: grpc.Metadata;

  constructor(
    options: grpc.TransportOptions,
    defaultFactory: grpc.TransportFactory,
    extraMetadata: grpc.Metadata
  ) {
    this.options = options;
    this.extraMetadata = extraMetadata;
    this.transport = defaultFactory(options);
  }

  public start(metadata: grpc.Metadata) {
    // This `forEach` is a built-in function for `grpc.Metadata`
    // eslint-disable-next-line unicorn/no-array-for-each
    this.extraMetadata.forEach((key: string, values: string | string[]) => {
      metadata.set(key, values);
    });

    this.transport.start(metadata);
  }

  public sendMessage(msgBytes: Uint8Array) {
    this.transport.sendMessage(msgBytes);
  }

  public finishSend() {
    this.transport.finishSend();
  }

  public cancel() {
    this.transport.cancel();
  }
}

const getMetadataWithBearerToken = (accessToken: string) => {
  const md = new grpc.Metadata();
  md.set('authorization', `Bearer ${accessToken}`);
  return md;
};

const getInternallyAuthenticatedMetadata = async (
  address: string,
  transportFactory: grpc.TransportFactory,
  { authEntity, credentials, externalAuthAddress }: DialOptions
): Promise<grpc.Metadata> => {
  const request = new AuthenticateRequest();
  // eslint-disable-next-line prefer-named-capture-group
  request.setEntity(authEntity ?? address.replace(/^(.*:\/\/)/u, ''));

  const creds = new Credentials();
  creds.setType(credentials?.type ?? '');
  creds.setPayload(credentials?.payload ?? '');
  request.setCredentials(creds);

  let accessToken = '';

  return new Promise((resolve, reject) => {
    grpc.invoke(AuthService.Authenticate, {
      request,
      host: externalAuthAddress ?? address,
      transport: transportFactory,
      onMessage: (message: AuthenticateResponse) => {
        accessToken = message.getAccessToken();
      },
      onEnd: (
        code: grpc.Code,
        msg: string | undefined,
        _trailers: grpc.Metadata
      ) => {
        if (code === grpc.Code.OK) {
          resolve(getMetadataWithBearerToken(accessToken));
        } else {
          reject(msg);
        }
      },
    });
  });
};

const getExternallyAuthenticatedMetadata = async (
  authenticatedMetadata: grpc.Metadata,
  transportFactory: grpc.TransportFactory,
  { externalAuthAddress, externalAuthToEntity }: DialOptions
): Promise<grpc.Metadata> => {
  const request = new AuthenticateToRequest();
  request.setEntity(externalAuthToEntity ?? '');

  let accessToken = '';

  return new Promise((resolve, reject) => {
    grpc.invoke(ExternalAuthService.AuthenticateTo, {
      request,
      host: externalAuthAddress ?? '',
      transport: transportFactory,
      metadata: authenticatedMetadata,
      onMessage: (message: AuthenticateToResponse) => {
        accessToken = message.getAccessToken();
      },
      onEnd: (
        code: grpc.Code,
        msg: string | undefined,
        _trailers: grpc.Metadata
      ) => {
        if (code === grpc.Code.OK) {
          resolve(getMetadataWithBearerToken(accessToken));
        } else {
          reject(msg);
        }
      },
    });
  });
};

const getAuthenticatedMetadata = async (
  address: string,
  transportFactory: grpc.TransportFactory,
  options: DialOptions
): Promise<grpc.Metadata> => {
  // TODO(GOUT-10): handle expiration
  const authenticatedMetadata = options.accessToken
    ? getMetadataWithBearerToken(options.accessToken)
    : await getInternallyAuthenticatedMetadata(
        address,
        transportFactory,
        options
      );

  if (options.externalAuthAddress && options.externalAuthToEntity) {
    const { externalAuthAddress, externalAuthToEntity } = options;
    return getExternallyAuthenticatedMetadata(
      authenticatedMetadata,
      transportFactory,
      { externalAuthAddress, externalAuthToEntity }
    );
  }

  return authenticatedMetadata;
};

export const createAuthenticatedTransportFactory = async (
  address: string,
  defaultFactory: grpc.TransportFactory,
  options: DialOptions
): Promise<grpc.TransportFactory> => {
  const authenticatedMetadata = await getAuthenticatedMetadata(
    address,
    defaultFactory,
    options
  );

  return (opts: grpc.TransportOptions): grpc.Transport => {
    return new AuthenticatedTransport(
      opts,
      defaultFactory,
      authenticatedMetadata
    );
  };
};
