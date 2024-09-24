export class ConnectionClosedError extends Error {
  public override readonly name = 'ConnectionClosedError';

  constructor(msg: string) {
    super(msg);
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
