package codec

import "crypto/tls"

// ALPN is the default application-layer protocol negotiation token for the rmtt
// protocol over TLS and QUIC transports. QUIC requires ALPN (RFC 9001), and the
// Java (Netty) reference implementation requires peers to negotiate the exact
// token "rmtt", so both ends advertise it by default.
const ALPN = "rmtt"

// DisableALPN is a sentinel that may be placed in tls.Config.NextProtos to turn
// ALPN negotiation off entirely on a QUIC endpoint.
//
// Deprecated: QUIC requires ALPN (RFC 9001). An endpoint that disables it opens
// a non-compliant connection that interoperates only with peers that also send
// no ALPN — the very legacy mismatch this library exists to remove. It is
// retained solely to ease migration off such peers and will be removed in a
// future release. Leave NextProtos unset instead so the default ALPN token is
// negotiated.
const DisableALPN = "rmtt-disable-alpn"

// WithDefaultALPN returns a copy of cfg whose NextProtos reflects the rmtt ALPN
// policy:
//
//   - A nil cfg yields nil.
//   - When cfg.NextProtos is empty (nil or an empty slice — the caller did not
//     configure ALPN), the default [ALPN] is applied so the endpoint
//     interoperates with the Java/Netty reference, which mandates "rmtt". Both
//     nil and empty therefore mean "use the default", never "disable".
//   - When cfg.NextProtos is non-empty it is used verbatim, so callers can
//     override the token.
//   - When cfg.NextProtos contains DisableALPN, ALPN is turned off entirely.
//     This is deprecated — see DisableALPN.
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
	for _, p := range cfg.NextProtos {
		if p == DisableALPN {
			// Deprecated escape hatch: drop the sentinel and turn ALPN off.
			out.NextProtos = []string{}
			return out
		}
	}
	if len(cfg.NextProtos) == 0 {
		out.NextProtos = []string{ALPN}
	} else {
		// Copy so the output never aliases the input's backing array.
		out.NextProtos = make([]string, len(cfg.NextProtos))
		copy(out.NextProtos, cfg.NextProtos)
	}
	return out
}
