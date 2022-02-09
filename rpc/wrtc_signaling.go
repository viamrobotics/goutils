package rpc

import (
	"encoding/base64"
	"encoding/json"

	"github.com/pion/webrtc/v3"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// Adapted from https://github.com/pion/webrtc/blob/master/examples/internal/signal/signal.go

// encodeSDP encodes the given SDP in base64.
func encodeSDP(sdp *webrtc.SessionDescription) (string, error) {
	b, err := json.Marshal(sdp)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(b), nil
}

// decodeSDP decodes the input from base64 into the given SDP.
func decodeSDP(in string, sdp *webrtc.SessionDescription) error {
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, sdp)
	if err != nil {
		return err
	}
	return err
}

func extendWebRTCConfig(original *webrtc.Configuration, optional *webrtcpb.WebRTCConfig) webrtc.Configuration {
	configCopy := *original
	if optional == nil {
		return configCopy
	}
	if len(optional.AdditionalIceServers) > 0 {
		iceServers := make([]webrtc.ICEServer, len(original.ICEServers)+len(optional.AdditionalIceServers))
		copy(iceServers, original.ICEServers)
		for _, server := range optional.AdditionalIceServers {
			iceServers = append(iceServers, webrtc.ICEServer{
				URLs:       server.Urls,
				Username:   server.Username,
				Credential: server.Credential,
			})
		}
		configCopy.ICEServers = iceServers
	}
	return configCopy
}
