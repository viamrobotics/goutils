import { grpc } from "@improbable-eng/grpc-web";
import { CallRequest, CallResponse, CallUpdateRequest, CallUpdateResponse, ICECandidate } from "proto/rpc/webrtc/v1/signaling_pb";
import { SignalingService } from "proto/rpc/webrtc/v1/signaling_pb_service";
import { ClientChannel } from "./ClientChannel";
import { newPeerConnectionForClient } from "./peer";
import { Code } from "google-rpc/code_pb"
import { Status } from "google-rpc/status_pb"

interface DialOptions {
	disableTrickleICE: boolean;
	rtcConfig?: RTCConfiguration;
}

// TODO(https://github.com/viamrobotics/core/issues/111): figure out decent way to handle reconnect on connection termination
export async function dial(signalingAddress: string, host: string, opts?: DialOptions): Promise<ClientChannel> {
	const { pc, dc } = await newPeerConnectionForClient(opts !== undefined && opts.disableTrickleICE, opts?.rtcConfig);

	const client = grpc.client(SignalingService.Call, {
		host: signalingAddress
	})

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
			onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
				const { status, statusMessage, message } = output;
				if (status === grpc.Code.OK && message) {
					return;
				}
				console.error(statusMessage)
			}
		});
	}
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
			onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
				const { status, statusMessage, message } = output;
				if (status === grpc.Code.OK && message) {
					return;
				}
				console.error(statusMessage)
			}
		});
	}

	let pResolve: (value: any) => void;
	let pReject: (reason?: any) => void;
	let remoteDescSet = new Promise<any>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});
	let exchangeDone = false;
	if (!opts?.disableTrickleICE) {
		// set up offer
		const offerDesc = await pc.createOffer();

		let iceComplete = false;
		pc.onicecandidate = async event => {
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
				onEnd: (output: grpc.UnaryOutput<CallUpdateResponse>) => {
					const { status, statusMessage, message } = output;
					if (status === grpc.Code.OK && message) {
						return;
					}
					if (exchangeDone || iceComplete) {
						return;	
					}
					console.error("error sending candidate", statusMessage)
				}
			});
		}

		await pc.setLocalDescription(offerDesc);
	}

	let haveInit = false;
	client.onMessage(async (message: CallResponse) => {
		if (message.hasInit()) {
			if (haveInit) {
				sendError("got init stage more than once");
				return;
			}
			const init = message.getInit()!;
			haveInit = true;
			uuid = message.getUuid();

			const remoteSDP = new RTCSessionDescription(JSON.parse(atob(init.getSdp())));
			pc.setRemoteDescription(remoteSDP);

			pResolve(true);

			if (opts?.disableTrickleICE) {
				exchangeDone = true;
				sendDone();
				return;
			}
		} else if (message.hasUpdate()) {
			if (!haveInit) {
				sendError("got update stage before init stage");
				return;
			}
			if (message.getUuid() !== uuid) {
				sendError(`uuid mismatch; have=${message.getUuid()} want=${uuid}`);
				return;
			}
			const update = message.getUpdate()!;
			const cand = iceCandidateFromProto(update.getCandidate()!);
			try {
				await pc.addIceCandidate(cand);
			} catch (e) {
				sendError(e);
				return;
			}
		} else {
			sendError("unknown CallResponse stage");
			return;
		}
	});

	client.onEnd((status: grpc.Code, statusMessage: string, trailers: grpc.Metadata) => {
		if (status === grpc.Code.OK) {
			return;
		}
		console.error(statusMessage);
	});
	client.start({ 'rpc-host': host })

	const callRequest = new CallRequest();
	const encodedSDP = btoa(JSON.stringify(pc.localDescription));
	callRequest.setSdp(encodedSDP);
	if (opts && opts.disableTrickleICE) {
		callRequest.setDisableTrickle(opts.disableTrickleICE);
	}
	client.send(callRequest);

	const cc = new ClientChannel(pc, dc);
	await cc.ready;
	exchangeDone = true;
	sendDone();
	return cc;
}

function iceCandidateFromProto(i: ICECandidate): RTCIceCandidateInit {
	let candidate: RTCIceCandidateInit = {
		candidate: i.getCandidate(),
	}
	if (i.hasSdpMid()) {
		candidate.sdpMid = i.getSdpMid();
	}
	if (i.hasSdpmLineIndex()) {
		candidate.sdpMLineIndex = i.getSdpmLineIndex();
	}
	if (i.hasUsernameFragment()) {
		candidate.usernameFragment = i.getUsernameFragment();
	}
	return candidate;
}

function iceCandidateToProto(i: RTCIceCandidateInit): ICECandidate {
	let candidate = new ICECandidate();
	candidate.setCandidate(i.candidate!);
	if (i.sdpMid) {
		candidate.setSdpMid(i.sdpMid);
	}
	if (i.sdpMLineIndex) {
		candidate.setSdpmLineIndex(i.sdpMLineIndex);
	}
	if (i.usernameFragment) {
		candidate.setUsernameFragment(i.usernameFragment);
	}
	return candidate;
}

