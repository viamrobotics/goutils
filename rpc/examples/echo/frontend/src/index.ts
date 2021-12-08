import { grpc } from "@improbable-eng/grpc-web";
import { EchoBiDiRequest, EchoMultipleRequest, EchoMultipleResponse, EchoRequest, EchoResponse } from "proto/rpc/examples/echo/v1/echo_pb";
import { EchoServiceClient, ServiceError } from "proto/rpc/examples/echo/v1/echo_pb_service";
import { dialDirect, dialWebRTC, Credentials } from "rpc";

const signalingAddress = `${window.location.protocol}//${window.location.host}`;
const host = "local";

declare global {
	interface Window {
		creds?: Credentials;
		externalAuthAddr?: string;
	}
}

async function getClients() {
	const opts = { credentials: window.creds, externalAuthAddress: window.externalAuthAddr };
	console.log("WebRTC")
	const webRTCTransport = await dialWebRTC(signalingAddress, host, opts);
	const webrtcClient = new EchoServiceClient(host, { transport: webRTCTransport });
	await doEchos(webrtcClient);

	console.log("Direct") // bi-di may not work
	const directTransport = await dialDirect(signalingAddress, opts);
	const directClient = new EchoServiceClient(signalingAddress, { transport: directTransport });
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
