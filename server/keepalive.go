package server

type KeepalivePolicy struct {
	MinSeconds     int64
	MaxSeconds     int64
	DefaultSeconds int64
	AllowDisable   bool
}

func DefaultKeepalivePolicy() *KeepalivePolicy {
	return &KeepalivePolicy{
		MinSeconds:     30,
		MaxSeconds:     600,
		DefaultSeconds: 60,
		AllowDisable:   false,
	}
}

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
