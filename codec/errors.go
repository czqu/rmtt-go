package codec

import "errors"

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
