package codec

import "crypto/tls"

// ALPN is the application-layer protocol negotiation token for the rmtt
// protocol over TLS and QUIC transports. QUIC requires ALPN (RFC 9001), and the
// Java (Netty) reference implementation requires the server to negotiate the
// exact token "rmtt"; both peers must therefore offer it via
// tls.Config.NextProtos.
const ALPN = "rmtt"

// WithALPN returns a copy of cfg that is guaranteed to offer the rmtt ALPN
// token, prepending it when it is not already present. The input is never
// modified: the returned copy leaves a caller's shared *tls.Config — for
// example one also handed to a WSS listener, which must NOT advertise "rmtt" —
// untouched. A nil cfg yields nil.
func WithALPN(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	out := cfg.Clone()
	for _, p := range out.NextProtos {
		if p == ALPN {
			return out
		}
	}
	out.NextProtos = append([]string{ALPN}, out.NextProtos...)
	return out
}
