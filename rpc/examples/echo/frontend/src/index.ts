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
	}
}

async function getClients() {
	const webrtcHost = window.webrtcHost;
	const opts: DialOptions = {
		credentials: window.creds,
		externalAuthAddress: window.externalAuthAddr,
		externalAuthToEntity: window.externalAuthToEntity,
		webrtcOptions: {
			disableTrickleICE: false,
			signalingCredentials: window.creds,
		}
	};
	if (opts.externalAuthAddress) {
		// we are authenticating against the external address and then
		// we will authenticate for externalAuthToEntity.
		opts.authEntity = opts.externalAuthAddress.replace(/^(.*:\/\/)/, '');

		// do similar for WebRTC
		opts.webrtcOptions!.signalingExternalAuthAddress = opts.externalAuthAddress;
		opts.webrtcOptions!.signalingExternalAuthToEntity = opts.externalAuthToEntity;
	}
	console.log("WebRTC")
	const webRTCConn = await dialWebRTC(thisHost, webrtcHost, opts);
	const webrtcClient = new EchoServiceClient(webrtcHost, { transport: webRTCConn.transportFactory });
	await doEchos(webrtcClient);

	console.log("Direct") // bi-di may not work
	const directTransport = await dialDirect(thisHost, opts);
	const directClient = new EchoServiceClient(thisHost, { transport: directTransport });
	await doEchos(directClient);
}
getClients().catch(e => {
	console.error("error getting clients", e);
});

async function doEchos(client: EchoServiceClient) {
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
		console.log(resp.toObject());
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
		console.log(message.toObject());
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
		console.log(message.toObject());
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
