package codec

// Protocol framing constants shared by all peers.
const (
	// MagicNumber is the fixed first field of every CONNECT packet.
	MagicNumber uint32 = 0x637a7175
	// ProtocolVersion is the protocol version spoken by this build.
	ProtocolVersion byte = 1
	// MaxPacketSize bounds a frame's RemainingLength. ReadPacket rejects
	// larger claims before allocating, so an unauthenticated peer cannot make
	// the process allocate unbounded memory from a few header bytes.
	MaxPacketSize = 16 << 20 // 16 MiB
)

// Control packet types.
const (
	Connect    = 1
	Connack    = 2
	Push       = 3
	Pingreq    = 5
	Pingresp   = 6
	Disconnect = 14
)

// CONNACK return codes.
const (
	Accepted                     = 0x00
	ErrRefusedBadProtocolVersion = 0x01
	ErrRefusedServerUnavailable  = 0x02
	ErrRefusedNotAuthorised      = 0x03
	ErrNetworkError              = 0xFE
	ErrProtocolViolation         = 0xFF
)

// DISCONNECT reason codes.
const (
	DiscNormalDisconnect   byte = 0x00
	DiscCredentialExpired  byte = 0x01
	DiscSessionTakenOver   byte = 0x02
	DiscServerShutdown     byte = 0x03
	DiscProtocolViolation  byte = 0x04
	DiscKeepaliveTimeout   byte = 0x05
	DiscKickedByAdmin      byte = 0x06
	DiscRateLimited        byte = 0x07
	DiscCredentialRejected byte = 0x08
	DiscUnknownError       byte = 0xFE
)

// ConnackReturnCodes maps CONNACK return codes to human-readable messages.
var ConnackReturnCodes = map[uint8]string{
	0:   "Connection Accepted",
	1:   "Connection Refused: Bad Protocol Version",
	2:   "Connection Refused: Server Unavailable",
	3:   "Connection Refused: Not Authorised",
	254: "Connection Error",
	255: "Connection Refused: Protocol Violation",
}
