interface ReadyPeer {
  pc: RTCPeerConnection;
  dc: RTCDataChannel;
}
export declare function addCustomSdpFields(
  localDescription?: RTCSessionDescription | null,
  sdpFields?: Record<string, string | number>
): {
  sdp: string | undefined;
  type: RTCSdpType | undefined;
};
export declare function newPeerConnectionForClient(
  disableTrickle: boolean,
  rtcConfig?: RTCConfiguration,
  additionalSdpFields?: Record<string, string | number>
): Promise<ReadyPeer>;
export {};
