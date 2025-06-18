package rpc

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/samber/lo"
	"github.com/viamrobotics/webrtc/v3"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// Adapted from https://github.com/pion/webrtc/blob/master/examples/internal/signal/signal.go

// EncodeSDP encodes the given SDP in base64.
func EncodeSDP(sdp *webrtc.SessionDescription) (string, error) {
	b, err := json.Marshal(sdp)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(b), nil
}

// DecodeSDP decodes the input from base64 into the given SDP.
func DecodeSDP(in string, sdp *webrtc.SessionDescription) error {
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

type extendWebRTCConfigOptions struct {
	replaceUDPWithTCP bool
	turnsDomain       string
}

// extendWebRTCConfig will take a WebRTC configuration and an extension to that
// configuration obtained by calling `OptionalWebRTCConfig` against the
// signaling server and append the latter's ICE servers and creds to the
// former. This is particularly useful for adding a TURN URL to the ICE servers
// list. `replaceUDPWithTCP`, when true, will replace URLs where
// "transport=udp" with the same URL with "transport=tcp"; this is useful when
// running behind a SOCKS proxy that can only forward the TCP protocol.
// `replaceTURNWithTURNS`, when true, will change the protocol from `turn` to
// `turns` on all TURN servers.
func extendWebRTCConfig(original *webrtc.Configuration, optional *webrtcpb.WebRTCConfig,
	options extendWebRTCConfigOptions,
) webrtc.Configuration {
	configCopy := *original
	if optional == nil {
		return configCopy
	}
	if len(optional.GetAdditionalIceServers()) > 0 {
		iceServers := make([]webrtc.ICEServer, len(original.ICEServers), len(original.ICEServers)+len(optional.GetAdditionalIceServers()))
		copy(iceServers, original.ICEServers)
		for _, server := range optional.GetAdditionalIceServers() {
			urls := server.GetUrls()
			if options.replaceUDPWithTCP || options.turnsDomain != "" {
				urls = lo.FilterMap(urls, func(rawUrl string, _ int) (string, bool) {
					uri, err := url.Parse(rawUrl)
					if err != nil {
						return "", false
					}
					if options.turnsDomain != "" && (uri.Scheme == "turn" || uri.Scheme == "turns") {
						// net/url doesn't parse the turn URIs exactly right
						host := strings.Split(uri.Opaque, ":")[0]
						if host != options.turnsDomain {
							return "", false
						}
						uri.Scheme = "turns"
					}
					if options.replaceUDPWithTCP {
						query := uri.Query()
						query.Set("transport", "tcp")
						uri.RawQuery = query.Encode()
					}
					return uri.String(), true
				})
			}
			if len(urls) == 0 {
				continue
			}

			iceServers = append(iceServers, webrtc.ICEServer{
				URLs:       urls,
				Username:   server.GetUsername(),
				Credential: server.GetCredential(),
			})
		}
		configCopy.ICEServers = iceServers
	}
	return configCopy
}
