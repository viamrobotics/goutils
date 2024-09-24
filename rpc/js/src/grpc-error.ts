export class GRPCError extends Error {
  public override readonly name = 'GRPCError';
  public readonly code: number;
  public readonly grpcMessage: string;

  constructor(code: number, message: string) {
    super(`Code=${code} Message=${message}`);
    this.code = code;
    this.grpcMessage = message;
    Object.setPrototypeOf(this, GRPCError.prototype);
  }
}
