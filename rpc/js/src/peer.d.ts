interface ReadyPeer {
  pc: RTCPeerConnection;
  dc: RTCDataChannel;
}
export declare function addCustomSdpFields(
  sdpFields?: object,
  localDescription?: RTCSessionDescription | null
): {
  sdp: string | undefined;
  type: RTCSdpType | undefined;
};
export declare function newPeerConnectionForClient(
  disableTrickle: boolean,
  rtcConfig?: RTCConfiguration,
  additionalSdpFields?: object
): Promise<ReadyPeer>;
export {};
