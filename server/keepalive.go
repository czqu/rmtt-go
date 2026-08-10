package server

// KeepalivePolicy clamps the client's Keepalive proposal into a server-side
// range. A non-positive proposal maps to DefaultSeconds unless AllowDisable
// is set (which returns 0 and disables server-side keepalive enforcement).
type KeepalivePolicy struct {
	MinSeconds     int64
	MaxSeconds     int64
	DefaultSeconds int64
	AllowDisable   bool
}

// DefaultKeepalivePolicy returns the default policy: client proposals are
// clamped into [30, 600] seconds, 60s fallback, keepalive cannot be
// disabled.
func DefaultKeepalivePolicy() *KeepalivePolicy {
	return &KeepalivePolicy{
		MinSeconds:     30,
		MaxSeconds:     600,
		DefaultSeconds: 60,
		AllowDisable:   false,
	}
}

// Decide maps the client's Keepalive proposal (seconds) to the server-side
// keepalive echoed in CONNACK.
func (p *KeepalivePolicy) Decide(clientKp int64) int64 {
	if clientKp <= 0 {
		if p.AllowDisable {
			return 0
		}
		return p.DefaultSeconds
	}
	if clientKp < p.MinSeconds {
		return p.MinSeconds
	}
	if clientKp > p.MaxSeconds {
		return p.MaxSeconds
	}
	return clientKp
}
