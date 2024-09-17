import type { PartialMessage } from "@bufbuild/protobuf";
import { ConnectError, createPromiseClient, PromiseClient } from "@connectrpc/connect";
import { createWritableIterable } from "@connectrpc/connect/protocol";
import { dialDirect, dialWebRTC } from "@viamrobotics/rpc";
import { isNode } from "browser-or-node";
import { EchoService } from "./gen/proto/rpc/examples/echo/v1/echo_connect.js";
import { EchoBiDiRequest, EchoMultipleRequest, EchoRequest } from "./gen/proto/rpc/examples/echo/v1/echo_pb.js";

const thisHost = "http://localhost:8080";


async function getClients() {
	try {
		const webRTCConn = await dialWebRTC(thisHost, "echo-server", {});
		const webrtcClient = createPromiseClient(EchoService, webRTCConn.transport);
		await renderResponses(webrtcClient, "wrtc");
		webRTCConn.peerConnection.close();
	} catch (err) {
		console.error("error trying WebRTC", err);
	}

	try {
		const directTransport = await dialDirect(thisHost, {});
		const directClient = createPromiseClient(EchoService, directTransport);
		await renderResponses(directClient, "direct");
	} catch (err) {
		console.error("error trying direct", err);
	}
}

getClients().catch(e => {
	console.error("error getting clients", e);
});

async function renderResponses(client: PromiseClient<typeof EchoService>, method: string) {
	const echoRequest = new EchoRequest();
	echoRequest.message = "hello";

	console.log(`\n+++${method}+++`);
	console.log("---unary---")
	const response = await client.echo(echoRequest);
	console.log(response.message);

	const echoMultipleRequest = new EchoMultipleRequest();
	echoMultipleRequest.message = "hello?";

	console.log("---multi---")
	const multiStream = client.echoMultiple(echoMultipleRequest);
	try {
		for await (const response of multiStream) {
			console.log(response.message);
		}
	} catch (err) {
		if (err instanceof ConnectError) {
			console.log(err.code);
			console.log(err.details);
		}
		throw err;
	}

	if (!isNode) {
		// bidi unsupported in browser
		return;
	}

	console.log("---bidi---")
	const clientStream = createWritableIterable<PartialMessage<EchoBiDiRequest>>();
	const bidiStream = client.echoBiDi(clientStream);

	let msgCount = 0;
	const processResponses = async () => {
		try {
			for await (const response of bidiStream) {
				msgCount++
				console.log(response.message);
				if (msgCount == 3) {
					return;
				}
			}
		} catch (err) {
			if (err instanceof ConnectError) {
				console.log(err.code);
				console.log(err.details);
			}
			throw err;
		}
	}

	let echoBiDiRequest = new EchoBiDiRequest();
	echoBiDiRequest.message = "one";

	clientStream.write(echoBiDiRequest);
	await processResponses();

	msgCount = 0;

	echoBiDiRequest = new EchoBiDiRequest();
	echoBiDiRequest.message = "two";
	clientStream.write(echoBiDiRequest);

	await processResponses();
	clientStream.close();
	await processResponses();
}
