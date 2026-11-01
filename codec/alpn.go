package codec

import "crypto/tls"

// ALPN is the default application-layer protocol negotiation token for the rmtt
// protocol over TLS and QUIC transports. QUIC requires ALPN (RFC 9001), and the
// Java (Netty) reference implementation requires peers to negotiate the exact
// token "rmtt", so both ends advertise it by default.
const ALPN = "rmtt"

// WithDefaultALPN returns a copy of cfg whose NextProtos reflects the rmtt ALPN
// policy:
//
//   - A nil cfg yields nil.
//   - When cfg.NextProtos is nil (the caller did not configure ALPN), the default
//     [ALPN] is applied so the endpoint interoperates with the Java/Netty
//     reference, which mandates "rmtt".
//   - When cfg.NextProtos is set — including an explicitly empty slice — it is used
//     verbatim. This lets callers override the token or, by passing an empty slice,
//     disable ALPN entirely to reach legacy peers that send none.
//
// The input is never modified and the returned NextProtos never aliases the
// input's backing array: a caller's shared *tls.Config — for example one also
// handed to a WSS listener, which must NOT advertise "rmtt" — stays untouched.
//
// Because RFC 9001 ALPN negotiation is strict, a single QUIC endpoint cannot
// accept both ALPN-less and ALPN-offering peers at once; the choice must be
// consistent across a deployment.
func WithDefaultALPN(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	out := cfg.Clone()
	if cfg.NextProtos == nil {
		out.NextProtos = []string{ALPN}
	} else {
		// Copy so the output never aliases the input's backing array, and so an
		// explicitly empty slice stays empty (non-nil) — the signal for "ALPN
		// disabled" — rather than collapsing to nil ("unset → default").
		out.NextProtos = make([]string, len(cfg.NextProtos))
		copy(out.NextProtos, cfg.NextProtos)
	}
	return out
}
