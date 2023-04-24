export class ConnectionClosedError extends Error {
  constructor(msg: string) {
    super(msg);
    Object.setPrototypeOf(this, ConnectionClosedError.prototype);
  }

  static isError(error: any): boolean {
    if (error instanceof ConnectionClosedError) {
      return true;
    }
    if (typeof error === "string") {
      return error === "Response closed without headers";
    }
    if (error instanceof Error) {
      return error.message === "Response closed without headers";
    }
    return false;
  }
}

export class GRPCError extends Error {
  public readonly code: number;
  public readonly grpcMessage: string;

  constructor(code: number, message: string) {
    super(`Code=${code} Message=${message}`);
    this.code = code;
    this.grpcMessage = message;
    Object.setPrototypeOf(this, GRPCError.prototype);
  }
}
