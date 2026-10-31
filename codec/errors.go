package codec

import "errors"

// Protocol framing errors returned by ReadPacket and the packet factories.
// Compare with errors.Is; error text is not part of the contract.
var (
	// ErrUnsupportedPacketType is returned when a frame carries an unknown
	// control packet type.
	ErrUnsupportedPacketType = errors.New("unsupported packet type")
	// ErrMalformedRemainingLength is returned when the RemainingLength varint
	// continues past four bytes.
	ErrMalformedRemainingLength = errors.New("malformed remaining length")
	// ErrPacketTooLarge is returned when a frame's RemainingLength exceeds
	// MaxPacketSize.
	ErrPacketTooLarge = errors.New("packet exceeds maximum size")
)

// Errors corresponding to the CONNACK return codes in ConnErrors.
var (
	ErrorRefusedBadProtocolVersion = errors.New("unacceptable protocol version")
	ErrorRefusedServerUnavailable  = errors.New("server Unavailable")
	ErrorRefusedNotAuthorised      = errors.New("not Authorized")
	ErrorNetworkError              = errors.New("network Error")
	ErrorProtocolViolation         = errors.New("protocol Violation")
)

// ConnErrors maps CONNACK return codes to errors (Accepted maps to nil).
var ConnErrors = map[byte]error{
	Accepted:                     nil,
	ErrRefusedBadProtocolVersion: ErrorRefusedBadProtocolVersion,
	ErrRefusedServerUnavailable:  ErrorRefusedServerUnavailable,
	ErrRefusedNotAuthorised:      ErrorRefusedNotAuthorised,
	ErrNetworkError:              ErrorNetworkError,
	ErrProtocolViolation:         ErrorProtocolViolation,
}
