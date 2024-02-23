import { RTCPeerConnection, RTCSessionDescription } from "react-native-webrtc";
import MessageEvent from "react-native-webrtc/lib/typescript/MessageEvent";
import RTCDataChannel from "react-native-webrtc/lib/typescript/RTCDataChannel";
import { atob, btoa } from './polyfills'

interface ReadyPeer {
  pc: RTCPeerConnection;
  dc: RTCDataChannel;
}

export function addSdpFields(
  localDescription?: RTCSessionDescription | null,
  sdpFields?: Record<string, string | number>
) {
  let description = {
    sdp: localDescription?.sdp,
    type: localDescription?.type,
  };
  if (sdpFields) {
    Object.keys(sdpFields).forEach((key) => {
      description.sdp = [
        description.sdp,
        `a=${key}:${sdpFields[key as keyof typeof sdpFields]}\r\n`,
      ].join('');
    });
  }
  return description;
}

export async function newPeerConnectionForClient(
  disableTrickle: boolean,
  rtcConfig?: RTCConfiguration,
  additionalSdpFields?: Record<string, string | number>
): Promise<ReadyPeer> {
  if (!rtcConfig) {
    rtcConfig = {
      iceServers: [
        {
          urls: 'stun:global.stun.twilio.com:3478',
        },
      ],
    };
  }
  const peerConnection = new RTCPeerConnection(rtcConfig);

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
  negotiationChannel.addEventListener("open", () => {
    negOpen = true;
  })
  negotiationChannel.addEventListener("message", async (event: MessageEvent<any>) => {
    try {
      const description = new RTCSessionDescription(
        JSON.parse(atob(event.data.toString()))
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
    } catch (e) {
      console.error(e);
    }
  })
  // negotiationChannel.onmessage = async (event: MessageEvent<any>) => {
  //   try {
  //     const description = new RTCSessionDescription(
  //       JSON.parse(atob(event.data))
  //     );

  //     const offerCollision =
  //       description.type === 'offer' &&
  //       (description || peerConnection.signalingState !== 'stable');
  //     ignoreOffer = !polite && offerCollision;
  //     if (ignoreOffer) {
  //       return;
  //     }

  //     await peerConnection.setRemoteDescription(description);

  //     if (description.type === 'offer') {
  //       await peerConnection.setLocalDescription();
  //       const newDescription = addSdpFields(
  //         peerConnection.localDescription,
  //         additionalSdpFields
  //       );
  //       negotiationChannel.send(btoa(JSON.stringify(newDescription)));
  //     }
  //   } catch (e) {
  //     console.error(e);
  //   }
  // };

  peerConnection.addEventListener("negotiationneeded", async () => {
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
    } catch (e) {
      console.error(e);
    }
  })
  // peerConnection.onnegotiationneeded = async () => {
  //   if (!negOpen) {
  //     return;
  //   }
  //   try {
  //     await peerConnection.setLocalDescription();
  //     const newDescription = addSdpFields(
  //       peerConnection.localDescription,
  //       additionalSdpFields
  //     );
  //     negotiationChannel.send(btoa(JSON.stringify(newDescription)));
  //   } catch (e) {
  //     console.error(e);
  //   }
  // };

  if (!disableTrickle) {
    return Promise.resolve({ pc: peerConnection, dc: dataChannel });
  }
  // set up offer
  const offerDesc = await peerConnection.createOffer({});
  try {
    await peerConnection.setLocalDescription(offerDesc);
  } catch (e) {
    return Promise.reject(e);
  }

  peerConnection.addEventListener("icecandidate", async (event) => {
    if (event.candidate !== null) {
      return;
    }
    pResolve({ pc: peerConnection, dc: dataChannel });
  })
  // peerConnection.onicecandidate = async (event) => {
  //   if (event.candidate !== null) {
  //     return;
  //   }
  //   pResolve({ pc: peerConnection, dc: dataChannel });
  // };

  return result;
}
