package rpc

import (
	"encoding/base64"
	"encoding/json"
	"slices"

	"github.com/pion/stun"
	"github.com/samber/lo"
	"github.com/viamrobotics/webrtc/v3"

	"go.viam.com/utils"
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
	turnURI           *stun.URI
	turnPort          int
	turnScheme        stun.SchemeType
}

var validTurnSchemes = []stun.SchemeType{stun.SchemeTypeTURN, stun.SchemeTypeTURNS}

func (o extendWebRTCConfigOptions) nonZero() bool {
	return o.replaceUDPWithTCP || o.turnURI != nil || o.turnPort > 0 || slices.Contains(validTurnSchemes, o.turnScheme)
}

// extendWebRTCConfig will take a WebRTC configuration and an extension to that
// configuration obtained by calling `OptionalWebRTCConfig` against the
// signaling server and append the latter's ICE servers and creds to the
// former. This is particularly useful for adding a TURN URL to the ICE servers
// list. `replaceUDPWithTCP`, when true, will replace URLs where
// "transport=udp" with the same URL with "transport=tcp"; this is useful when
// running behind a SOCKS proxy that can only forward the TCP protocol.
// `turnsHost`, when set, will filter TURN/TURNS ICE URLs to include at most
// one URL whose host matches the provided value, and set that URL's scheme to
// "stuns". `turnsPort`, if set, will override the port on any configured TURN
// URLs to the provided value.
func extendWebRTCConfig(logger utils.ZapCompatibleLogger, original *webrtc.Configuration, optional *webrtcpb.WebRTCConfig,
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
			if options.nonZero() {
				urls = lo.FilterMap(urls, func(rawUrl string, _ int) (string, bool) {
					uri, err := stun.ParseURI(rawUrl)
					if err != nil {
						logger.Warnw("Failed to parse ICE url, dropping from config", "url", rawUrl)
						return "", false
					}
					if slices.Contains(validTurnSchemes, uri.Scheme) {
						if options.turnURI != nil {
							if *options.turnURI != *uri {
								logger.Debugw("Found TURN/TURNS URI that doesn't match requested URI, dropping from config", "url", rawUrl)
								return "", false
							}
							logger.Debugw("Found configured TURN URI",
								"url", rawUrl, TURNURIEnvVar, options.turnURI.String())
						}
						if slices.Contains(validTurnSchemes, options.turnScheme) {
							logger.Debugw("Setting scheme for TURN host", "url", rawUrl, "scheme", options.turnScheme)
							uri.Scheme = options.turnScheme
						}
						if options.turnPort > 0 {
							logger.Debugw("Setting port for TURN host", "url", rawUrl, "port", options.turnPort)
							uri.Port = options.turnPort
						}
						if options.replaceUDPWithTCP {
							logger.Debugw("Setting protocol=tcp for TURN host", "url", rawUrl)
							uri.Proto = stun.ProtoTypeTCP
						}
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
	iceURLS := lo.Flatten(lo.Map(configCopy.ICEServers, func(s webrtc.ICEServer, _ int) []string {
		return s.URLs
	}))
	logger.Debugw("extended WebRTC config", "iceURLs", iceURLS)
	return configCopy
}
