import { DialWebRTCOptions } from './dial-options';

interface ReadyPeer {
  peerConnection: RTCPeerConnection;
  dataChannel: RTCDataChannel;
}

export function addSdpFields(
  localDescription?: RTCSessionDescription | null,
  sdpFields?: Record<string, string | number>
) {
  const description = {
    sdp: localDescription?.sdp,
    type: localDescription?.type,
  };
  if (sdpFields) {
    for (const key of Object.keys(sdpFields)) {
      description.sdp = [
        description.sdp,
        `a=${key}:${sdpFields[key]}\r\n`,
      ].join('');
    }
  }
  return description;
}

export const newPeerConnectionForClient = async ({
  disableTrickleICE,
  rtcConfig,
  additionalSdpFields,
}: DialWebRTCOptions): Promise<ReadyPeer> => {
  const config = rtcConfig ?? {
    iceServers: [
      {
        urls: 'stun:global.stun.twilio.com:3478',
      },
    ],
  };

  const peerConnection = new RTCPeerConnection(config);

  let pResolve: (value: ReadyPeer) => void;
  const result = new Promise<ReadyPeer>((resolve) => {
    pResolve = resolve;
  });
  const dataChannel = peerConnection.createDataChannel('data', {
    id: 0,
    negotiated: true,
    ordered: true,
  });
  dataChannel.binaryType = 'arraybuffer';

  const negotiationChannel = peerConnection.createDataChannel('negotiation', {
    id: 1,
    negotiated: true,
    ordered: true,
  });
  negotiationChannel.binaryType = 'arraybuffer';

  let ignoreOffer = false;
  const polite = true;
  let negOpen = false;

  // eslint-disable-next-line unicorn/prefer-add-event-listener
  negotiationChannel.onopen = () => {
    negOpen = true;
  };

  // eslint-disable-next-line unicorn/prefer-add-event-listener
  negotiationChannel.onmessage = async (event: MessageEvent) => {
    try {
      const description = new RTCSessionDescription(
        JSON.parse(atob(event.data))
      );

      const offerCollision =
        description.type === 'offer' &&
        (description || peerConnection.signalingState !== 'stable');
      ignoreOffer = !polite && offerCollision;
      if (ignoreOffer) {
        return;
      }

      await peerConnection.setRemoteDescription(description);

      if (description.type === 'offer') {
        await peerConnection.setLocalDescription();
        const newDescription = addSdpFields(
          peerConnection.localDescription,
          additionalSdpFields
        );
        negotiationChannel.send(btoa(JSON.stringify(newDescription)));
      }
    } catch (error) {
      console.error(error);
    }
  };

  peerConnection.onnegotiationneeded = async () => {
    if (!negOpen) {
      return;
    }
    try {
      await peerConnection.setLocalDescription();
      const newDescription = addSdpFields(
        peerConnection.localDescription,
        additionalSdpFields
      );
      negotiationChannel.send(btoa(JSON.stringify(newDescription)));
    } catch (error) {
      console.error(error);
    }
  };

  if (!disableTrickleICE) {
    return { peerConnection, dataChannel };
  }
  // set up offer
  const offerDesc = await peerConnection.createOffer();
  try {
    await peerConnection.setLocalDescription(offerDesc);
  } catch (error) {
    return Promise.reject(error);
  }

  peerConnection.onicecandidate = async (event) => {
    if (event.candidate !== null) {
      return;
    }
    pResolve({ peerConnection, dataChannel });
  };

  return result;
};
