import { grpc } from "@improbable-eng/grpc-web";
import type { ProtobufMessage } from "@improbable-eng/grpc-web/dist/typings/message";
import { ClientChannel } from "./ClientChannel";
import { ConnectionClosedError } from "./errors";
import { Code } from "./gen/google/rpc/code_pb";
import { Status } from "./gen/google/rpc/status_pb";
import { AuthenticateRequest, AuthenticateResponse, AuthenticateToRequest, AuthenticateToResponse, Credentials as PBCredentials } from "./gen/proto/rpc/v1/auth_pb";
import { AuthService, ExternalAuthService } from "./gen/proto/rpc/v1/auth_pb_service";
import { CallRequest, CallResponse, CallUpdateRequest, CallUpdateResponse, ICECandidate, WebRTCConfig, OptionalWebRTCConfigRequest, OptionalWebRTCConfigResponse } from "./gen/proto/rpc/webrtc/v1/signaling_pb";
import { SignalingService } from "./gen/proto/rpc/webrtc/v1/signaling_pb_service";
import { newPeerConnectionForClient } from "./peer";

export interface DialOptions {
	authEntity?: string;
	credentials?: Credentials;
	webrtcOptions?: DialWebRTCOptions;
	externalAuthAddress?: string;
	externalAuthToEntity?: string;

	// `accessToken` allows a pre-authenticated client to dial with
	// an authorization header. Direct dial will have the access token
	// appended to the "Authorization: Bearer" header. WebRTC dial will
	// appened it to the signaling server communication
	//
	// If enabled, other auth options have no affect. Eg. authEntity, credentials, 
	// externalAuthAddress, externalAuthToEntity, webrtcOptions.signalingAccessToken
	accessToken?: string;
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

	// `signalingAccessToken` allows a pre-authenticated client to dial with
	// an authorization header to the signaling server. This skips the Authenticate()
	// request to the singaling server or external auth but does not skip the 
	// AuthenticateTo() request to retrieve the credentials at the external auth
	// endpoint.
	//
	// If enabled, other auth options have no affect. Eg. authEntity, credentials, signalingAuthEntity, signalingCredentials.
	signalingAccessToken?: string;
}

export interface Credentials {
	type: string;
	payload: string;
}

export async function dialDirect(address: string, opts?: DialOptions): Promise<grpc.TransportFactory> {
	validateDialOptions(opts);

	const defaultFactory = (opts: grpc.TransportOptions): grpc.Transport => {
		return grpc.CrossBrowserHttpTransport({ withCredentials: false })(opts);
	};

	// Client already has access token with no external auth, skip Authenticate process.
	if (opts?.accessToken && !(opts?.externalAuthAddress && opts?.externalAuthToEntity)) {
		const md = new grpc.Metadata();
		md.set("authorization", `Bearer ${opts.accessToken}`);
		return (opts: grpc.TransportOptions): grpc.Transport => {
			return new authenticatedTransport(opts, defaultFactory, md);
		};
	}
	
	if (!opts || (!opts?.credentials && !opts?.accessToken)) {
		return defaultFactory;
	}

	return makeAuthenticatedTransportFactory(address, defaultFactory, opts);
}

async function makeAuthenticatedTransportFactory(address: string, defaultFactory: grpc.TransportFactory, opts: DialOptions): Promise<grpc.TransportFactory> {
	let accessToken = "";
	const getExtraMetadata = async (): Promise<grpc.Metadata> => {
		// TODO(GOUT-10): handle expiration
		if (accessToken == "") {
			let thisAccessToken = "";

			let pResolve: (value: grpc.Metadata) => void;
			let pReject: (reason?: unknown) => void;

			if (!opts.accessToken || opts.accessToken === "") {
				const request = new AuthenticateRequest();
				request.setEntity(opts.authEntity ? opts.authEntity : address.replace(/^(.*:\/\/)/, ''));
				const creds = new PBCredentials();
				creds.setType(opts.credentials?.type!);
				creds.setPayload(opts.credentials?.payload!);
				request.setCredentials(creds);

				let done = new Promise<grpc.Metadata>((resolve, reject) => {
					pResolve = resolve;
					pReject = reject;
				});

				grpc.invoke(AuthService.Authenticate, {
					request: request,
					host: opts.externalAuthAddress ? opts.externalAuthAddress : address,
					transport: defaultFactory,
					onMessage: (message: AuthenticateResponse) => {
						thisAccessToken = message.getAccessToken();
					},
					onEnd: (code: grpc.Code, msg: string | undefined, _trailers: grpc.Metadata) => {
						if (code == grpc.Code.OK) {
							pResolve(md);
						} else {
							pReject(msg);
						}
					}
				});
				await done;
			} else {
				thisAccessToken = opts.accessToken;
			}

			accessToken = thisAccessToken;

			if (opts.externalAuthAddress && opts.externalAuthToEntity) {
				const md = new grpc.Metadata();
				md.set("authorization", `Bearer ${accessToken}`);

				let done = new Promise<grpc.Metadata>((resolve, reject) => {
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
					onEnd: (code: grpc.Code, msg: string | undefined, _trailers: grpc.Metadata) => {
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

	public start(metadata: grpc.Metadata) {
		this.extraMetadata.forEach((key: string, values: string | string[]) => {
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

async function getOptionalWebRTCConfig(signalingAddress: string, host: string, opts?: DialOptions): Promise<WebRTCConfig> {
    const optsCopy = { ...opts } as DialOptions;
		const directTransport = await dialDirect(signalingAddress, optsCopy);

    let pResolve: (value: WebRTCConfig) => void;
    let pReject: (reason?: unknown) => void;

    let result: WebRTCConfig | undefined;
    let done = new Promise<WebRTCConfig>((resolve, reject) => {
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
      }
    });

    await done;

    if (!result) {
      throw new Error("no config");
    }
    return result;
}

// dialWebRTC makes a connection to given host by signaling with the address provided. A Promise is returned
// upon successful connection that contains a transport factory to use with gRPC client as well as the WebRTC
// PeerConnection itself. Care should be taken with the PeerConnection and is currently returned for experimental
// use.
// TODO(GOUT-7): figure out decent way to handle reconnect on connection termination
export async function dialWebRTC(signalingAddress: string, host: string, opts?: DialOptions): Promise<WebRTCConnection> {
	validateDialOptions(opts);

  // TODO(RSDK-2836): In general, this logic should be in parity with the golang implementation.
  // https://github.com/viamrobotics/goutils/blob/main/rpc/wrtc_client.go#L160-L175
  const config = await getOptionalWebRTCConfig(signalingAddress, host, opts);
  const additionalIceServers: RTCIceServer[] = config.toObject().additionalIceServersList.map((ice) => {
    return {
      urls: ice.urlsList,
      credential: ice.credential,
      username: ice.username,
    }
  });

  if (!opts) {
    opts = {};
  }

  let webrtcOpts: DialWebRTCOptions;
  if (!opts.webrtcOptions) {
    // use additional webrtc config as default
    webrtcOpts = {
      disableTrickleICE: config.getDisableTrickle(),
      rtcConfig: {
        iceServers: additionalIceServers,
      }
    };
  } else {
    webrtcOpts = opts.webrtcOptions;
    if (!webrtcOpts.rtcConfig) {
      webrtcOpts.rtcConfig = { iceServers: additionalIceServers };
    } else {
      webrtcOpts.rtcConfig.iceServers = [
        ...(webrtcOpts.rtcConfig.iceServers || []),
        ...additionalIceServers
      ];
    }
  }

	const { pc, dc } = await newPeerConnectionForClient(webrtcOpts !== undefined && webrtcOpts.disableTrickleICE, webrtcOpts?.rtcConfig);
	let successful = false;

	try {
		// replace auth entity and creds
		let optsCopy = opts;
		if (opts) {
			optsCopy = { ...opts } as DialOptions;

			if (!opts.accessToken) {
				optsCopy.authEntity = opts?.webrtcOptions?.signalingAuthEntity;
				if (!optsCopy.authEntity) {
					if (optsCopy.externalAuthAddress) {
						optsCopy.authEntity = opts.externalAuthAddress?.replace(/^(.*:\/\/)/, '');
					} else {
						optsCopy.authEntity = signalingAddress.replace(/^(.*:\/\/)/, '');
					}
				}
				optsCopy.credentials = opts?.webrtcOptions?.signalingCredentials;
				optsCopy.accessToken = opts?.webrtcOptions?.signalingAccessToken;
			}

			optsCopy.externalAuthAddress = opts?.webrtcOptions?.signalingExternalAuthAddress;
			optsCopy.externalAuthToEntity = opts?.webrtcOptions?.signalingExternalAuthToEntity;
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

		let pResolve: (value: unknown) => void;
		let remoteDescSet = new Promise<unknown>((resolve) => {
			pResolve = resolve;
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
		// TS says that CallResponse isn't a valid type here. More investigation required.
		client.onMessage(async (message: ProtobufMessage) => {
			const response = message as CallResponse

			if (response.hasInit()) {
				if (haveInit) {
					sendError("got init stage more than once");
					return;
				}
				const init = response.getInit()!;
				haveInit = true;
				uuid = response.getUuid();

				const remoteSDP = new RTCSessionDescription(JSON.parse(atob(init.getSdp())));
				pc.setRemoteDescription(remoteSDP);

				pResolve(true);

				if (webrtcOpts?.disableTrickleICE) {
					exchangeDone = true;
					sendDone();
					return;
				}
			} else if (response.hasUpdate()) {
				if (!haveInit) {
					sendError("got update stage before init stage");
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
					return;
				}
			} else {
				sendError("unknown CallResponse stage");
				return;
			}
		});

		let clientEndResolve: () => void;
		let clientEndReject: (reason?: unknown) => void;
		let clientEnd = new Promise<void>((resolve, reject) => {
			clientEndResolve = resolve;
			clientEndReject = reject;
		});
		client.onEnd((status: grpc.Code, statusMessage: string, _trailers: grpc.Metadata) => {
			if (status === grpc.Code.OK) {
				clientEndResolve();
				return;
			}
			if (statusMessage === "Response closed without headers") {
				clientEndReject(new ConnectionClosedError("failed to dial"));
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
		cc.ready.then(() => clientEndResolve()).catch(err => clientEndReject(err));
		await clientEnd;
		await cc.ready;
		exchangeDone = true;
		sendDone();

		if (opts?.externalAuthAddress) {
			// TODO(GOUT-11): prepare AuthenticateTo here
			// for client channel.
		} else if (opts?.credentials?.type) {
			// TODO(GOUT-11): prepare Authenticate here
			// for client channel
		}
	
		successful = true;
		return { transportFactory: cc.transportFactory(), peerConnection: pc };
	} finally {
		if (!successful) {
			pc.close();
		}
	}
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

function validateDialOptions(opts?: DialOptions) {
	if (!opts) {
		return;
	}

	if (opts.accessToken && opts.accessToken.length > 0) {
		if (opts.authEntity) {
			throw new Error("cannot set authEntity with accessToken");
		}

		if (opts.credentials) {
			throw new Error("cannot set credentials with accessToken");
		}

		if (opts.webrtcOptions) {
			if (opts.webrtcOptions.signalingAccessToken) {
				throw new Error("cannot set webrtcOptions.signalingAccessToken with accessToken");
			}
			if (opts.webrtcOptions.signalingAuthEntity) {
				throw new Error("cannot set webrtcOptions.signalingAuthEntity with accessToken");
			}
			if (opts.webrtcOptions.signalingCredentials) {
				throw new Error("cannot set webrtcOptions.signalingCredentials with accessToken");
			}
		}
	}

	if (opts?.webrtcOptions?.signalingAccessToken && opts.webrtcOptions.signalingAccessToken.length > 0) {
		if (opts.webrtcOptions.signalingAuthEntity) {
			throw new Error("cannot set webrtcOptions.signalingAuthEntity with webrtcOptions.signalingAccessToken");
		}
		if (opts.webrtcOptions.signalingCredentials) {
			throw new Error("cannot set webrtcOptions.signalingCredentials with webrtcOptions.signalingAccessToken");
		}
	}
}
