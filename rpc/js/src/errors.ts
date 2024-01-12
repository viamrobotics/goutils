/* eslint-disable max-classes-per-file */

export class ConnectionClosedError extends Error {
  constructor(msg: string) {
    super(msg);
    this.name = 'ConnectionClosedError';
    Object.setPrototypeOf(this, ConnectionClosedError.prototype);
  }

  static isError(error: unknown): boolean {
    if (error instanceof ConnectionClosedError) {
      return true;
    }

    if (typeof error === 'string') {
      return error === 'Response closed without headers';
    }

    if (error instanceof Error) {
      return error.message === 'Response closed without headers';
    }

    return false;
  }
}

export class GRPCError extends Error {
  constructor(
    readonly code: number,
    readonly grpcMessage: string
  ) {
    super(`Code=${code} Message=${grpcMessage}`);
    this.name = 'GRPCError';
    this.code = code;
    this.grpcMessage = grpcMessage;
    Object.setPrototypeOf(this, GRPCError.prototype);
  }
}
