
import { Message } from "@bufbuild/protobuf";

import type {
  AnyMessage,
  MethodInfo,
  PartialMessage,
  ServiceType,
} from "@bufbuild/protobuf";

import type { ContextValues, StreamResponse, Transport, UnaryResponse } from "@connectrpc/connect";
import { Code, ConnectError, createPromiseClient } from "@connectrpc/connect";
import { ClientChannel, TransportFactory } from './ClientChannel';
import { ConnectionClosedError } from './errors';
import { Status } from './gen/google/rpc/status_pb';
import {
  AuthService,
  ExternalAuthService,
} from './gen/proto/rpc/v1/auth_connect';
import {
  AuthenticateRequest,
  Credentials as PBCredentials,
} from './gen/proto/rpc/v1/auth_pb';
import {
  SignalingService
} from './gen/proto/rpc/webrtc/v1/signaling_connect';
import {
  CallRequest,
  CallUpdateRequest,
  ICECandidate,
  WebRTCConfig,
} from './gen/proto/rpc/webrtc/v1/signaling_pb';
import { addSdpFields, newPeerConnectionForClient } from './peer';

import { createGrpcWebTransport, GrpcWebTransportOptions } from "@connectrpc/connect-web";
import { atob, btoa } from './polyfills';

export interface DialOptions {
  authEntity?: string | undefined;
  credentials?: Credentials | undefined;
  webrtcOptions?: DialWebRTCOptions;
  externalAuthAddress?: string | undefined;
  externalAuthToEntity?: string | undefined;

  // `accessToken` allows a pre-authenticated client to dial with
  // an authorization header. Direct dial will have the access token
  // appended to the "Authorization: Bearer" header. WebRTC dial will
  // appened it to the signaling server communication
  //
  // If enabled, other auth options have no affect. Eg. authEntity, credentials,
  // externalAuthAddress, externalAuthToEntity, webrtcOptions.signalingAccessToken
  accessToken?: string | undefined;

  // set timeout in milliseconds for dialing.
  dialTimeout?: number | undefined;
}

export interface DialWebRTCOptions {
  disableTrickleICE: boolean;
  rtcConfig?: RTCConfiguration;

  // signalingAuthEntity is the entity to authenticate as to the signaler.
  signalingAuthEntity?: string;

  // signalingExternalAuthAddress is the address to perform external auth yet.
  // This is unlikely to be needed since the signaler is typically in the same
  // place where authentication happens.
  signalingExternalAuthAddress?: string;

  // signalingExternalAuthToEntity is the entity to authenticate for after
  // externally authenticating.
  // This is unlikely to be needed since the signaler is typically in the same
  // place where authentication happens.
  signalingExternalAuthToEntity?: string;

  // signalingCredentials are used to authenticate the request to the signaling server.
  signalingCredentials?: Credentials;

  // `signalingAccessToken` allows a pre-authenticated client to dial with
  // an authorization header to the signaling server. This skips the Authenticate()
  // request to the singaling server or external auth but does not skip the
  // AuthenticateTo() request to retrieve the credentials at the external auth
  // endpoint.
  //
  // If enabled, other auth options have no affect. Eg. authEntity, credentials, signalingAuthEntity, signalingCredentials.
  signalingAccessToken?: string;

  // `additionalSDPValues` is a collection of additional SDP values that we want to pass into the connection's call request.
  additionalSdpFields?: Record<string, string | number>;
}

export interface Credentials {
  type: string;
  payload: string;
}

// TODO(erd): correctly get grpc-web/node
export async function dialDirect(
  address: string,
  opts?: DialOptions
): Promise<Transport> {
  validateDialOptions(opts);
  let transFact: TransportFactory;
  try {
    transFact = window.VIAM.GRPC_TRANSPORT_FACTORY;
  } catch {
    transFact = createGrpcWebTransport;
  }

  const transportOpts = {
    baseUrl: address
  };

  // Client already has access token with no external auth, skip Authenticate process.
  if (
    opts?.accessToken &&
    !(opts?.externalAuthAddress && opts?.externalAuthToEntity)
  ) {
    const headers = new Headers();
    headers.set('authorization', `Bearer ${opts.accessToken}`);
    return new authenticatedTransport(transportOpts, transFact, headers);
  }

  if (!opts || (!opts?.credentials && !opts?.accessToken)) {
    return transFact(transportOpts);
  }

  const authFact = await makeAuthenticatedTransportFactory(address, transFact, opts);
  return authFact(transportOpts);
}


async function makeAuthenticatedTransportFactory(
  address: string,
  defaultFactory: TransportFactory,
  opts: DialOptions
): Promise<TransportFactory> {
  let accessToken = '';
  const getExtraHeaders = async (): Promise<Headers> => {
    const headers = new Headers();
    // TODO(GOUT-10): handle expiration
    if (accessToken == '') {
      let thisAccessToken = '';

      if (!opts.accessToken || opts.accessToken === '') {
        const request = new AuthenticateRequest();
        request.entity = opts.authEntity ? opts.authEntity : address.replace(/^(.*:\/\/)/, '');
        const creds = new PBCredentials();
        creds.type = opts.credentials?.type!;
        creds.payload = opts.credentials?.payload!;
        request.credentials = creds;

        const resolvedAddress = opts.externalAuthAddress ? opts.externalAuthAddress : address;
        const transport = defaultFactory({ baseUrl: resolvedAddress });
        const authClient = createPromiseClient(AuthService, transport);
        const resp = await authClient.authenticate(request);
        thisAccessToken = resp.accessToken;
      } else {
        thisAccessToken = opts.accessToken;
      }

      accessToken = thisAccessToken;

      if (opts.externalAuthAddress && opts.externalAuthToEntity) {
        const headers = new Headers();
        headers.set('authorization', `Bearer ${accessToken}`);

        thisAccessToken = '';

        const request = new AuthenticateRequest();
        request.entity = opts.externalAuthToEntity;
        const transport = defaultFactory({ baseUrl: opts.externalAuthAddress! });
        const externalAuthClient = createPromiseClient(ExternalAuthService, transport);
        const resp = await externalAuthClient.authenticateTo(request);
        thisAccessToken = resp.accessToken;
        accessToken = thisAccessToken;
      }
    }
    headers.set('authorization', `Bearer ${accessToken}`);
    return headers;
  };
  const extraMd = await getExtraHeaders();
  return (opts: GrpcWebTransportOptions): Transport => {
    return new authenticatedTransport(opts, defaultFactory, extraMd);
  };
}

class authenticatedTransport implements Transport {
  protected readonly opts: GrpcWebTransportOptions;
  protected readonly transport: Transport;
  protected readonly extraHeaders: Headers;

  constructor(
    opts: GrpcWebTransportOptions,
    defaultFactory: TransportFactory,
    extraHeaders: Headers
  ) {
    this.opts = opts;
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
    header: Headers,
    message: PartialMessage<I>,
    contextValues?: ContextValues,
  ): Promise<UnaryResponse<I, O>> {
    this.extraHeaders.forEach((key: string, value: string) => {
      header.set(key, value);
    });
    return this.transport.unary(
      service,
      method,
      signal,
      timeoutMs,
      header,
      message,
      contextValues)
  }

  public async stream<
    I extends Message<I> = AnyMessage,
    O extends Message<O> = AnyMessage,
  >(
    service: ServiceType,
    method: MethodInfo<I, O>,
    signal: AbortSignal | undefined,
    timeoutMs: number | undefined,
    header: Headers,
    input: AsyncIterable<PartialMessage<I>>,
    contextValues?: ContextValues,
  ): Promise<StreamResponse<I, O>> {
    this.extraHeaders.forEach((key: string, value: string) => {
      header.set(key, value);
    });
    return this.transport.stream(
      service,
      method,
      signal,
      timeoutMs,
      header,
      input,
      contextValues)
  }

  
}

export interface WebRTCConnection {
  transport: Transport;
  peerConnection: RTCPeerConnection;
  dataChannel: RTCDataChannel;
}

async function getOptionalWebRTCConfig(
  signalingAddress: string,
  host: string,
  opts?: DialOptions
): Promise<WebRTCConfig> {
  const optsCopy = { ...opts } as DialOptions;
  const directTransport = await dialDirect(signalingAddress, optsCopy);

  const signalingClient = createPromiseClient(SignalingService, directTransport);
  try {
    const resp = await signalingClient.optionalWebRTCConfig({}, {
      headers: {
        'rpc-host': host
      }
    });
    return resp.config ?? new WebRTCConfig();
  } catch (err) {
    if (err instanceof ConnectError) {
      if (err.code == Code.Unimplemented) {
        return new WebRTCConfig();
      }
    }
    throw err;
  }
}

// dialWebRTC makes a connection to given host by signaling with the address provided. A Promise is returned
// upon successful connection that contains a transport factory to use with gRPC client as well as the WebRTC
// PeerConnection itself. Care should be taken with the PeerConnection and is currently returned for experimental
// use.
// TODO(GOUT-7): figure out decent way to handle reconnect on connection termination
// eslint-disable-next-line sonarjs/cognitive-complexity
// eslint-disable-next-line func-style
export async function dialWebRTC(
  signalingAddress: string,
  host: string,
  opts?: DialOptions
): Promise<WebRTCConnection> {
  signalingAddress = signalingAddress.replace(/(\/)$/, '');
  validateDialOptions(opts);

  // TODO(RSDK-2836): In general, this logic should be in parity with the golang implementation.
  // https://github.com/viamrobotics/goutils/blob/main/rpc/wrtc_client.go#L160-L175
  const config = await getOptionalWebRTCConfig(signalingAddress, host, opts);
  const additionalIceServers: RTCIceServer[] = config
    .additionalIceServers.map((ice) => {
      return {
        urls: ice.urls,
        credential: ice.credential,
        username: ice.username,
      };
    });

  if (!opts) {
    opts = {};
  }

  let webrtcOpts: DialWebRTCOptions;
  if (!opts.webrtcOptions) {
    // use additional webrtc config as default
    webrtcOpts = {
      disableTrickleICE: config.disableTrickle,
      rtcConfig: {
        iceServers: additionalIceServers,
      },
    };
  } else {
    // RSDK-8715: We deep copy here to avoid mutating the input config's `rtcConfig.iceServers`
    // list.
    webrtcOpts = JSON.parse(JSON.stringify(opts.webrtcOptions));
    if (!webrtcOpts.rtcConfig) {
      webrtcOpts.rtcConfig = { iceServers: additionalIceServers };
    } else {
      webrtcOpts.rtcConfig.iceServers = [
        ...(webrtcOpts.rtcConfig.iceServers || []),
        ...additionalIceServers,
      ];
    }
  }

  const { pc, dc } = await newPeerConnectionForClient(
    webrtcOpts !== undefined && webrtcOpts.disableTrickleICE,
    webrtcOpts?.rtcConfig,
    webrtcOpts?.additionalSdpFields
  );
  let successful = false;

  try {
    // replace auth entity and creds
    let optsCopy = opts;
    if (opts) {
      optsCopy = { ...opts } as DialOptions;

      if (!opts.accessToken) {
        optsCopy.authEntity = opts?.webrtcOptions?.signalingAuthEntity;
        if (!optsCopy.authEntity) {
          if (optsCopy.externalAuthAddress) {
            optsCopy.authEntity = opts.externalAuthAddress?.replace(
              /^(.*:\/\/)/,
              ''
            );
          } else {
            optsCopy.authEntity = signalingAddress.replace(/^(.*:\/\/)/, '');
          }
        }
        optsCopy.credentials = opts?.webrtcOptions?.signalingCredentials;
        optsCopy.accessToken = opts?.webrtcOptions?.signalingAccessToken;
      }

      optsCopy.externalAuthAddress =
        opts?.webrtcOptions?.signalingExternalAuthAddress;
      optsCopy.externalAuthToEntity =
        opts?.webrtcOptions?.signalingExternalAuthToEntity;
    }

    const directTransport = await dialDirect(signalingAddress, optsCopy);
    const signalingClient = createPromiseClient(SignalingService, directTransport);

    let uuid = '';
    // only send once since exchange may end or ICE may end
    let sentDoneOrErrorOnce = false;
    const sendError = async (err: string) => {
      if (sentDoneOrErrorOnce) {
        return;
      }
      sentDoneOrErrorOnce = true;
      const callRequestUpdate = new CallUpdateRequest();
      callRequestUpdate.uuid = uuid;
      const status = new Status();
      status.code = Code.Unknown;
      status.message = err;
      callRequestUpdate.update = {
        case: "error",
        value: status,
      };
      try {
        await signalingClient.callUpdate(callRequestUpdate, {
          headers: {
            'rpc-host': host,
          },
        });
      } catch (err) {
        console.error(err);
      }
    };
    const sendDone = async () => {
      if (sentDoneOrErrorOnce) {
        return;
      }
      sentDoneOrErrorOnce = true;
      const callRequestUpdate = new CallUpdateRequest();
      callRequestUpdate.uuid = uuid;
      callRequestUpdate.update = {
        case: "done",
        value: true,
      }
      try {
        await signalingClient.callUpdate(callRequestUpdate, {
          headers: {
            'rpc-host': host,
          },
        });
      } catch (err) {
        console.error(err);
      }
    };

    let pResolve: (value: unknown) => void;
    let remoteDescSet = new Promise<unknown>((resolve) => {
      pResolve = resolve;
    });
    let exchangeDone = false;
    if (!webrtcOpts?.disableTrickleICE) {
      // set up offer
      const offerDesc = await pc.createOffer({});

      let iceComplete = false;
      let numCallUpdates = 0;
      let maxCallUpdateDuration = 0;
      let totalCallUpdateDuration = 0;

      pc.addEventListener('iceconnectionstatechange', () => {
        if (pc.iceConnectionState !== 'completed' || numCallUpdates === 0) {
          return;
        }
        let averageCallUpdateDuration =
          totalCallUpdateDuration / numCallUpdates;
        console.groupCollapsed('Caller update statistics');
        console.table({
          num_updates: numCallUpdates,
          average_duration: `${averageCallUpdateDuration}ms`,
          max_duration: `${maxCallUpdateDuration}ms`,
        });
        console.groupEnd();
      });
      pc.addEventListener(
        'icecandidate',
        async (event: { candidate: RTCIceCandidateInit | null }) => {
          await remoteDescSet;
          if (exchangeDone) {
            return;
          }

          if (event.candidate === null) {
            iceComplete = true;
            sendDone();
            return;
          }

          if (event.candidate.candidate !== null) {
            console.debug(`gathered local ICE ${event.candidate.candidate}`);
          }
          const iProto = iceCandidateToProto(event.candidate);
          const callRequestUpdate = new CallUpdateRequest();
          callRequestUpdate.uuid = uuid;
          callRequestUpdate.update = {
            case: "candidate",
            value: iProto,
          }
          const callUpdateStart = new Date();
          try {
            await signalingClient.callUpdate(callRequestUpdate, {
              headers: {
                'rpc-host': host,
              },
            });
            numCallUpdates++;
            let callUpdateEnd = new Date();
            let callUpdateDuration =
              callUpdateEnd.getTime() - callUpdateStart.getTime();
            if (callUpdateDuration > maxCallUpdateDuration) {
              maxCallUpdateDuration = callUpdateDuration;
            }
            totalCallUpdateDuration += callUpdateDuration;
            return;
          } catch (err) {
            if (exchangeDone || iceComplete) {
              return;
            }
            console.error(err);
          }
        }
      );

      await pc.setLocalDescription(offerDesc);
    }

    // initialize cc here so we can use it in the callbacks
    const cc = new ClientChannel(pc, dc);

    let haveInit = false;

    const callRequest = new CallRequest();
    const description = addSdpFields(
      pc.localDescription,
      opts.webrtcOptions?.additionalSdpFields
    );
    const encodedSDP = btoa(JSON.stringify(description));
    callRequest.sdp = encodedSDP;
    if (webrtcOpts && webrtcOpts.disableTrickleICE) {
      callRequest.disableTrickle = webrtcOpts.disableTrickleICE;
    }

    const callResponses = await signalingClient.call(callRequest, {
      headers: {
        'rpc-host': host
      }
    });

    // set timeout for dial attempt if a timeout is specified
    if (opts.dialTimeout) {
      setTimeout(() => {
        if (!successful) {
          cc.close();
        }
      }, opts.dialTimeout);
    }

    const processCallResponses = async () => {
      try {
        for await (const response of callResponses) {
          if (response.stage.case == "init") {
            if (haveInit) {
              sendError('got init stage more than once');
              continue;
            }
            const init = response.stage.value;
            haveInit = true;
            uuid = response.uuid;

            const remoteSDP = new RTCSessionDescription(JSON.parse(atob(init.sdp)));
            if (cc.isClosed()) {
              sendError('client channel is closed');
              continue
            }
            await pc.setRemoteDescription(remoteSDP);

            pResolve!(true);

            if (webrtcOpts?.disableTrickleICE) {
              exchangeDone = true;
              sendDone();
              continue
            }
          } else if (response.stage.case == "update") {
            if (!haveInit) {
              sendError('got update stage before init stage');
              continue
            }
            if (response.uuid !== uuid) {
              sendError(`uuid mismatch; have=${response.uuid} want=${uuid}`);
              continue
            }
            const update = response.stage.value;
            const cand = iceCandidateFromProto(update.candidate!);
            if (cand.candidate !== null) {
              console.debug(`received remote ICE ${cand.candidate}`);
            }
            try {
              await pc.addIceCandidate(cand);
            } catch (error) {
              sendError(JSON.stringify(error));
              continue
            }
          } else {
            sendError('unknown CallResponse stage');
            continue
          }
        }
      } catch (err) {
        if (err instanceof ConnectError) {
          if (err.code == Code.Unimplemented) {
            if (err.message === 'Response closed without headers') {
              throw new ConnectionClosedError('failed to dial');
            }
            if (cc?.isClosed()) {
              throw new ConnectionClosedError('client channel is closed');
            }
            console.error(err.message);
          }
        }
        throw err;
      }
    };

    await Promise.all([cc.ready, processCallResponses()])
    exchangeDone = true;
    sendDone();

    if (opts?.externalAuthAddress) {
      // TODO(GOUT-11): prepare AuthenticateTo here
      // for client channel.
    } else if (opts?.credentials?.type) {
      // TODO(GOUT-11): prepare Authenticate here
      // for client channel
    }

    successful = true;
    return {
      transport: cc,
      peerConnection: pc,
      dataChannel: dc,
    };
  } finally {
    if (!successful) {
      pc.close();
    }
  }
}

function iceCandidateFromProto(i: ICECandidate): RTCIceCandidateInit {
  let candidate: RTCIceCandidateInit = {
    candidate: i.candidate,
  };
  if (i.sdpMid) {
    candidate.sdpMid = i.sdpMid;
  }
  if (i.sdpmLineIndex) {
    candidate.sdpMLineIndex = i.sdpmLineIndex;
  }
  if (i.usernameFragment) {
    candidate.usernameFragment = i.usernameFragment;
  }
  return candidate;
}

function iceCandidateToProto(i: RTCIceCandidateInit): ICECandidate {
  let candidate = new ICECandidate();
  candidate.candidate = i.candidate!;
  if (i.sdpMid) {
    candidate.sdpMid = i.sdpMid;
  }
  if (i.sdpMLineIndex) {
    candidate.sdpmLineIndex = i.sdpMLineIndex;
  }
  if (i.usernameFragment) {
    candidate.usernameFragment = i.usernameFragment;
  }
  return candidate;
}

function validateDialOptions(opts?: DialOptions) {
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
    opts?.webrtcOptions?.signalingAccessToken &&
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
}
