package rpc

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
)

// SDK types reported by Viam SDKs in the viam_client metadata field.
const (
	SDKTypeGo         = "go"
	SDKTypeTypeScript = "typescript"
	SDKTypePython     = "python"

	// SDKTypePythonOrCPP is reported when a tonic user agent is the only signal available.
	// Python and C++ both signal through viam-rust-utils, so at that point they cannot be
	// told apart.
	SDKTypePythonOrCPP = "python/c++"
)

// maxSDKRawLen bounds the client-supplied viam_client value before it is logged or stored.
const maxSDKRawLen = 128

// Recognized values. Anything else is dropped rather than labeled, so that a client cannot
// inflate the cardinality of metrics derived from SDKInfo.Label.
var (
	knownSDKTypes = map[string]bool{
		SDKTypeGo:         true,
		SDKTypeTypeScript: true,
		SDKTypePython:     true,
	}
	knownSDKSources = map[string]bool{
		"viam-app": true,
	}
)

// SDKInfo identifies the client SDK behind a request.
type SDKInfo struct {
	// Type is the bounded SDK identifier, empty when the SDK could not be determined.
	Type string

	// Source is the bounded embedder wrapping the SDK, e.g. "viam-app". Empty when the client
	// reported none or reported one that is not recognized.
	Source string

	Version string

	// Raw is the reported viam_client value, truncated to maxSDKRawLen and set only when it was
	// not fully understood — an unrecognized SDK or an unlisted source. It is how a new SDK or
	// embedder gets noticed: safe to log or store, never to use as a metric label.
	Raw string
}

// Label renders the SDK as a bounded metric label, qualified by source when one was
// recognized, e.g. "typescript(viam-app)". It returns "" when the SDK is unknown so that
// callers can apply their own sentinel.
func (i SDKInfo) Label() string {
	if i.Type == "" {
		return ""
	}
	if i.Source == "" {
		return i.Type
	}
	return i.Type + "(" + i.Source + ")"
}

// SDKInfoFromCtx determines which SDK issued the request described by ctx.
//
// A recognized viam_client value wins. The x-grpc-web and user-agent fallbacks cover clients
// that do not send one: SDK releases predating viam_client, and the Python and C++ SDKs, whose
// signaling is performed by viam-rust-utils rather than by the SDK itself.
func SDKInfoFromCtx(ctx context.Context) SDKInfo {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return SDKInfo{}
	}

	var info SDKInfo
	if vals := md.Get(ViamClientMetadataField); len(vals) > 0 && vals[0] != "" {
		info = parseViamClient(vals[0])
		if info.Type != "" {
			return info
		}
	}

	// A version reported alongside an unrecognized SDK belongs to that SDK, not to the one the
	// fallback infers, so it is dropped. Raw still carries it.
	if vals := md.Get(XGRPCWebMetadataField); len(vals) > 0 && vals[0] == "1" {
		info.Type = SDKTypeTypeScript
		info.Version = ""
		return info
	}
	if vals := md.Get(UserAgentMetadataField); len(vals) > 0 && strings.Contains(vals[0], "tonic/") {
		info.Type = SDKTypePythonOrCPP
		info.Version = ""
	}
	return info
}

// parseViamClient parses a "[type];[version];[apiVersion]" viam_client value, whose type may
// carry a source qualifier as in "typescript(viam-app)".
func parseViamClient(raw string) SDKInfo {
	var info SDKInfo
	sdkField, rest, _ := strings.Cut(raw, ";")
	info.Version, _, _ = strings.Cut(rest, ";")

	base, source, hasSource := strings.Cut(strings.ToLower(strings.TrimSpace(sdkField)), "(")
	if knownSDKTypes[base] {
		info.Type = base

		// Only meaningful against a recognized SDK. Keeping it otherwise would let a fallback
		// stamp its own Type onto someone else's source, e.g. "rust(viam-app)" over gRPC-Web
		// labeling as "typescript(viam-app)".
		if hasSource {
			if source = strings.TrimSuffix(source, ")"); knownSDKSources[source] {
				info.Source = source
			}
		}
	}

	if info.Type == "" || (hasSource && info.Source == "") {
		info.Raw = raw
		if len(info.Raw) > maxSDKRawLen {
			info.Raw = strings.ToValidUTF8(info.Raw[:maxSDKRawLen], "")
		}
	}
	return info
}
