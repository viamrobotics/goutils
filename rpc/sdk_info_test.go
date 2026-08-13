package rpc

import (
	"context"
	"strings"
	"testing"

	"go.viam.com/test"
	"google.golang.org/grpc/metadata"
)

func TestSDKInfoFromCtx(t *testing.T) {
	longSource := strings.Repeat("a", 200)

	for _, tc := range []struct {
		name     string
		md       map[string]string
		expected SDKInfo
		label    string
	}{
		{
			name:     "typescript with known source",
			md:       map[string]string{ViamClientMetadataField: "typescript(viam-app);v0.74.2;v0.1.573"},
			expected: SDKInfo{Type: SDKTypeTypeScript, Source: "viam-app", Version: "v0.74.2"},
			label:    "typescript(viam-app)",
		},
		{
			name:     "go",
			md:       map[string]string{ViamClientMetadataField: "go;v0.1.2;v0.3.4"},
			expected: SDKInfo{Type: SDKTypeGo, Version: "v0.1.2"},
			label:    "go",
		},
		{
			name:     "python",
			md:       map[string]string{ViamClientMetadataField: "python;v1.2.3;v0.1.0"},
			expected: SDKInfo{Type: SDKTypePython, Version: "v1.2.3"},
			label:    "python",
		},
		{
			name:     "type only",
			md:       map[string]string{ViamClientMetadataField: "go"},
			expected: SDKInfo{Type: SDKTypeGo},
			label:    "go",
		},
		{
			name:     "type is case insensitive",
			md:       map[string]string{ViamClientMetadataField: "TypeScript;v1.2.3;v0.1.0"},
			expected: SDKInfo{Type: SDKTypeTypeScript, Version: "v1.2.3"},
			label:    "typescript",
		},
		{
			// An unrecognized source is dropped rather than labeled, but the raw value is kept
			// so the unrecognized embedder is still discoverable.
			name:     "unknown source drops to bare SDK",
			md:       map[string]string{ViamClientMetadataField: "typescript(bogus);v1.2.3;v0.1.0"},
			expected: SDKInfo{Type: SDKTypeTypeScript, Version: "v1.2.3", Raw: "typescript(bogus);v1.2.3;v0.1.0"},
			label:    "typescript",
		},
		{
			name:     "unknown SDK keeps raw",
			md:       map[string]string{ViamClientMetadataField: "rust;v1.2.3;v0.1.0"},
			expected: SDKInfo{Version: "v1.2.3", Raw: "rust;v1.2.3;v0.1.0"},
			label:    "",
		},
		{
			// Regression: viam_client used to shadow the x-grpc-web fallback entirely, so every
			// TypeScript client reported as unknown once the SDK began sending viam_client.
			name: "viam_client wins over x-grpc-web",
			md: map[string]string{
				ViamClientMetadataField: "typescript(viam-app);v0.74.2;v0.1.573",
				XGRPCWebMetadataField:   "1",
			},
			expected: SDKInfo{Type: SDKTypeTypeScript, Source: "viam-app", Version: "v0.74.2"},
			label:    "typescript(viam-app)",
		},
		{
			// The fallback classifies the client, but the unrecognized value is still surfaced:
			// a new TypeScript-based SDK would otherwise be labeled and never noticed.
			name: "falls back to x-grpc-web when viam_client is unrecognized",
			md: map[string]string{
				ViamClientMetadataField: "rust;v1.2.3;v0.1.0",
				XGRPCWebMetadataField:   "1",
			},
			expected: SDKInfo{Type: SDKTypeTypeScript, Version: "v1.2.3", Raw: "rust;v1.2.3;v0.1.0"},
			label:    "typescript",
		},
		{
			// The source belongs to the SDK the client claimed, not to the one the fallback
			// inferred, so it must not survive into the label.
			name: "fallback does not adopt a source from an unrecognized SDK",
			md: map[string]string{
				ViamClientMetadataField: "rust(viam-app);v1.2.3;v0.1.0",
				XGRPCWebMetadataField:   "1",
			},
			expected: SDKInfo{Type: SDKTypeTypeScript, Version: "v1.2.3", Raw: "rust(viam-app);v1.2.3;v0.1.0"},
			label:    "typescript",
		},
		{
			name:     "x-grpc-web alone",
			md:       map[string]string{XGRPCWebMetadataField: "1"},
			expected: SDKInfo{Type: SDKTypeTypeScript},
			label:    "typescript",
		},
		{
			name:     "tonic user agent alone",
			md:       map[string]string{UserAgentMetadataField: "tonic/0.12.3"},
			expected: SDKInfo{Type: SDKTypePythonOrCPP},
			label:    "python/c++",
		},
		{
			name:     "unrecognized user agent",
			md:       map[string]string{UserAgentMetadataField: "grpc-go/1.65.0"},
			expected: SDKInfo{},
			label:    "",
		},
		{
			name:     "empty metadata",
			md:       map[string]string{},
			expected: SDKInfo{},
			label:    "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.New(tc.md))
			info := SDKInfoFromCtx(ctx)
			test.That(t, info, test.ShouldResemble, tc.expected)
			test.That(t, info.Label(), test.ShouldEqual, tc.label)
		})
	}

	t.Run("no incoming metadata", func(t *testing.T) {
		info := SDKInfoFromCtx(context.Background())
		test.That(t, info, test.ShouldResemble, SDKInfo{})
		test.That(t, info.Label(), test.ShouldEqual, "")
	})

	t.Run("raw is truncated", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
			ViamClientMetadataField: "typescript(" + longSource + ");v1.2.3;v0.1.0",
		}))
		info := SDKInfoFromCtx(ctx)
		test.That(t, len(info.Raw), test.ShouldEqual, maxSDKRawLen)
		test.That(t, info.Type, test.ShouldEqual, SDKTypeTypeScript)
		test.That(t, info.Source, test.ShouldEqual, "")
		test.That(t, info.Label(), test.ShouldEqual, "typescript")
	})
}
