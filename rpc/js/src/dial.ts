import { Message } from '@bufbuild/protobuf';

import type {
  AnyMessage,
  MethodInfo,
  PartialMessage,
  ServiceType,
} from '@bufbuild/protobuf';

import type {
  CallOptions,
  ContextValues,
  StreamResponse,
  Transport,
  UnaryResponse,
} from '@connectrpc/connect';
import { Code, ConnectError, createPromiseClient } from '@connectrpc/connect';
import {
  AuthService,
  ExternalAuthService,
} from './gen/proto/rpc/v1/auth_connect';
import {
  AuthenticateRequest,
  Credentials as PBCredentials,
} from './gen/proto/rpc/v1/auth_pb';
import { SignalingService } from './gen/proto/rpc/webrtc/v1/signaling_connect';
import { WebRTCConfig } from './gen/proto/rpc/webrtc/v1/signaling_pb';
import { newPeerConnectionForClient } from './peer';

import { createGrpcWebTransport } from '@connectrpc/connect-web';
import { SignalingExchange } from './signaling-exchange';

export interface DialOptions {
  authEntity?: string | undefined;
  credentials?: Credentials | undefined;
  webrtcOptions?: DialWebRTCOptions;
  externalAuthAddress?: string | undefined;
  externalAuthToEntity?: string | undefined;

  /**
   * `accessToken` allows a pre-authenticated client to dial with
   * an authorization header. Direct dial will have the access token
   * appended to the "Authorization: Bearer" header. WebRTC dial will
   * appened it to the signaling server communication
   *
   * If enabled, other auth options have no affect. Eg. authEntity, credentials,
   * externalAuthAddress, externalAuthToEntity, webrtcOptions.signalingAccessToken
   */
  accessToken?: string | undefined;

  // set timeout in milliseconds for dialing.
  dialTimeout?: number | undefined;
}

export interface DialWebRTCOptions {
  disableTrickleICE: boolean;
  rtcConfig?: RTCConfiguration;

  // signalingAuthEntity is the entity to authenticate as to the signaler.
  signalingAuthEntity?: string;

  /**
   * signalingExternalAuthAddress is the address to perform external auth yet.
   * This is unlikely to be needed since the signaler is typically in the same
   * place where authentication happens.
   */
  signalingExternalAuthAddress?: string;

  /**
   * signalingExternalAuthToEntity is the entity to authenticate for after
   * externally authenticating.
   * This is unlikely to be needed since the signaler is typically in the same
   * place where authentication happens.
   */
  signalingExternalAuthToEntity?: string;

  // signalingCredentials are used to authenticate the request to the signaling server.
  signalingCredentials?: Credentials;

  /**
   * `signalingAccessToken` allows a pre-authenticated client to dial with
   * an authorization header to the signaling server. This skips the Authenticate()
   * request to the singaling server or external auth but does not skip the
   * AuthenticateTo() request to retrieve the credentials at the external auth
   * endpoint.
   *
   * If enabled, other auth options have no affect. Eg. authEntity, credentials, signalingAuthEntity, signalingCredentials.
   */
  signalingAccessToken?: string;

  // `additionalSDPValues` is a collection of additional SDP values that we want to pass into the connection's call request.
  additionalSdpFields?: Record<string, string | number>;
}

export interface Credentials {
  type: string;
  payload: string;
}

export type TransportFactory = (
  // platform specific
  init: TransportInitOptions
) => Transport;

interface TransportInitOptions {
  baseUrl: string;
}

export const dialDirect = async (
  address: string,
  opts?: DialOptions
): Promise<Transport> => {
  validateDialOptions(opts);
  const createTransport =
    globalThis.VIAM?.GRPC_TRANSPORT_FACTORY ?? createGrpcWebTransport;

  const transportOpts = {
    baseUrl: address,
  };

  // Client already has access token with no external auth, skip Authenticate process.
  if (
    opts?.accessToken &&
    !(opts.externalAuthAddress && opts.externalAuthToEntity)
  ) {
    const headers = new Headers();
    headers.set('authorization', `Bearer ${opts.accessToken}`);
    return new AuthenticatedTransport(transportOpts, createTransport, headers);
  }

  if (!opts || (!opts.credentials && !opts.accessToken)) {
    return createTransport(transportOpts);
  }

  const authFact = await makeAuthenticatedTransportFactory(
    address,
    createTransport,
    opts
  );
  return authFact(transportOpts);
};

const addressCleanupRegex = /^.*:\/\//u;

const makeAuthenticatedTransportFactory = async (
  address: string,
  defaultFactory: TransportFactory,
  opts: DialOptions
): Promise<TransportFactory> => {
  let accessToken = '';
  const getExtraHeaders = async (): Promise<Headers> => {
    const headers = new Headers();
    // TODO(GOUT-10): handle expiration
    if (accessToken === '') {
      let thisAccessToken = '';

      if (!opts.accessToken || opts.accessToken === '') {
        const request = new AuthenticateRequest({
          entity: opts.authEntity ?? address.replace(addressCleanupRegex, ''),
        });
        if (opts.credentials) {
          request.credentials = new PBCredentials({
            type: opts.credentials.type,
            payload: opts.credentials.payload,
          });
        }

        const resolvedAddress = opts.externalAuthAddress ?? address;
        const transport = defaultFactory({ baseUrl: resolvedAddress });
        const authClient = createPromiseClient(AuthService, transport);
        const resp = await authClient.authenticate(request);
        thisAccessToken = resp.accessToken;
      } else {
        thisAccessToken = opts.accessToken;
      }

      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- await race
      if (accessToken === '') {
        accessToken = thisAccessToken;

        if (opts.externalAuthAddress && opts.externalAuthToEntity) {
          const authHeaders = new Headers();
          authHeaders.set('authorization', `Bearer ${accessToken}`);

          thisAccessToken = '';

          const request = new AuthenticateRequest({
            entity: opts.externalAuthToEntity,
          });
          const transport = defaultFactory({
            baseUrl: opts.externalAuthAddress,
          });
          const externalAuthClient = createPromiseClient(
            ExternalAuthService,
            transport
          );
          const resp = await externalAuthClient.authenticateTo(request, {
            headers: authHeaders,
          });
          thisAccessToken = resp.accessToken;
          // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- await race
          if (accessToken === '') {
            accessToken = thisAccessToken;
          }
        }
      }
    }
    headers.set('authorization', `Bearer ${accessToken}`);
    return headers;
  };
  const extraMd = await getExtraHeaders();
  return (transportOpts: TransportInitOptions): Transport => {
    return new AuthenticatedTransport(transportOpts, defaultFactory, extraMd);
  };
};

class AuthenticatedTransport implements Transport {
  protected readonly transport: Transport;
  protected readonly extraHeaders: Headers;

  constructor(
    opts: TransportInitOptions,
    defaultFactory: TransportFactory,
    extraHeaders: Headers
  ) {
    this.extraHeaders = extraHeaders;
    this.transport = defaultFactory(opts);
  }

  public async unary<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    service: ServiceType,
    method: MethodInfo<I, O>,
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    header: HeadersInit | undefined,
    message: PartialMessage<I>,
    contextValues?: ContextValues
  ): Promise<UnaryResponse<I, O>> {
    const newHeaders = cloneHeaders(header);
    for (const [key, value] of this.extraHeaders) {
      newHeaders.set(key, value);
    }
    return this.transport.unary(
      service,
      method,
      signal,
      timeoutMs,
      newHeaders,
      message,
      contextValues
    );
  }

  public async stream<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    service: ServiceType,
    method: MethodInfo<I, O>,
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    header: HeadersInit | undefined,
    input: AsyncIterable<PartialMessage<I>>,
    contextValues?: ContextValues
  ): Promise<StreamResponse<I, O>> {
    const newHeaders = cloneHeaders(header);
    for (const [key, value] of this.extraHeaders) {
      newHeaders.set(key, value);
    }
    return this.transport.stream(
      service,
      method,
      signal,
      timeoutMs,
      newHeaders,
      input,
      contextValues
    );
  }
}

export const cloneHeaders = (headers: HeadersInit | undefined): Headers => {
  const cloned = new Headers();
  if (headers !== undefined) {
    if (Array.isArray(headers)) {
      for (const [key, value] of headers) {
        cloned.append(key, value);
      }
    } else if ('forEach' in headers) {
      if (typeof headers.forEach === 'function') {
        // eslint-disable-next-line unicorn/no-array-for-each
        headers.forEach((value, key) => {
          cloned.append(key, value);
        });
      }
    } else {
      for (const [key, value] of Object.entries<string>(headers)) {
        cloned.append(key, value);
      }
    }
  }
  return cloned;
};

export interface WebRTCConnection {
  transport: Transport;
  peerConnection: RTCPeerConnection;
  dataChannel: RTCDataChannel;
}

const getOptionalWebRTCConfig = async (
  signalingAddress: string,
  callOpts: CallOptions,
  dialOpts?: DialOptions
): Promise<WebRTCConfig> => {
  const optsCopy = { ...dialOpts } as DialOptions;
  const directTransport = await dialDirect(signalingAddress, optsCopy);

  const signalingClient = createPromiseClient(
    SignalingService,
    directTransport
  );
  try {
    const resp = await signalingClient.optionalWebRTCConfig({}, callOpts);
    return resp.config ?? new WebRTCConfig();
  } catch (error) {
    if (error instanceof ConnectError && error.code === Code.Unimplemented) {
      return new WebRTCConfig();
    }
    throw error;
  }
};

/**
 * dialWebRTC makes a connection to given host by signaling with the address provided. A Promise is returned
 * upon successful connection that contains a transport factory to use with gRPC client as well as the WebRTC
 * PeerConnection itself. Care should be taken with the PeerConnection and is currently returned for experimental
 * use.
 * TODO(GOUT-7): figure out decent way to handle reconnect on connection termination
 */
export const dialWebRTC = async (
  signalingAddress: string,
  host: string,
  dialOpts?: DialOptions
): Promise<WebRTCConnection> => {
  const usableSignalingAddress = signalingAddress.replace(/\/$/u, '');
  validateDialOptions(dialOpts);

  /**
   * TODO(RSDK-2836): In general, this logic should be in parity with the golang implementation.
   * https://github.com/viamrobotics/goutils/blob/main/rpc/wrtc_client.go#L160-L175
   */
  const callOpts = {
    headers: {
      'rpc-host': host,
    },
  };

  /**
   * first complete our WebRTC options, gathering any extra information like
   * TURN servers from a cloud server.
   */
  const webrtcOpts = await processWebRTCOpts(
    usableSignalingAddress,
    callOpts,
    dialOpts
  );
  // then derive options specifically for signaling against our target.
  const exchangeOpts = processSignalingExchangeOpts(
    usableSignalingAddress,
    dialOpts
  );

  const { pc, dc } = await newPeerConnectionForClient(
    webrtcOpts.disableTrickleICE,
    webrtcOpts.rtcConfig,
    webrtcOpts.additionalSdpFields
  );
  let successful = false;

  let directTransport: Transport;
  try {
    directTransport = await dialDirect(usableSignalingAddress, exchangeOpts);
  } catch (error) {
    pc.close();
    throw error;
  }

  const signalingClient = createPromiseClient(
    SignalingService,
    directTransport
  );

  const exchange = new SignalingExchange(
    signalingClient,
    callOpts,
    pc,
    dc,
    webrtcOpts
  );
  try {
    // set timeout for dial attempt if a timeout is specified
    if (dialOpts?.dialTimeout) {
      setTimeout(() => {
        if (!successful) {
          exchange.terminate();
        }
      }, dialOpts.dialTimeout);
    }

    const cc = await exchange.doExchange();

    if (dialOpts?.externalAuthAddress) {
      // TODO(GOUT-11): prepare AuthenticateTo here  for client channel.
      // eslint-disable-next-line sonarjs/no-duplicated-branches
    } else if (dialOpts?.credentials?.type) {
      // TODO(GOUT-11): prepare Authenticate here for client channel
    }

    successful = true;
    return {
      transport: cc,
      peerConnection: pc,
      dataChannel: dc,
    };
  } catch (error) {
    console.error('error dialing', error);
    throw error;
  } finally {
    if (!successful) {
      pc.close();
    }
  }
};

const processWebRTCOpts = async (
  signalingAddress: string,
  callOpts: CallOptions,
  dialOpts?: DialOptions
): Promise<DialWebRTCOptions> => {
  // Get TURN servers, if any.
  const config = await getOptionalWebRTCConfig(
    signalingAddress,
    callOpts,
    dialOpts
  );
  const additionalIceServers: RTCIceServer[] = config.additionalIceServers.map(
    (ice) => {
      return {
        urls: ice.urls,
        credential: ice.credential,
        username: ice.username,
      };
    }
  );

  const usableDialOpts = dialOpts ?? {};

  let webrtcOpts: DialWebRTCOptions;
  if (usableDialOpts.webrtcOptions === undefined) {
    // use additional webrtc config as default
    webrtcOpts = {
      disableTrickleICE: config.disableTrickle,
      rtcConfig: {
        iceServers: additionalIceServers,
      },
    };
  } else {
    // RSDK-8715: We deep copy here to avoid mutating the input config's `rtcConfig.iceServers` list.
    webrtcOpts = JSON.parse(
      JSON.stringify(usableDialOpts.webrtcOptions)
    ) as DialWebRTCOptions;
    if (webrtcOpts.rtcConfig === undefined) {
      webrtcOpts.rtcConfig = { iceServers: additionalIceServers };
    } else {
      webrtcOpts.rtcConfig.iceServers = [
        ...(webrtcOpts.rtcConfig.iceServers ?? []),
        ...additionalIceServers,
      ];
    }
  }

  return webrtcOpts;
};

const processSignalingExchangeOpts = (
  signalingAddress: string,
  dialOpts?: DialOptions
) => {
  // replace auth entity and creds
  let optsCopy = dialOpts;
  if (dialOpts) {
    optsCopy = { ...dialOpts } as DialOptions;

    if (!dialOpts.accessToken) {
      optsCopy.authEntity = dialOpts.webrtcOptions?.signalingAuthEntity;
      if (!optsCopy.authEntity) {
        optsCopy.authEntity = optsCopy.externalAuthAddress
          ? dialOpts.externalAuthAddress?.replace(addressCleanupRegex, '')
          : signalingAddress.replace(addressCleanupRegex, '');
      }
      optsCopy.credentials = dialOpts.webrtcOptions?.signalingCredentials;
      optsCopy.accessToken = dialOpts.webrtcOptions?.signalingAccessToken;
    }

    optsCopy.externalAuthAddress =
      dialOpts.webrtcOptions?.signalingExternalAuthAddress;
    optsCopy.externalAuthToEntity =
      dialOpts.webrtcOptions?.signalingExternalAuthToEntity;
  }
  return optsCopy;
};

// eslint-disable-next-line sonarjs/cognitive-complexity -- it is not complex
const validateDialOptions = (opts?: DialOptions) => {
  if (!opts) {
    return;
  }

  if (opts.accessToken && opts.accessToken.length > 0) {
    if (opts.authEntity) {
      throw new Error('cannot set authEntity with accessToken');
    }

    if (opts.credentials) {
      throw new Error('cannot set credentials with accessToken');
    }

    if (opts.webrtcOptions) {
      if (opts.webrtcOptions.signalingAccessToken) {
        throw new Error(
          'cannot set webrtcOptions.signalingAccessToken with accessToken'
        );
      }
      if (opts.webrtcOptions.signalingAuthEntity) {
        throw new Error(
          'cannot set webrtcOptions.signalingAuthEntity with accessToken'
        );
      }
      if (opts.webrtcOptions.signalingCredentials) {
        throw new Error(
          'cannot set webrtcOptions.signalingCredentials with accessToken'
        );
      }
    }
  }

  if (
    opts.webrtcOptions?.signalingAccessToken &&
    opts.webrtcOptions.signalingAccessToken.length > 0
  ) {
    if (opts.webrtcOptions.signalingAuthEntity) {
      throw new Error(
        'cannot set webrtcOptions.signalingAuthEntity with webrtcOptions.signalingAccessToken'
      );
    }
    if (opts.webrtcOptions.signalingCredentials) {
      throw new Error(
        'cannot set webrtcOptions.signalingCredentials with webrtcOptions.signalingAccessToken'
      );
    }
  }
};
