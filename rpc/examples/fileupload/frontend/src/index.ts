import { PartialMessage } from "@bufbuild/protobuf";
import { createPromiseClient, PromiseClient } from "@connectrpc/connect";
import { createWritableIterable } from "@connectrpc/connect/protocol";
import { Credentials, dialWebRTC } from "@viamrobotics/rpc";
import { DialOptions } from "@viamrobotics/rpc/src/dial";
import { FileUploadService } from "./gen/proto/rpc/examples/fileupload/v1/fileupload_connect";
import { UploadFileRequest, UploadFileResponse } from "./gen/proto/rpc/examples/fileupload/v1/fileupload_pb";

const thisHost = `${window.location.protocol}//${window.location.host}`;

declare global {
	interface Window {
		webrtcHost: string;
		creds?: Credentials;
		externalAuthAddr?: string;
		externalAuthToEntity?: string;
	}
}

let clientResolve: (value: PromiseClient<typeof FileUploadService>) => void;
let clientReject: (reason?: any) => void;
let clientProm = new Promise<PromiseClient<typeof FileUploadService>>((resolve, reject) => {
	clientResolve = resolve;
	clientReject = reject;
});

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
dialWebRTC(thisHost, webrtcHost, opts).then(async ({ transport }) => {
	console.log("WebRTC connection established")
	clientResolve(createPromiseClient(FileUploadService, transport));
}).catch((e: any) => clientReject(e));

window.onload = async (event) => {
	const form = document.getElementById('form')!;
	form.addEventListener('submit', async event => {
		event.preventDefault();
		console.debug("waiting for client to be ready");
		const client = await clientProm;
		console.debug("ready");
		const file = (document.getElementById('myFile') as HTMLInputElement)!;
		if (!file.files || file.files.length === 0) {
			return;
		}
		const fileToUpload = file.files![0];
		doUpload(client, fileToUpload.name, new Uint8Array(await fileToUpload.arrayBuffer()));
	});
};

async function doUpload(client: PromiseClient<typeof FileUploadService>, name: string, data: Uint8Array) {
	let pResolve: (value: UploadFileResponse) => void;
	let pReject: (reason?: any) => void;
	let done = new Promise<UploadFileResponse>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});


	const uploadStream = createWritableIterable<PartialMessage<UploadFileRequest>>();
	const resp = client.uploadFile(uploadStream);

	let uploadFileRequest = new UploadFileRequest();

	uploadFileRequest.data.case = "name";
	uploadFileRequest.data.value = name;
	uploadStream.write(uploadFileRequest);

	for (let i = 0; i < data.byteLength; i += 1024) {
		uploadFileRequest = new UploadFileRequest();
		uploadFileRequest.data.case = "chunkData";
		uploadFileRequest.data.value = data.slice(i, i + 1024);
		uploadStream.write(uploadFileRequest);
	}

	uploadStream.close();
	console.log("upload complete", (await resp).toJson());
}
