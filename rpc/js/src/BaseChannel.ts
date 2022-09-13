import { PacketMessage } from "./gen/proto/rpc/webrtc/v1/grpc"; 

export class BaseChannel {
	public readonly ready: Promise<any>;

	private readonly peerConn: RTCPeerConnection;
	private readonly dataChannel: RTCDataChannel;
	private pResolve: (value: any) => void;

	private closed: boolean;
	private closedReason?: Error;

	protected maxDataChannelSize = 16384;

	constructor(peerConn: RTCPeerConnection, dataChannel: RTCDataChannel) {
		this.peerConn = peerConn;
		this.dataChannel = dataChannel;

		this.ready = new Promise<any>(resolve => {
			this.pResolve = resolve;
		})

		dataChannel.onopen = () => this.onChannelOpen();
		dataChannel.onclose = () => this.onChannelClose();
		dataChannel.onerror = (ev: RTCErrorEvent) => this.onChannelError(ev);

		peerConn.oniceconnectionstatechange = () => console.log(peerConn.iceConnectionState);
	}

	public close() {
		this.closeWithReason(undefined);
	}

	public isClosed() {
		return this.closed
	}

	public isClosedReason() {
		return this.closedReason
	}

	private closeWithReason(err?: Error) {
		if (this.closed) {
			return;
		}
		this.closed = true;
		this.closedReason = err;
		this.peerConn.close();
	}

	private onChannelOpen() {
		this.pResolve(undefined);
	}

	private onChannelClose() {
		this.closeWithReason(new Error("data channel closed"));
	}

	private onChannelError(ev: RTCErrorEvent) {
		console.error("channel error", ev);
		this.closeWithReason(ev.error);
	}

	protected write(msg: PacketMessage) {
		this.dataChannel.send(msg.data);
	}
}
