import { DialWebRTCOptions } from './dial-options';

interface ReadyPeer {
  peerConnection: RTCPeerConnection;
  dataChannel: RTCDataChannel;
}

export const addSDPFields = (
  localDescription: RTCSessionDescription | null,
  sdpFields: Record<string, string | number> = {}
) => {
  let { sdp } = localDescription ?? { sdp: '' };

  for (const key of Object.keys(sdpFields)) {
    sdp = [sdp, `a=${key}:${sdpFields[key]}\r\n`].join('');
  }

  return { sdp, type: localDescription?.type };
};

const DEFAULT_CONFIG: RTCConfiguration = {
  iceServers: [
    {
      urls: 'stun:global.stun.twilio.com:3478',
    },
  ],
};

const SHARED_OPTIONS: RTCDataChannelInit = {
  negotiated: true,
  ordered: true,
} as const;

const getDataChannelOptions = (id: number) => ({ id, ...SHARED_OPTIONS });

export const newPeerConnectionForClient = async ({
  disableTrickleICE,
  rtcConfig = DEFAULT_CONFIG,
  additionalSdpFields,
}: DialWebRTCOptions): Promise<ReadyPeer> => {
  const config = { ...rtcConfig };

  let ignoreOffer = false;
  let negotiationOpen = false;

  const peerConnection = new RTCPeerConnection(config);
  const dataChannel = peerConnection.createDataChannel(
    'data',
    getDataChannelOptions(0)
  );

  dataChannel.binaryType = 'arraybuffer';

  const negotiationChannel = peerConnection.createDataChannel(
    'negotiation',
    getDataChannelOptions(1)
  );

  negotiationChannel.binaryType = 'arraybuffer';

  // eslint-disable-next-line unicorn/prefer-add-event-listener
  negotiationChannel.onopen = () => (negotiationOpen = true);

  // eslint-disable-next-line unicorn/prefer-add-event-listener
  negotiationChannel.onmessage = async (event: MessageEvent) => {
    try {
      const description = new RTCSessionDescription(
        JSON.parse(atob(event.data))
      );

      const offerCollision =
        description.type === 'offer' &&
        peerConnection.signalingState !== 'stable';

      ignoreOffer = offerCollision;
      if (ignoreOffer) {
        return;
      }

      await peerConnection.setRemoteDescription(description);

      if (description.type === 'offer') {
        await peerConnection.setLocalDescription();
        const newDescription = addSDPFields(
          peerConnection.localDescription,
          additionalSdpFields
        );

        negotiationChannel.send(btoa(JSON.stringify(newDescription)));
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error(error);
    }
  };

  peerConnection.onnegotiationneeded = async () => {
    if (!negotiationOpen) {
      return;
    }

    try {
      await peerConnection.setLocalDescription();
      const newDescription = addSDPFields(
        peerConnection.localDescription,
        additionalSdpFields
      );

      negotiationChannel.send(btoa(JSON.stringify(newDescription)));
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error(error);
    }
  };

  if (!disableTrickleICE) {
    return { peerConnection, dataChannel };
  }

  return new Promise((resolve, reject) => {
    peerConnection.onicecandidate = async (event) => {
      if (event.candidate !== null) {
        return;
      }

      // set up offer
      const offerDesc = await peerConnection.createOffer();

      try {
        await peerConnection.setLocalDescription(offerDesc);
        resolve({ peerConnection, dataChannel });
      } catch (error) {
        reject(error);
      }
    };
  });
};
