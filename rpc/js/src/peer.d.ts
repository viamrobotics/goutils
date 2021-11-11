interface ReadyPeer {
    pc: RTCPeerConnection;
    dc: RTCDataChannel;
}
export declare function newPeerConnectionForClient(disableTrickle: boolean, rtcConfig?: RTCConfiguration): Promise<ReadyPeer>;
export {};
