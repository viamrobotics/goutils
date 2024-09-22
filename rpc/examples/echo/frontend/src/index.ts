import type { PartialMessage } from "@bufbuild/protobuf";
import { ConnectError, createPromiseClient, PromiseClient } from "@connectrpc/connect";
import { createWritableIterable } from "@connectrpc/connect/protocol";
import { Credentials, dialDirect, dialWebRTC } from "@viamrobotics/rpc";
import { DialOptions } from "@viamrobotics/rpc/src/dial";
import { createWriteStream } from "fs";
import { EchoService } from "./gen/proto/rpc/examples/echo/v1/echo_connect";
import { EchoBiDiRequest, EchoBiDiResponse, EchoMultipleRequest, EchoMultipleResponse, EchoRequest, EchoResponse } from "./gen/proto/rpc/examples/echo/v1/echo_pb";

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
  const webrtcClient = createPromiseClient(EchoService, webRTCConn.transport);
  await renderResponses(webrtcClient, "wrtc");

  const directTransport = await dialDirect(thisHost, opts);
  const directClient = createPromiseClient(EchoService, directTransport);
  await renderResponses(directClient, "direct");
}

getClients().catch(e => {
  console.error("error getting clients", e);
});

async function renderResponses(client: PromiseClient<typeof EchoService>, method: string) {
  const echoRequest = new EchoRequest();
  echoRequest.message = "hello";

  const response = await client.echo(echoRequest);
  createElemForResponse(response.message, method, "unary");

  const echoMultipleRequest = new EchoMultipleRequest();
  echoMultipleRequest.message = "hello?";

  const multiStream = client.echoMultiple(echoMultipleRequest);
  try {
    for await (const response of multiStream) {
      createElemForResponse(response.message, method, "multi");
    }
  } catch (err) {
    if (err instanceof ConnectError) {
      console.log(err.code);
      console.log(err.details);
    }
    throw err;
  }

  if (method === "direct") {
    // grpc-web cannot do bidi correctly. Previous versions
    // of this code just stalled after the first message was received.
    return;
  }

  const clientStream = createWritableIterable<PartialMessage<EchoBiDiRequest>>();
  const bidiStream = client.echoBiDi(clientStream);

  let msgCount = 0;
  const processResponses = async () => {
    try {
      for await (const response of bidiStream) {
        msgCount++
        createElemForResponse(response.message, method, "bidi");
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
}
