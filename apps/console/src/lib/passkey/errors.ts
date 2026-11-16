// the passkey error type on its own, so the console's error texts can name
// it without loading @soropass/core into every page

export type PasskeyErrorCode =
  // the person dismissed the browser's prompt
  | "cancelled"
  // the browser or authenticator cannot make an es256 passkey with user
  // verification
  | "unsupported"
  | "failed";

export class PasskeyError extends Error {
  readonly code: PasskeyErrorCode;
  constructor(code: PasskeyErrorCode, message: string) {
    super(message);
    this.name = "PasskeyError";
    this.code = code;
  }
}
