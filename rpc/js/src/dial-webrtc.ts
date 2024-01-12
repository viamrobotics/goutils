import { grpc } from '@improbable-eng/grpc-web';
import { ProtobufMessage } from '@improbable-eng/grpc-web/dist/typings/message';
import { dialDirect } from './dial-direct';
import {
  DialOptions,
  DialWebRTCOptions,
  validateDialOptions,
} from './dial-options';
import { Code } from './gen/google/rpc/code_pb';
import {
  WebRTCConfig,
  OptionalWebRTCConfigRequest,
  OptionalWebRTCConfigResponse,
  CallRequest,
  CallResponse,
  CallUpdateRequest,
  CallUpdateResponse,
} from './gen/proto/rpc/webrtc/v1/signaling_pb';
import { SignalingService } from './gen/proto/rpc/webrtc/v1/signaling_pb_service';
import { ClientChannel } from './ClientChannel';
import { ConnectionClosedError } from './errors';
import { Status } from './gen/google/rpc/status_pb';
import { iceCandidateToProto, iceCandidateFromProto } from './ice-candidate';
import { newPeerConnectionForClient, addSdpFields } from './peer';

export interface WebRTCConnection {
  // TODO: Add doc comments for these properties
  transportFactory: grpc.TransportFactory;
  peerConnection: RTCPeerConnection;
}

const getOptionalWebRTCConfig = async (
  signalingAddress: string,
  host: string,
  options: DialOptions = {}
): Promise<WebRTCConfig> => {
  const directTransport = await dialDirect(signalingAddress, options);

  return new Promise((resolve, reject) => {
    grpc.unary(SignalingService.OptionalWebRTCConfig, {
      request: new OptionalWebRTCConfigRequest(),
      metadata: { 'rpc-host': host },
      host: signalingAddress,
      transport: directTransport,
      onEnd: (resp: grpc.UnaryOutput<OptionalWebRTCConfigResponse>) => {
        const { status, statusMessage, message } = resp;
        if (status === grpc.Code.OK && message) {
          const config = message.getConfig();
          if (!config) {
            resolve(new WebRTCConfig());
            return;
          }

          resolve(config);
        } else {
          reject(statusMessage);
        }
      },
    });
  });
};

const getAdditionalIceServers = (config: WebRTCConfig): RTCIceServer[] =>
  config.toObject().additionalIceServersList.map((ice) => {
    return {
      urls: ice.urlsList,
      credential: ice.credential,
      username: ice.username,
    };
  });

const getDialWebRTCOptions = (
  webRTCConfig: WebRTCConfig,
  iceServers: RTCIceServer[],
  options: DialOptions = {}
): DialWebRTCOptions => {
  if (!options.webrtcOptions) {
    return {
      disableTrickleICE: webRTCConfig.getDisableTrickle(),
      rtcConfig: {
        iceServers,
      },
    };
  }

  const dialWebRTCOptions = { ...options.webrtcOptions };
  if (dialWebRTCOptions.rtcConfig) {
    dialWebRTCOptions.rtcConfig.iceServers = [
      ...(dialWebRTCOptions.rtcConfig.iceServers ?? []),
      ...iceServers,
    ];
  } else {
    dialWebRTCOptions.rtcConfig = { iceServers };
  }

  return dialWebRTCOptions;
};

const getDirectTransport = async (
  signalingAddress: string,
  options: DialOptions = {}
) => {
  // replace auth entity and creds
  const dialOptions: DialOptions = { ...options };
  if (!options.accessToken) {
    dialOptions.authEntity = options.webrtcOptions?.signalingAuthEntity;
    if (!dialOptions.authEntity) {
      dialOptions.authEntity = dialOptions.externalAuthAddress
        ? // eslint-disable-next-line prefer-named-capture-group
          options.externalAuthAddress?.replace(/^(.*:\/\/)/u, '')
        : // eslint-disable-next-line prefer-named-capture-group
          signalingAddress.replace(/^(.*:\/\/)/u, '');
    }
    dialOptions.credentials = options.webrtcOptions?.signalingCredentials;
    dialOptions.accessToken = options.webrtcOptions?.signalingAccessToken;
  }

  dialOptions.externalAuthAddress =
    options.webrtcOptions?.signalingExternalAuthAddress;
  dialOptions.externalAuthToEntity =
    options.webrtcOptions?.signalingExternalAuthToEntity;

  return dialDirect(signalingAddress, dialOptions);
};

// only send once since exchange may end or ICE may end
let sentDoneOrErrorOnce = false;

const sendErrorUpdate = (
  uuid: string,
  message: string,
  signalingAddress: string,
  host: string,
  directTransport: grpc.TransportFactory
) => {
  if (sentDoneOrErrorOnce) {
    return;
  }

  sentDoneOrErrorOnce = true;

  const request = new CallUpdateRequest();
  request.setUuid(uuid);

  const status = new Status();
  status.setCode(Code.UNKNOWN);
  status.setMessage(message);
  request.setError(status);

  grpc.unary(SignalingService.CallUpdate, {
    request,
    metadata: { 'rpc-host': host },
    host: signalingAddress,
    transport: directTransport,
    onEnd: ({
      status: outputStatus,
      message: outputMessage,
      statusMessage,
    }: grpc.UnaryOutput<CallUpdateResponse>) => {
      if (outputStatus === grpc.Code.OK && outputMessage) {
        return;
      }

      // eslint-disable-next-line no-console
      console.error(statusMessage);
    },
  });
};

const sendDoneUpdate = (
  uuid: string,
  signalingAddress: string,
  host: string,
  directTransport: grpc.TransportFactory
) => {
  if (sentDoneOrErrorOnce) {
    return;
  }

  sentDoneOrErrorOnce = true;

  const request = new CallUpdateRequest();
  request.setUuid(uuid);
  request.setDone(true);

  grpc.unary(SignalingService.CallUpdate, {
    request,
    metadata: { 'rpc-host': host },
    host: signalingAddress,
    transport: directTransport,
    onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
      const { status, statusMessage, message } = output;
      if (status === grpc.Code.OK && message) {
        return;
      }

      // eslint-disable-next-line no-console
      console.error(statusMessage);
    },
  });
};

const setupTrickleICE = async (
  uuid: string,
  signalingAddress: string,
  host: string,
  exchangeDone: boolean,
  waitForRemoteDescription: Promise<void>,
  peerConnection: RTCPeerConnection,
  directTransport: grpc.TransportFactory
) => {
  // set up offer
  const offerDesc = await peerConnection.createOffer();
  let iceComplete = false;

  peerConnection.onicecandidate = async (event: RTCPeerConnectionIceEvent) => {
    await waitForRemoteDescription;
    if (exchangeDone) {
      return;
    }

    if (event.candidate === null) {
      iceComplete = true;
      sendDoneUpdate(uuid, signalingAddress, host, directTransport);
      return;
    }

    const candidate = iceCandidateToProto(event.candidate);
    const request = new CallUpdateRequest();
    request.setUuid(uuid);
    request.setCandidate(candidate);

    grpc.unary(SignalingService.CallUpdate, {
      request,
      metadata: { 'rpc-host': host },
      host: signalingAddress,
      transport: directTransport,
      onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
        const { status, statusMessage, message } = output;
        if (status === grpc.Code.OK && message) {
          return;
        }

        if (iceComplete) {
          return;
        }

        // eslint-disable-next-line no-console
        console.error('error sending candidate', statusMessage);
      },
    });
  };

  await peerConnection.setLocalDescription(offerDesc);
};

const createClientMessageHandler = (
  uuid: string,
  signalingAddress: string,
  host: string,
  peerConnection: RTCPeerConnection,
  directTransport: grpc.TransportFactory,
  setUUID: (value: string) => void,
  remoteDescriptionSet: () => void
) => {
  let haveInit = false;

  return async (message: ProtobufMessage) => {
    const response = message as CallResponse;

    if (response.hasInit()) {
      if (haveInit) {
        sendErrorUpdate(
          uuid,
          'got init stage more than once',
          signalingAddress,
          host,
          directTransport
        );
        return;
      }

      const init = response.getInit()!;
      haveInit = true;
      setUUID(response.getUuid());

      const remoteSDP = new RTCSessionDescription(
        JSON.parse(atob(init.getSdp()))
      );

      await peerConnection.setRemoteDescription(remoteSDP);
      remoteDescriptionSet();

      /*
       *   TODO: Set these in the handler for remoteDescriptionSet
       *   if (webrtcOpts.disableTrickleICE) {
       *     exchangeDone = true;
       *     sendDone();
       *   }
       */
    } else if (response.hasUpdate()) {
      if (!haveInit) {
        sendErrorUpdate(
          uuid,
          'got update stage before init stage',
          signalingAddress,
          host,
          directTransport
        );
        return;
      }

      if (response.getUuid() !== uuid) {
        sendErrorUpdate(
          uuid,
          `uuid mismatch; have=${response.getUuid()} want=${uuid}`,
          signalingAddress,
          host,
          directTransport
        );
        return;
      }

      const update = response.getUpdate();
      if (!update) {
        sendErrorUpdate(
          uuid,
          `invalid update from response`,
          signalingAddress,
          host,
          directTransport
        );
        return;
      }

      const proto = update.getCandidate();
      if (!proto) {
        sendErrorUpdate(
          uuid,
          `could not get candidate from update`,
          signalingAddress,
          host,
          directTransport
        );
        return;
      }

      const candidate = iceCandidateFromProto(proto);

      try {
        await peerConnection.addIceCandidate(candidate);
      } catch (error) {
        sendErrorUpdate(
          uuid,
          JSON.stringify(error),
          signalingAddress,
          host,
          directTransport
        );
      }
    } else {
      sendErrorUpdate(
        uuid,
        'unknown CallResponse stage',
        signalingAddress,
        host,
        directTransport
      );
    }
  };
};

const waitForClientEnd = async (
  client: grpc.Client<grpc.ProtobufMessage, grpc.ProtobufMessage>
) =>
  new Promise<void>((resolve, reject) => {
    client.onEnd(
      (status: grpc.Code, statusMessage: string, _trailers: grpc.Metadata) => {
        if (status === grpc.Code.OK) {
          resolve();
          return;
        }

        if (statusMessage === 'Response closed without headers') {
          reject(new ConnectionClosedError('failed to dial'));
          return;
        }

        // eslint-disable-next-line no-console
        console.error(statusMessage);
        reject(statusMessage);
      }
    );
  });

const waitForClientChannelReady = async (channel: ClientChannel) =>
  new Promise<void>((resolve, reject) => {
    channel.ready.then(() => resolve()).catch((error) => reject(error));
  });

const startClient = (
  host: string,
  client: grpc.Client<grpc.ProtobufMessage, grpc.ProtobufMessage>,
  peerConnection: RTCPeerConnection,
  options: DialWebRTCOptions
) => {
  client.start({ 'rpc-host': host });

  const callRequest = new CallRequest();
  const description = addSdpFields(
    peerConnection.localDescription,
    options.additionalSdpFields
  );

  const encodedSDP = btoa(JSON.stringify(description));
  callRequest.setSdp(encodedSDP);

  if (options.disableTrickleICE) {
    callRequest.setDisableTrickle(options.disableTrickleICE);
  }

  client.send(callRequest);
};

/*
 * dialWebRTC makes a connection to given host by signaling with the address provided. A Promise is returned
 * upon successful connection that contains a transport factory to use with gRPC client as well as the WebRTC
 * PeerConnection itself. Care should be taken with the PeerConnection and is currently returned for experimental
 * use.
 * TODO(GOUT-7): figure out decent way to handle reconnect on connection termination
 */
export const dialWebRTC = async (
  address: string,
  host: string,
  options: DialOptions = {}
): Promise<WebRTCConnection> => {
  const signalingAddress = address.replace(/(?<temp1>\/)$/u, '');
  validateDialOptions(options);

  /*
   * TODO(RSDK-2836): In general, this logic should be in parity with the golang implementation.
   * https://github.com/viamrobotics/goutils/blob/main/rpc/wrtc_client.go#L160-L175
   */
  const config = await getOptionalWebRTCConfig(signalingAddress, host, options);
  const additionalIceServers = getAdditionalIceServers(config);
  const webRTCOptions = getDialWebRTCOptions(config, additionalIceServers);
  const { peerConnection, dataChannel } =
    await newPeerConnectionForClient(webRTCOptions);

  let successful = false;

  try {
    const directTransport = await getDirectTransport(signalingAddress, options);
    const client = grpc.client(SignalingService.Call, {
      host: signalingAddress,
      transport: directTransport,
    });

    let uuid = '';
    let exchangeDone = false;
    sentDoneOrErrorOnce = false;

    let remoteDescriptionSet: () => void = () => ({});
    const waitForRemoteDescription = new Promise<void>((resolve) => {
      remoteDescriptionSet = resolve;
    });

    if (!webRTCOptions.disableTrickleICE) {
      await setupTrickleICE(
        uuid,
        signalingAddress,
        host,
        exchangeDone,
        waitForRemoteDescription,
        peerConnection,
        directTransport
      );
    }

    client.onMessage(
      (message) =>
        // eslint-disable-next-line no-void
        void createClientMessageHandler(
          uuid,
          signalingAddress,
          host,
          peerConnection,
          directTransport,
          (next) => (uuid = next),
          remoteDescriptionSet
        )(message)
    );

    startClient(host, client, peerConnection, webRTCOptions);

    const channel = new ClientChannel(peerConnection, dataChannel);
    await Promise.race([
      waitForClientEnd(client),
      waitForClientChannelReady(channel),
    ]);

    exchangeDone = true;
    sendDoneUpdate(uuid, signalingAddress, host, directTransport);

    /*
     * TODO(GOUT-11): prepare AuthenticateTo here for client channel
     * if (options.externalAuthAddress) {
     *   ...
     * } else if (options.credentials?.type) {
     *   ...
     * }
     */

    successful = true;
    return { transportFactory: channel.transportFactory(), peerConnection };
  } finally {
    if (!successful) {
      peerConnection.close();
    }
  }
};
