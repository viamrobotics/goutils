export class ConnectionClosedError extends Error {
    constructor(msg: string) {
        super(msg);
        Object.setPrototypeOf(this, ConnectionClosedError.prototype);
    }
}
