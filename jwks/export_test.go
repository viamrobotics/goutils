package jwks

// NewCachingOIDCJWKKeyProviderForTest is NewCachingOIDCJWKKeyProvider with the minimum refresh
// interval and the per-request HTTP timeout exposed, so tests need not wait out the defaults.
var NewCachingOIDCJWKKeyProviderForTest = newCachingOIDCJWKKeyProvider
