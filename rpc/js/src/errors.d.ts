export declare class ConnectionClosedError extends Error {
    constructor(msg: string);
    static IsError(error: any): boolean;
}
export declare class GRPCError extends Error {
    readonly code: number;
    readonly grpcMessage: string;
    constructor(code: number, message: string);
}
