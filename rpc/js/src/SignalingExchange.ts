import {
  CallOptions,
  Code,
  ConnectError,
  PromiseClient,
} from '@connectrpc/connect';
import { ClientChannel } from './ClientChannel';
import { DialWebRTCOptions } from './dial';
import { ConnectionClosedError } from './errors';
import { Status } from './gen/google/rpc/status_pb';
import { SignalingService } from './gen/proto/rpc/webrtc/v1/signaling_connect';
import {
  CallRequest,
  CallResponse,
  CallResponseInitStage,
  CallResponseUpdateStage,
  CallUpdateRequest,
  ICECandidate,
} from './gen/proto/rpc/webrtc/v1/signaling_pb';
import { addSdpFields } from './peer';

const callUUIDUnset = 'invariant: call uuid unset';

export class SignalingExchange {
  private readonly clientChannel: ClientChannel;
  private callUuid?: string;
  // only send once since exchange may end or ICE may end
  private sentDoneOrErrorOnce = false;
  private exchangeDone = false;
  private iceComplete = false;
  private awaitingRemoteDescription?: {
    success: (value: unknown) => void;
    failure: (reason?: any) => void;
  };

  private remoteDescriptionSet?: Promise<unknown>;

  // stats
  private numCallUpdates = 0;
  private maxCallUpdateDuration = 0;
  private totalCallUpdateDuration = 0;

  constructor(
    private readonly signalingClient: PromiseClient<typeof SignalingService>,
    private readonly callOpts: CallOptions,
    private readonly pc: RTCPeerConnection,
    private readonly dc: RTCDataChannel,
    private readonly dialOpts?: DialWebRTCOptions
  ) {
    this.clientChannel = new ClientChannel(this.pc, this.dc);
  }

  public async doExchange(): Promise<ClientChannel> {
    // Setup our handlers before starting the signaling call.
    await this.setup();

    const description = addSdpFields(
      this.pc.localDescription,
      this.dialOpts?.additionalSdpFields
    );
    const encodedSDP = btoa(JSON.stringify(description));
    const callRequest = new CallRequest({
      sdp: encodedSDP,
    });
    if (this.dialOpts && this.dialOpts.disableTrickleICE) {
      callRequest.disableTrickle = this.dialOpts.disableTrickleICE;
    }

    // As long as we establish a connection (i.e. we are ready), then
    // we will make it clear across the exchange that we are done
    // and no more work should be done nor should any errors be emitted.
    this.clientChannel.ready
      .then(() => {
        this.exchangeDone = true;
      })
      .catch(console.error);

    // Initiate now the call now that all of our handlers are setup.
    const callResponses = this.signalingClient.call(callRequest, this.callOpts);

    // Start processing the responses asynchronously.
    const responsesProcessed = this.processCallResponses(callResponses);

    await Promise.all([this.clientChannel.ready, responsesProcessed]);
    return this.clientChannel;
  }

  private async setup() {
    this.remoteDescriptionSet = new Promise<unknown>((resolve, reject) => {
      this.awaitingRemoteDescription = {
        success: resolve,
        failure: reject,
      };
    });
    if (!this.dialOpts?.disableTrickleICE) {
      // set up offer
      const offerDesc = await this.pc.createOffer({});

      this.pc.addEventListener('iceconnectionstatechange', () => {
        if (
          this.pc.iceConnectionState !== 'completed' ||
          this.numCallUpdates === 0
        ) {
          return;
        }
        let averageCallUpdateDuration =
          this.totalCallUpdateDuration / this.numCallUpdates;
        console.groupCollapsed('Caller update statistics');
        console.table({
          num_updates: this.numCallUpdates,
          average_duration: `${averageCallUpdateDuration}ms`,
          max_duration: `${this.maxCallUpdateDuration}ms`,
        });
        console.groupEnd();
      });
      this.pc.addEventListener(
        'icecandidate',
        async (event: { candidate: RTCIceCandidateInit | null }) =>
          this.onLocalICECandidate(event)
      );

      await this.pc.setLocalDescription(offerDesc);
    }
  }

  public terminate() {
    this.clientChannel.close();
  }

  private async processCallResponses(
    callResponses: AsyncIterable<CallResponse>
  ) {
    let haveInit = false;
    try {
      for await (const response of callResponses) {
        if (response.stage.case == 'init') {
          if (haveInit) {
            await this.sendError('got init stage more than once');
            return;
          }
          haveInit = true;
          if (!this.handleInitResponse(response.uuid, response.stage.value)) {
            return;
          }
        } else if (response.stage.case == 'update') {
          if (!haveInit) {
            await this.sendError('got update stage before init stage');
            return;
          }
          if (!this.handleUpdateResponse(response.uuid, response.stage.value)) {
            return;
          }
        } else {
          await this.sendError('unknown CallResponse stage');
          return;
        }
      }
    } catch (err) {
      if (this.exchangeDone || this.pc.iceConnectionState === 'connected') {
        // There's nothing to do with these errors, our connection is established.
        return;
      }
      if (err instanceof ConnectError && err.code == Code.Unimplemented) {
        if (err.message === 'Response closed without headers') {
          throw new ConnectionClosedError('failed to dial');
        }
        if (this.clientChannel?.isClosed()) {
          throw new ConnectionClosedError('client channel is closed');
        }
        console.error(err.message);
      }
      throw err;
    }
  }

  private async handleInitResponse(
    uuid: string,
    response: CallResponseInitStage
  ): Promise<boolean> {
    this.callUuid = uuid;

    const remoteSDP = new RTCSessionDescription(JSON.parse(atob(response.sdp)));
    if (this.clientChannel.isClosed()) {
      await this.sendError('client channel is closed');
      return false;
    }
    await this.pc.setRemoteDescription(remoteSDP);
    this.awaitingRemoteDescription?.success(true);

    if (this.dialOpts?.disableTrickleICE) {
      this.exchangeDone = true;
      await this.sendDone();
      return false;
    }

    return true;
  }

  private async handleUpdateResponse(
    uuid: string,
    response: CallResponseUpdateStage
  ): Promise<boolean> {
    if (uuid !== this.callUuid) {
      await this.sendError(`uuid mismatch; have=${uuid} want=${this.callUuid}`);
      return false;
    }
    const cand = iceCandidateFromProto(response.candidate!);
    if (cand.candidate !== null) {
      console.debug(`received remote ICE ${cand.candidate}`);
    }
    try {
      await this.pc.addIceCandidate(cand);
    } catch (error) {
      await this.sendError(JSON.stringify(error));
      return false;
    }

    return true;
  }

  private async onLocalICECandidate(event: {
    candidate: RTCIceCandidateInit | null;
  }) {
    await this.remoteDescriptionSet;
    if (this.exchangeDone || this.pc.iceConnectionState === 'connected') {
      return;
    }

    if (event.candidate === null) {
      this.iceComplete = true;
      await this.sendDone();
      return;
    }

    if (!this.callUuid) {
      throw new Error(callUUIDUnset);
    }

    if (event.candidate.candidate !== null) {
      console.debug(`gathered local ICE ${event.candidate.candidate}`);
    }
    const iProto = iceCandidateToProto(event.candidate);
    const callRequestUpdate = new CallUpdateRequest({
      uuid: this.callUuid,
      update: {
        case: 'candidate',
        value: iProto,
      },
    });
    const callUpdateStart = new Date();
    try {
      await this.signalingClient.callUpdate(callRequestUpdate, this.callOpts);
      this.numCallUpdates++;
      let callUpdateEnd = new Date();
      let callUpdateDuration =
        callUpdateEnd.getTime() - callUpdateStart.getTime();
      if (callUpdateDuration > this.maxCallUpdateDuration) {
        this.maxCallUpdateDuration = callUpdateDuration;
      }
      this.totalCallUpdateDuration += callUpdateDuration;
      return;
    } catch (err) {
      if (
        this.exchangeDone ||
        this.iceComplete ||
        // @ts-expect-error tsc is unaware that iceConnectionState can change
        // after we've inspected it before.
        this.pc.iceConnectionState === 'connected'
      ) {
        return;
      }
      console.error(err);
    }
  }

  private async sendError(err: string) {
    if (this.sentDoneOrErrorOnce) {
      return;
    }
    if (!this.callUuid) {
      throw new Error(callUUIDUnset);
    }
    this.sentDoneOrErrorOnce = true;
    const callRequestUpdate = new CallUpdateRequest({
      uuid: this.callUuid,
      update: {
        case: 'error',
        value: new Status({
          code: Code.Unknown,
          message: err,
        }),
      },
    });
    try {
      await this.signalingClient.callUpdate(callRequestUpdate, this.callOpts);
    } catch (err) {
      // even though this call update fails, there's a chance another
      // attempt with another ICE candidate(s) will make the connection
      // work. In the future it may be better to figure out if this
      // error is fatal or not.
      console.error('failed to send call update; continuing', err);
    }
  }

  private async sendDone() {
    if (this.sentDoneOrErrorOnce) {
      return;
    }
    if (!this.callUuid) {
      throw new Error(callUUIDUnset);
    }
    this.sentDoneOrErrorOnce = true;
    const callRequestUpdate = new CallUpdateRequest({
      uuid: this.callUuid,
      update: {
        case: 'done',
        value: true,
      },
    });
    try {
      await this.signalingClient.callUpdate(callRequestUpdate, this.callOpts);
    } catch (err) {
      // even though this call update fails, there's a chance another
      // attempt with another ICE candidate(s) will make the connection
      // work. In the future it may be better to figure out if this
      // error is fatal or not.
      console.error(err);
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
  let candidate = new ICECandidate({
    candidate: i.candidate!,
  });
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
