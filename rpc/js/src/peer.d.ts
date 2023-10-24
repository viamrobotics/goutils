interface ReadyPeer {
  pc: RTCPeerConnection;
  dc: RTCDataChannel;
}
export declare function newPeerConnectionForClient(
  disableTrickle: boolean,
  rtcConfig?: RTCConfiguration,
  priority?: number
): Promise<ReadyPeer>;
export {};
