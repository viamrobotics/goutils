import { ClientChannel } from "./ClientChannel";
export declare function dial(signalingAddress: string, host: string, rtcConfig?: RTCConfiguration): Promise<ClientChannel>;
