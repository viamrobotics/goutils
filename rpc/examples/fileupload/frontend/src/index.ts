import { grpc } from "@improbable-eng/grpc-web";
import { UploadFileRequest, UploadFileResponse } from "proto/rpc/examples/fileupload/v1/fileupload_pb";
import { FileUploadServiceClient, ServiceError } from "proto/rpc/examples/fileupload/v1/fileupload_pb_service";
import { dial } from "rpc";

const signalingAddress = `${window.location.protocol}//${window.location.host}`;
const host = "local";

let clientResolve: (value: FileUploadServiceClient) => void;
let clientReject: (reason?: any) => void;
let clientProm = new Promise<FileUploadServiceClient>((resolve, reject) => {
	clientResolve = resolve;
	clientReject = reject;
});

dial(signalingAddress, host).then(async cc => {
	console.log("WebRTC connection established")
	const webrtcClient = new FileUploadServiceClient(host, { transport: cc.transportFactory() });
	clientResolve(webrtcClient);
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

async function doUpload(client: FileUploadServiceClient, name: string, data: Uint8Array) {
	let pResolve: (value: UploadFileResponse) => void;
	let pReject: (reason?: any) => void;
	let done = new Promise<UploadFileResponse>((resolve, reject) => {
		pResolve = resolve;
		pReject = reject;
	});

	const uploadStream = client.uploadFile();

	let uploadFileRequest = new UploadFileRequest();

	uploadFileRequest.setName(name);
	uploadStream.write(uploadFileRequest);

	uploadStream.on("data", (message: UploadFileResponse) => {
		pResolve(message);
	});
	uploadStream.on("end", ({ code, details }: { code: number, details: string, metadata: grpc.Metadata }) => {
		if (code !== 0) {
			console.log(code);
			console.log(details);
			pReject(code);
			return;
		}
	});

	for (let i = 0; i < data.byteLength; i += 1024) {
		uploadFileRequest = new UploadFileRequest();
		uploadFileRequest.setChunkData(data.slice(i, i + 1024));
		uploadStream.write(uploadFileRequest);
	}

	uploadStream.end();
	const resp = await done;
	console.log("upload complete", resp.toObject());
}
