export interface Credentials {
  // TODO: Add doc comments for these properties
  type: string;
  payload: string;
}

export interface DialWebRTCOptions {
  // TODO: Add doc comments for these properties
  disableTrickleICE: boolean;
  rtcConfig?: RTCConfiguration;

  /** `signalingAuthEntity` is the entity to authenticate as to the signaler. */
  signalingAuthEntity?: string;

  /**
   * `signalingExternalAuthAddress` is the address to perform external auth yet.
   * This is unlikely to be needed since the signaler is typically in the same
   * place where authentication happens.
   */
  signalingExternalAuthAddress?: string;

  /**
   * `signalingExternalAuthToEntity` is the entity to authenticate for after
   * externally authenticating. This is unlikely to be needed since the signaler
   * is typically in the same place where authentication happens.
   */
  signalingExternalAuthToEntity?: string;

  /**
   * `signalingCredentials` are used to authenticate the request to the
   * signaling server.
   */
  signalingCredentials?: Credentials;

  /*
   * `signalingAccessToken` allows a pre-authenticated client to dial with
   * an authorization header to the signaling server. This skips the
   * Authenticate() request to the singaling server or external auth but does
   * not skip the AuthenticateTo() request to retrieve the credentials at the
   * external auth endpoint.
   *
   * If enabled, other auth options have no affect. Eg. authEntity, credentials,
   * signalingAuthEntity, signalingCredentials.
   */
  signalingAccessToken?: string;

  /**
   * `additionalSDPValues` is a collection of additional SDP values that we want
   * to pass into the connection's call request.
   */
  additionalSdpFields?: Record<string, string | number>;
}

export interface DialOptions {
  // TODO: Add doc comments for these properties
  authEntity?: string;
  credentials?: Credentials;
  webrtcOptions?: DialWebRTCOptions;
  externalAuthAddress?: string;
  externalAuthToEntity?: string;

  /**
   * `accessToken` allows a pre-authenticated client to dial with
   * an authorization header. Direct dial will have the access token
   * appended to the "Authorization: Bearer" header. WebRTC dial will
   * appened it to the signaling server communication
   *
   * If enabled, other auth options have no affect. Eg. authEntity,
   * credentials, externalAuthAddress, externalAuthToEntity,
   * webrtcOptions.signalingAccessToken
   */
  accessToken?: string;
}

const validateAccessToken = ({
  accessToken,
  authEntity,
  credentials,
  webrtcOptions,
}: DialOptions = {}) => {
  if (!accessToken || accessToken.length === 0) {
    return;
  }

  if (authEntity) {
    throw new Error('cannot set authEntity with accessToken');
  }

  if (credentials) {
    throw new Error('cannot set credentials with accessToken');
  }

  if (!webrtcOptions) {
    return;
  }

  const { signalingAccessToken, signalingAuthEntity, signalingCredentials } =
    webrtcOptions;

  if (signalingAccessToken) {
    throw new Error(
      'cannot set webrtcOptions.signalingAccessToken with accessToken'
    );
  }
  if (signalingAuthEntity) {
    throw new Error(
      'cannot set webrtcOptions.signalingAuthEntity with accessToken'
    );
  }
  if (signalingCredentials) {
    throw new Error(
      'cannot set webrtcOptions.signalingCredentials with accessToken'
    );
  }
};

const validateSignalingAccessToken = ({ webrtcOptions }: DialOptions = {}) => {
  if (!webrtcOptions) {
    return;
  }

  const { signalingAccessToken, signalingAuthEntity, signalingCredentials } =
    webrtcOptions;

  if (!signalingAccessToken || signalingAccessToken.length === 0) {
    return;
  }

  if (signalingAuthEntity) {
    throw new Error(
      'cannot set webrtcOptions.signalingAuthEntity with webrtcOptions.signalingAccessToken'
    );
  }
  if (signalingCredentials) {
    throw new Error(
      'cannot set webrtcOptions.signalingCredentials with webrtcOptions.signalingAccessToken'
    );
  }
};

export const validateDialOptions = (options: DialOptions = {}) => {
  validateAccessToken(options);
  validateSignalingAccessToken(options);
};
