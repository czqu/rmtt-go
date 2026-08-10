// Package codec implements the rmtt wire protocol: packet framing with the
// fixed header (message type + remaining length) and encoding/decoding of
// the six control packets CONNECT, CONNACK, PUSH, PINGREQ, PINGRESP and
// DISCONNECT.
//
// Packets are created with NewControlPacket and written to an io.Writer;
// ReadPacket decodes a single packet from an io.Reader. CONNACK return codes
// and DISCONNECT reason codes are defined as constants in this package.
package codec
