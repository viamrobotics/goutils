import { grpc } from "@improbable-eng/grpc-web";
import { Credentials, dialDirect, dialWebRTC } from "@viamrobotics/rpc";
import { DialOptions } from "@viamrobotics/rpc/src/dial";
import { EchoBiDiRequest, EchoMultipleRequest, EchoMultipleResponse, EchoRequest, EchoResponse } from "./gen/proto/rpc/examples/echo/v1/echo_pb";
import { EchoServiceClient, ServiceError } from "./gen/proto/rpc/examples/echo/v1/echo_pb_service";

const thisHost = `${window.location.protocol}//${window.location.host}`;

declare global {
	interface Window {
		webrtcHost: string;
		creds?: Credentials;
		externalAuthAddr?: string;
		externalAuthToEntity?: string;
		accessToken?: string;
	}
}

function createElemForResponse(text: string, method: string, type: string) {
  const selector = `[data-testid="${type}-${method}"]`;
  const elem = document.querySelector(selector);
  if (!elem) {
    throw new Error(`expecting to find selector '${selector}'`);
  }
  const inner = document.createElement("div");
  inner.setAttribute("data-testid", "message");
  inner.innerText = text;
  elem.appendChild(inner);
}

async function getClients() {
	const webrtcHost = window.webrtcHost;
	const opts: DialOptions = {
		externalAuthAddress: window.externalAuthAddr,
		externalAuthToEntity: window.externalAuthToEntity,
		webrtcOptions: {
			disableTrickleICE: false,
		}
	};

	if (!window.accessToken) {
		opts.credentials = window.creds;
		opts.webrtcOptions!.signalingCredentials = window.creds;
	} else {
		opts.accessToken = window.accessToken;
	}

	if (opts.externalAuthAddress) {
		if (!window.accessToken) {
			// we are authenticating against the external address and then
			// we will authenticate for externalAuthToEntity.
			opts.authEntity = opts.externalAuthAddress.replace(/^(.*:\/\/)/, '');
		}

		// do similar for WebRTC
		opts.webrtcOptions!.signalingExternalAuthAddress = opts.externalAuthAddress;
		opts.webrtcOptions!.signalingExternalAuthToEntity = opts.externalAuthToEntity;
	}

	const webRTCConn = await dialWebRTC(thisHost, webrtcHost, opts);
	const webrtcClient = new EchoServiceClient(webrtcHost, { transport: webRTCConn.transportFactory });
	await renderResponses(webrtcClient, "wrtc");

	const directTransport = await dialDirect(thisHost, opts);
	const directClient = new EchoServiceClient(thisHost, { transport: directTransport });
	await renderResponses(directClient, "direct");
}
getClients().catch(e => {
	console.error("error getting clients", e);
});

async function renderResponses(client: EchoServiceClient, method: string) {
	const echoRequest = new EchoRequest();
	echoRequest.setMessage("hello");

	let pResolve: (value: any) => void;
	let pReject: (reason?: any) => void;
	let done = new Promise<any>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});
	client.echo(echoRequest, (err: ServiceError, resp: EchoResponse) => {
		if (err) {
			console.error(err);
			pReject(err);
			return
		}
		createElemForResponse(resp.getMessage(), method, "unary");
		pResolve(resp);
	});
	await done;

	const echoMultipleRequest = new EchoMultipleRequest();
	echoMultipleRequest.setMessage("hello?");

	done = new Promise<any>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});
	const multiStream = client.echoMultiple(echoMultipleRequest);
	multiStream.on("data", (message: EchoMultipleResponse) => {
		createElemForResponse(message.getMessage(), method, "multi");
	});
	multiStream.on("end", ({ code, details }: { code: number, details: string, metadata: grpc.Metadata }) => {
		if (code !== 0) {
			console.log(code);
			console.log(details);
			pReject(code);
			return;
		}
		pResolve(undefined);
	});
	await done;

	const bidiStream = client.echoBiDi();

	let echoBiDiRequest = new EchoBiDiRequest();
	echoBiDiRequest.setMessage("one");

	done = new Promise<any>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});

	let msgCount = 0;
	bidiStream.on("data", (message: EchoMultipleResponse) => {
		msgCount++
		createElemForResponse(message.getMessage(), method, "bidi");
		if (msgCount == 3) {
			pResolve(undefined);
		}
	});
	bidiStream.on("end", ({ code, details }: { code: number, details: string, metadata: grpc.Metadata }) => {
		if (code !== 0) {
			console.log(code);
			console.log(details);
			pReject(code);
			return;
		}
	});

	bidiStream.write(echoBiDiRequest);
	await done;

	done = new Promise<any>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});
	msgCount = 0;

	echoBiDiRequest = new EchoBiDiRequest();
	echoBiDiRequest.setMessage("two");
	bidiStream.write(echoBiDiRequest);

	await done;
	bidiStream.end();
}
