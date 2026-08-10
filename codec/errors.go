package codec

import "errors"

var (
	ErrorRefusedBadProtocolVersion = errors.New("unacceptable protocol version")
	ErrorRefusedServerUnavailable  = errors.New("server Unavailable")
	ErrorRefusedNotAuthorised      = errors.New("not Authorized")
	ErrorNetworkError              = errors.New("network Error")
	ErrorProtocolViolation         = errors.New("protocol Violation")
)

var ConnErrors = map[byte]error{
	Accepted:                     nil,
	ErrRefusedBadProtocolVersion: ErrorRefusedBadProtocolVersion,
	ErrRefusedServerUnavailable:  ErrorRefusedServerUnavailable,
	ErrRefusedNotAuthorised:      ErrorRefusedNotAuthorised,
	ErrNetworkError:              ErrorNetworkError,
	ErrProtocolViolation:         ErrorProtocolViolation,
}
