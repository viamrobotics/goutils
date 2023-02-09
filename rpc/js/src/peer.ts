interface ReadyPeer {
	pc: RTCPeerConnection;
	dc: RTCDataChannel; 
}

export async function newPeerConnectionForClient(disableTrickle: boolean, rtcConfig?: RTCConfiguration): Promise<ReadyPeer> {
	if (!rtcConfig) {
		rtcConfig = {
			iceServers: [
				{
					urls: "stun:global.stun.twilio.com:3478"
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

	const negotiationChannel = peerConnection.createDataChannel("negotiation", {
		id: 1,
		negotiated: true,
		ordered: true
	});
	negotiationChannel.binaryType = "arraybuffer";

	let ignoreOffer = false;
	const polite = true;
	let negOpen = false;
	negotiationChannel.onopen = () => {
		negOpen = true;
	}
	negotiationChannel.onmessage = async (event: MessageEvent<any>) => {
		try {
			const description = new RTCSessionDescription(JSON.parse(atob(event.data)));

			const offerCollision = (description.type === "offer") &&
				(description || peerConnection.signalingState !== "stable");
			ignoreOffer = !polite && offerCollision;
			if (ignoreOffer) {
				return;
			}

			await peerConnection.setRemoteDescription(description);

			if (description.type === "offer") {
				await peerConnection.setLocalDescription();
				negotiationChannel.send(btoa(JSON.stringify(peerConnection.localDescription)));
			}
		} catch (e) {
			console.error(e);
		}
	}

	peerConnection.onnegotiationneeded = async () => {
		if (!negOpen) {
			return;
		}
		try {
			await peerConnection.setLocalDescription();
			negotiationChannel.send(btoa(JSON.stringify(peerConnection.localDescription)));
		} catch (e) {
			console.error(e);
		}
	};

	if (!disableTrickle) {
		return Promise.resolve({ pc: peerConnection, dc: dataChannel })
	}
	// set up offer
	const offerDesc = await peerConnection.createOffer();
	try {
		await peerConnection.setLocalDescription(offerDesc)
	} catch (e) {
		return Promise.reject(e);
	}

	peerConnection.onicecandidate = async event => {
		if (event.candidate !== null) {
			return;
		}
		pResolve({ pc: peerConnection, dc: dataChannel });
	}

	return result;
}
