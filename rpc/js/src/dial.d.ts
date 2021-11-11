import { ClientChannel } from "./ClientChannel";
interface DialOptions {
    disableTrickleICE: boolean;
    rtcConfig?: RTCConfiguration;
}
export declare function dial(signalingAddress: string, host: string, opts?: DialOptions): Promise<ClientChannel>;
export {};
