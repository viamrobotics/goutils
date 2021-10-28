interface ReadyPeer {
	pc: RTCPeerConnection;
	dc: RTCDataChannel; 
}

export async function newPeerConnectionForClient(rtcConfig?: RTCConfiguration): Promise<ReadyPeer> {
	if (!rtcConfig) {
		rtcConfig = {
			iceServers: [
				{
					urls: "stun:global.stun.twilio.com:3478?transport=udp"
				},
			]
		};
	}
	const peerConnection = new RTCPeerConnection(rtcConfig);

	let pResolve: (value: ReadyPeer) => void;
	const result = new Promise<ReadyPeer>(resolve => {
		pResolve = resolve;
	})
	const dataChannel = peerConnection.createDataChannel("data", {
		id: 0,
		negotiated: true,
		ordered: true
	});
	dataChannel.binaryType = "arraybuffer";

	peerConnection.onicecandidate = async event => {
		if (event.candidate !== null) {
			return;
		}
		pResolve({ pc: peerConnection, dc: dataChannel });
	}

	// set up offer
	const offerDesc = await peerConnection.createOffer();
	try {
		peerConnection.setLocalDescription(offerDesc)
	} catch (e) {
		console.error(e);
	}
	return result;
}
