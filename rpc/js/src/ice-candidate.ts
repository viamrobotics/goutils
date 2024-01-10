import { ICECandidate } from './gen/proto/rpc/webrtc/v1/signaling_pb';

export const iceCandidateFromProto = (
  candidate: ICECandidate
): RTCIceCandidateInit => {
  const init: RTCIceCandidateInit = {
    candidate: candidate.getCandidate(),
  };

  if (candidate.hasSdpMid()) {
    init.sdpMid = candidate.getSdpMid();
  }

  if (candidate.hasSdpmLineIndex()) {
    init.sdpMLineIndex = candidate.getSdpmLineIndex();
  }

  if (candidate.hasUsernameFragment()) {
    init.usernameFragment = candidate.getUsernameFragment();
  }

  return init;
};

export const iceCandidateToProto = (
  init: RTCIceCandidateInit
): ICECandidate => {
  const candidate = new ICECandidate();
  candidate.setCandidate(init.candidate ?? '');

  if (init.sdpMid) {
    candidate.setSdpMid(init.sdpMid);
  }

  if (init.sdpMLineIndex) {
    candidate.setSdpmLineIndex(init.sdpMLineIndex);
  }

  if (init.usernameFragment) {
    candidate.setUsernameFragment(init.usernameFragment);
  }

  return candidate;
};
