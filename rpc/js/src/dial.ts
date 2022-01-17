import { grpc } from "@improbable-eng/grpc-web";
import { ClientChannel } from "./ClientChannel";
import { Code } from "./gen/google/rpc/code_pb";
import { Status } from "./gen/google/rpc/status_pb";
import { AuthenticateRequest, AuthenticateResponse, AuthenticateToRequest, AuthenticateToResponse, Credentials as PBCredentials } from "./gen/proto/rpc/v1/auth_pb";
import { AuthService, ExternalAuthService } from "./gen/proto/rpc/v1/auth_pb_service";
import { CallRequest, CallResponse, CallUpdateRequest, CallUpdateResponse, ICECandidate } from "./gen/proto/rpc/webrtc/v1/signaling_pb";
import { SignalingService } from "./gen/proto/rpc/webrtc/v1/signaling_pb_service";
import { newPeerConnectionForClient } from "./peer";

export interface DialOptions {
	authEntity?: string;
	credentials?: Credentials;
	webrtcOptions?: DialWebRTCOptions;
	externalAuthAddress?: string;
	externalAuthToEntity?: string;
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
}

export interface Credentials {
	type: string;
	payload: string;
}

export async function dialDirect(address: string, opts?: DialOptions): Promise<grpc.TransportFactory> {
	const defaultFactory = (opts: grpc.TransportOptions): grpc.Transport => {
		return grpc.CrossBrowserHttpTransport({ withCredentials: false })(opts);
	};
	if (!opts?.credentials) {
		return defaultFactory;
	}
	return makeAuthenticatedTransportFactory(address, defaultFactory, opts);
}

async function makeAuthenticatedTransportFactory(address: string, defaultFactory: grpc.TransportFactory, opts?: DialOptions): Promise<grpc.TransportFactory> {
	let accessToken = "";
	const getExtraMetadata = async (): Promise<grpc.Metadata> => {
		// TODO(https://github.com/viamrobotics/goutils/issues/13): handle expiration
		if (accessToken == "") {
			const request = new AuthenticateRequest();
			request.setEntity(opts?.authEntity ? opts.authEntity : address.replace(/^(.*:\/\/)/, ''));
			const creds = new PBCredentials();
			creds.setType(opts?.credentials?.type!);
			creds.setPayload(opts?.credentials?.payload!);
			request.setCredentials(creds);

			let pResolve: (value: grpc.Metadata) => void;
			let pReject: (reason?: any) => void;
			let done = new Promise<grpc.Metadata>((resolve, reject) => {
				pResolve = resolve;
				pReject = reject;
			});
			let thisAccessToken = "";
			grpc.invoke(AuthService.Authenticate, {
				request: request,
				host: opts?.externalAuthAddress ? opts.externalAuthAddress : address,
				transport: defaultFactory,
				onMessage: (message: AuthenticateResponse) => {
					thisAccessToken = message.getAccessToken();
				},
				onEnd: (code: grpc.Code, msg: string | undefined, trailers: grpc.Metadata) => {
					if (code == grpc.Code.OK) {
						pResolve(md);
					} else {
						pReject(msg);
					}
				}
			});
			await done;
			accessToken = thisAccessToken;

			if (opts?.externalAuthAddress && opts?.externalAuthToEntity) {
				const md = new grpc.Metadata();
				md.set("authorization", `Bearer ${accessToken}`);

				done = new Promise<grpc.Metadata>((resolve, reject) => {
					pResolve = resolve;
					pReject = reject;
				});
				thisAccessToken = "";

				const request = new AuthenticateToRequest();
				request.setEntity(opts.externalAuthToEntity);
				grpc.invoke(ExternalAuthService.AuthenticateTo, {
					request: request,
					host: opts.externalAuthAddress!,
					transport: defaultFactory,
					metadata: md,
					onMessage: (message: AuthenticateToResponse) => {
						thisAccessToken = message.getAccessToken();
					},
					onEnd: (code: grpc.Code, msg: string | undefined, trailers: grpc.Metadata) => {
						if (code == grpc.Code.OK) {
							pResolve(md);
						} else {
							pReject(msg);
						}
					}
				});
				await done;
				accessToken = thisAccessToken;
			}
		}
		const md = new grpc.Metadata();
		md.set("authorization", `Bearer ${accessToken}`);
		return md;
	}
	const extraMd = await getExtraMetadata();
	return (opts: grpc.TransportOptions): grpc.Transport => {
		return new authenticatedTransport(opts, defaultFactory, extraMd);
	};
}

class authenticatedTransport implements grpc.Transport {
	protected readonly opts: grpc.TransportOptions;
	protected readonly transport: grpc.Transport;
	protected readonly extraMetadata: grpc.Metadata;

	constructor(opts: grpc.TransportOptions, defaultFactory: grpc.TransportFactory, extraMetadata: grpc.Metadata) {
		this.opts = opts;
		this.extraMetadata = extraMetadata;
		this.transport = defaultFactory(opts);
	}

	public async start(metadata: grpc.Metadata) {
		this.extraMetadata.forEach((key, values) => {
			metadata.set(key, values);
		});
		this.transport.start(metadata);
	}

	public sendMessage(msgBytes: Uint8Array) {
		this.transport.sendMessage(msgBytes);
	}

	public finishSend() {
		this.transport.finishSend();
	}

	public cancel() {
		this.transport.cancel();
	}
}

interface WebRTCConnection {
	transportFactory: grpc.TransportFactory;
	peerConnection: RTCPeerConnection;
}

// dialWebRTC makes a connection to given host by signaling with the address provided. A Promise is returned
// upon successful connection that contains a transport factory to use with gRPC client as well as the WebRTC
// PeerConnection itself. Care should be taken with the PeerConnection and is currently returned for experimental
// use.
// TODO(https://github.com/viamrobotics/goutils/issues/19): figure out decent way to handle reconnect on connection termination
export async function dialWebRTC(signalingAddress: string, host: string, opts?: DialOptions): Promise<WebRTCConnection> {
	const webrtcOpts = opts?.webrtcOptions;
	const { pc, dc } = await newPeerConnectionForClient(webrtcOpts !== undefined && webrtcOpts.disableTrickleICE, webrtcOpts?.rtcConfig);

	// replace auth entity and creds
	let optsCopy = opts;
	if (opts) {
		optsCopy = { ...opts } as DialOptions;

		optsCopy.authEntity = opts?.webrtcOptions?.signalingAuthEntity
		if (!optsCopy.authEntity) {
			if (optsCopy.externalAuthAddress) {
				optsCopy.authEntity = opts.externalAuthAddress?.replace(/^(.*:\/\/)/, '');
			} else {
				optsCopy.authEntity = signalingAddress.replace(/^(.*:\/\/)/, '');
			}
		}
		optsCopy.credentials = opts?.webrtcOptions?.signalingCredentials
		optsCopy.externalAuthAddress = opts?.webrtcOptions?.signalingExternalAuthAddress
		optsCopy.externalAuthToEntity = opts?.webrtcOptions?.signalingExternalAuthToEntity
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
			transport: directTransport,
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
	if (!webrtcOpts?.disableTrickleICE) {
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
				transport: directTransport,
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

			if (webrtcOpts?.disableTrickleICE) {
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

	let clientEndResolve: () => void;
	let clientEndReject: (reason?: any) => void;
	let clientEnd = new Promise<void>((resolve, reject) => {
		clientEndResolve = resolve;
		clientEndReject = reject;
	});
	client.onEnd((status: grpc.Code, statusMessage: string, trailers: grpc.Metadata) => {
		if (status === grpc.Code.OK) {
			clientEndResolve();
			return;
		}
		console.error(statusMessage);
		clientEndReject(statusMessage);
	});
	client.start({ 'rpc-host': host })

	const callRequest = new CallRequest();
	const encodedSDP = btoa(JSON.stringify(pc.localDescription));
	callRequest.setSdp(encodedSDP);
	if (webrtcOpts && webrtcOpts.disableTrickleICE) {
		callRequest.setDisableTrickle(webrtcOpts.disableTrickleICE);
	}
	client.send(callRequest);

	const cc = new ClientChannel(pc, dc);
	cc.ready.then(() => clientEndResolve());
	await clientEnd;
	await cc.ready;
	exchangeDone = true;
	sendDone();

	if (opts?.externalAuthAddress) {
		// TODO(https://github.com/viamrobotics/goutils/issues/12): prepare AuthenticateTo here
		// for client channel.
	} else if (opts?.credentials?.type) {
		// TODO(https://github.com/viamrobotics/goutils/issues/12): prepare Authenticate here
		// for client channel
	}
	
	return { transportFactory: cc.transportFactory(), peerConnection: pc };
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

