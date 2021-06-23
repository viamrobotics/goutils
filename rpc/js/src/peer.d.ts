interface ReadyPeer {
    pc: RTCPeerConnection;
    dc: RTCDataChannel;
}
export declare function newPeerConnectionForClient(rtcConfig?: RTCConfiguration): Promise<ReadyPeer>;
export {};
