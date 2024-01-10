import { grpc } from '@improbable-eng/grpc-web';
import type { ProtobufMessage } from '@improbable-eng/grpc-web/dist/typings/message';
import { ClientChannel } from './ClientChannel';
import { ConnectionClosedError } from './errors';
import { Code } from './gen/google/rpc/code_pb';
import { Status } from './gen/google/rpc/status_pb';
import {
  CallRequest,
  CallResponse,
  CallUpdateRequest,
  CallUpdateResponse,
  WebRTCConfig,
  OptionalWebRTCConfigRequest,
  OptionalWebRTCConfigResponse,
} from './gen/proto/rpc/webrtc/v1/signaling_pb';
import { SignalingService } from './gen/proto/rpc/webrtc/v1/signaling_pb_service';
import { addSdpFields, newPeerConnectionForClient } from './peer';
import {
  DialOptions,
  DialWebRTCOptions,
  validateDialOptions,
} from './dial-options';
import { dialDirect } from './dial-direct';
import { iceCandidateToProto, iceCandidateFromProto } from './ice-candidate';

export interface WebRTCConnection {
  transportFactory: grpc.TransportFactory;
  peerConnection: RTCPeerConnection;
}

async function getOptionalWebRTCConfig(
  signalingAddress: string,
  host: string,
  opts?: DialOptions
): Promise<WebRTCConfig> {
  const optsCopy = { ...opts } as DialOptions;
  const directTransport = await dialDirect(signalingAddress, optsCopy);

  let pResolve: (value: WebRTCConfig) => void;
  let pReject: (reason?: unknown) => void;

  let result: WebRTCConfig | undefined;
  const done = new Promise<WebRTCConfig>((resolve, reject) => {
    pResolve = resolve;
    pReject = reject;
  });

  grpc.unary(SignalingService.OptionalWebRTCConfig, {
    request: new OptionalWebRTCConfigRequest(),
    metadata: {
      'rpc-host': host,
    },
    host: signalingAddress,
    transport: directTransport,
    onEnd: (resp: grpc.UnaryOutput<OptionalWebRTCConfigResponse>) => {
      const { status, statusMessage, message } = resp;
      if (status === grpc.Code.OK && message) {
        result = message.getConfig();
        if (!result) {
          pResolve(new WebRTCConfig());
          return;
        }
        pResolve(result);
      } else {
        pReject(statusMessage);
      }
    },
  });

  await done;

  if (!result) {
    throw new Error('no config');
  }
  return result;
}

/*
 * dialWebRTC makes a connection to given host by signaling with the address provided. A Promise is returned
 * upon successful connection that contains a transport factory to use with gRPC client as well as the WebRTC
 * PeerConnection itself. Care should be taken with the PeerConnection and is currently returned for experimental
 * use.
 * TODO(GOUT-7): figure out decent way to handle reconnect on connection termination
 */
export async function dialWebRTC(
  signalingAddress: string,
  host: string,
  opts: DialOptions = {}
): Promise<WebRTCConnection> {
  signalingAddress = signalingAddress.replace(/(\/)$/, '');
  validateDialOptions(opts);

  /*
   * TODO(RSDK-2836): In general, this logic should be in parity with the golang implementation.
   * https://github.com/viamrobotics/goutils/blob/main/rpc/wrtc_client.go#L160-L175
   */
  const config = await getOptionalWebRTCConfig(signalingAddress, host, opts);
  const additionalIceServers: RTCIceServer[] = config
    .toObject()
    .additionalIceServersList.map((ice) => {
      return {
        urls: ice.urlsList,
        credential: ice.credential,
        username: ice.username,
      };
    });

  let webrtcOpts: DialWebRTCOptions;
  if (opts.webrtcOptions) {
    webrtcOpts = opts.webrtcOptions;
    if (webrtcOpts.rtcConfig) {
      webrtcOpts.rtcConfig.iceServers = [
        ...(webrtcOpts.rtcConfig.iceServers || []),
        ...additionalIceServers,
      ];
    } else {
      webrtcOpts.rtcConfig = { iceServers: additionalIceServers };
    }
  } else {
    // use additional webrtc config as default
    webrtcOpts = {
      disableTrickleICE: config.getDisableTrickle(),
      rtcConfig: {
        iceServers: additionalIceServers,
      },
    };
  }

  const { pc, dc } = await newPeerConnectionForClient(
    webrtcOpts !== undefined && webrtcOpts.disableTrickleICE,
    webrtcOpts.rtcConfig,
    webrtcOpts.additionalSdpFields
  );
  let successful = false;

  try {
    // replace auth entity and creds
    const optsCopy: DialOptions = { ...opts };
    if (opts) {
      if (!opts.accessToken) {
        optsCopy.authEntity = opts.webrtcOptions?.signalingAuthEntity;
        if (!optsCopy.authEntity) {
          optsCopy.authEntity = optsCopy.externalAuthAddress
            ? opts.externalAuthAddress?.replace(/^(.*:\/\/)/, '')
            : signalingAddress.replace(/^(.*:\/\/)/, '');
        }
        optsCopy.credentials = opts.webrtcOptions?.signalingCredentials;
        optsCopy.accessToken = opts.webrtcOptions?.signalingAccessToken;
      }

      optsCopy.externalAuthAddress =
        opts.webrtcOptions?.signalingExternalAuthAddress;
      optsCopy.externalAuthToEntity =
        opts.webrtcOptions?.signalingExternalAuthToEntity;
    }

    const directTransport = await dialDirect(signalingAddress, optsCopy);
    const client = grpc.client(SignalingService.Call, {
      host: signalingAddress,
      transport: directTransport,
    });

    let uuid = '';
    // only send once since exchange may end or ICE may end
    let sentDoneOrErrorOnce = false;
    const sendError = (err: string) => {
      if (sentDoneOrErrorOnce) {
        return;
      }
      sentDoneOrErrorOnce = true;
      const callRequestUpdate = new CallUpdateRequest();
      callRequestUpdate.setUuid(uuid);
      const status = new Status();
      status.setCode(Code.UNKNOWN);
      status.setMessage(err);
      callRequestUpdate.setError(status);
      grpc.unary(SignalingService.CallUpdate, {
        request: callRequestUpdate,
        metadata: {
          'rpc-host': host,
        },
        host: signalingAddress,
        transport: directTransport,
        onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
          const { status, statusMessage, message } = output;
          if (status === grpc.Code.OK && message) {
            return;
          }
          console.error(statusMessage);
        },
      });
    };
    const sendDone = () => {
      if (sentDoneOrErrorOnce) {
        return;
      }
      sentDoneOrErrorOnce = true;
      const callRequestUpdate = new CallUpdateRequest();
      callRequestUpdate.setUuid(uuid);
      callRequestUpdate.setDone(true);
      grpc.unary(SignalingService.CallUpdate, {
        request: callRequestUpdate,
        metadata: {
          'rpc-host': host,
        },
        host: signalingAddress,
        transport: directTransport,
        onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
          const { status, statusMessage, message } = output;
          if (status === grpc.Code.OK && message) {
            return;
          }
          console.error(statusMessage);
        },
      });
    };

    let pResolve: (value: unknown) => void;
    const remoteDescSet = new Promise<unknown>((resolve) => {
      pResolve = resolve;
    });
    let exchangeDone = false;
    if (!webrtcOpts.disableTrickleICE) {
      // set up offer
      const offerDesc = await pc.createOffer();

      let iceComplete = false;
      pc.onicecandidate = async (event) => {
        await remoteDescSet;
        if (exchangeDone) {
          return;
        }

        if (event.candidate === null) {
          iceComplete = true;
          sendDone();
          return;
        }

        const iProto = iceCandidateToProto(event.candidate);
        const callRequestUpdate = new CallUpdateRequest();
        callRequestUpdate.setUuid(uuid);
        callRequestUpdate.setCandidate(iProto);
        grpc.unary(SignalingService.CallUpdate, {
          request: callRequestUpdate,
          metadata: {
            'rpc-host': host,
          },
          host: signalingAddress,
          transport: directTransport,
          onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
            const { status, statusMessage, message } = output;
            if (status === grpc.Code.OK && message) {
              return;
            }
            if (exchangeDone || iceComplete) {
              return;
            }
            console.error('error sending candidate', statusMessage);
          },
        });
      };

      await pc.setLocalDescription(offerDesc);
    }

    let haveInit = false;
    // TS says that CallResponse isn't a valid type here. More investigation required.
    client.onMessage(async (message: ProtobufMessage) => {
      const response = message as CallResponse;

      if (response.hasInit()) {
        if (haveInit) {
          sendError('got init stage more than once');
          return;
        }
        const init = response.getInit()!;
        haveInit = true;
        uuid = response.getUuid();

        const remoteSDP = new RTCSessionDescription(
          JSON.parse(atob(init.getSdp()))
        );
        await pc.setRemoteDescription(remoteSDP);

        pResolve(true);

        if (webrtcOpts.disableTrickleICE) {
          exchangeDone = true;
          sendDone();
        }
      } else if (response.hasUpdate()) {
        if (!haveInit) {
          sendError('got update stage before init stage');
          return;
        }
        if (response.getUuid() !== uuid) {
          sendError(`uuid mismatch; have=${response.getUuid()} want=${uuid}`);
          return;
        }
        const update = response.getUpdate()!;
        const cand = iceCandidateFromProto(update.getCandidate()!);
        try {
          await pc.addIceCandidate(cand);
        } catch (error) {
          sendError(JSON.stringify(error));
        }
      } else {
        sendError('unknown CallResponse stage');
      }
    });

    let clientEndResolve: () => void;
    let clientEndReject: (reason?: unknown) => void;
    const clientEnd = new Promise<void>((resolve, reject) => {
      clientEndResolve = resolve;
      clientEndReject = reject;
    });
    client.onEnd(
      (status: grpc.Code, statusMessage: string, _trailers: grpc.Metadata) => {
        if (status === grpc.Code.OK) {
          clientEndResolve();
          return;
        }
        if (statusMessage === 'Response closed without headers') {
          clientEndReject(new ConnectionClosedError('failed to dial'));
          return;
        }
        console.error(statusMessage);
        clientEndReject(statusMessage);
      }
    );
    client.start({ 'rpc-host': host });

    const callRequest = new CallRequest();
    const description = addSdpFields(
      pc.localDescription,
      opts.webrtcOptions?.additionalSdpFields
    );
    const encodedSDP = btoa(JSON.stringify(description));
    callRequest.setSdp(encodedSDP);
    if (webrtcOpts && webrtcOpts.disableTrickleICE) {
      callRequest.setDisableTrickle(webrtcOpts.disableTrickleICE);
    }
    client.send(callRequest);

    const cc = new ClientChannel(pc, dc);
    cc.ready
      .then(() => clientEndResolve())
      .catch((error) => clientEndReject(error));
    await clientEnd;
    await cc.ready;
    exchangeDone = true;
    sendDone();

    if (opts.externalAuthAddress) {
      /*
       * TODO(GOUT-11): prepare AuthenticateTo here
       * for client channel.
       */
    } else if (opts.credentials?.type) {
      /*
       * TODO(GOUT-11): prepare Authenticate here
       * for client channel
       */
    }

    successful = true;
    return { transportFactory: cc.transportFactory(), peerConnection: pc };
  } finally {
    if (!successful) {
      pc.close();
    }
  }
}
