package duml

// Frame constants shared by both protocol versions.
const (
	// frameStart begins every packet.
	frameStart = 0x55
	// protocolVersion sits at offset 2 of every packet and is always 4.
	protocolVersion = 0x04

	// v1CommandLen is the length of a v1 setting command.
	v1CommandLen = 19
	// v2CommandLen is the length of a v2 setting command.
	v2CommandLen = 22
	// ackLen is the length of the ACK the receiver answers with.
	ackLen = 14

	// maxFrame is the largest frame either firmware emits (the v2 identity
	// push listing the receiver and both transmitters is 178 bytes). Longer
	// readings are discarded, since a stray 0x55 in payload must not be
	// mistaken for a frame.
	maxFrame = 256
)

// Dialect is which firmware protocol a receiver speaks. A firmware update
// changed the command shape, the CRC-16 seed and the status layout; there is
// no way to ask for the version directly, so it is read from the status
// stream's version marker.
type Dialect int

const (
	// V1 is the original DJI Mic Mini firmware protocol.
	V1 Dialect = 1
	// V2 is the DJI Mic Mini 2 firmware protocol.
	V2 Dialect = 2
)

func (d Dialect) String() string {
	switch d {
	case V1:
		return "v1"
	case V2:
		return "v2"
	}
	return "unknown"
}

func (d Dialect) crcInit() uint16 {
	if d == V2 {
		return 0x3692
	}
	return 0x0000
}

func (d Dialect) crcResidue() uint16 {
	if d == V2 {
		return 0x0000
	}
	return 0xbb01
}

// FrameKind is the role a frame plays, read from its ACK-flag byte.
type FrameKind int

const (
	// KindOther is a frame whose flag byte is none of the three below.
	KindOther FrameKind = iota
	// KindCommand is a host to device command.
	KindCommand
	// KindAck is the receiver's acknowledgement of a command.
	KindAck
	// KindPush is an unsolicited status frame from the receiver.
	KindPush
)

func (k FrameKind) String() string {
	switch k {
	case KindCommand:
		return "command"
	case KindAck:
		return "ack"
	case KindPush:
		return "push"
	}
	return "other"
}

// FrameKindOf classifies a complete frame.
func FrameKindOf(frame []byte) FrameKind {
	if len(frame) < 9 {
		return KindOther
	}
	switch frame[8] {
	case 0x40:
		return KindCommand
	case 0x80:
		return KindAck
	case 0x00:
		return KindPush
	}
	return KindOther
}

// DialectOf reports which firmware a frame came from, for frames that carry
// the status marker (offset 8..11). Commands and ACKs do not: only status
// frames are decoded before the dialect is known.
func DialectOf(frame []byte) (Dialect, bool) {
	if len(frame) < 12 || frame[8] != 0x00 || frame[9] != 0x5b || frame[10] != 0x03 {
		return 0, false
	}
	switch frame[11] {
	case 0x00:
		return V1, true
	case 0x03:
		return V2, true
	}
	return 0, false
}

// TakeFrame removes the next complete frame from buf and returns it with the
// rest of the buffer. A frame starts with 0x55 and carries the protocol
// version at offset 2, which is enough to tell a real frame boundary from a
// 0x55 inside payload (such as the "U" in an ASCII serial). Bytes that cannot
// begin a frame are dropped; when no complete frame is available yet, it
// returns nil and leaves the partial frame buffered.
func TakeFrame(buf []byte) (frame, rest []byte) {
	for {
		start := -1
		for i := 0; i < len(buf); i++ {
			if buf[i] != frameStart {
				continue
			}
			if i+2 >= len(buf) {
				// Not enough bytes to check the version byte yet.
				return nil, buf[i:]
			}
			if buf[i+2] == protocolVersion {
				start = i
				break
			}
		}
		if start < 0 {
			// Keep a lone trailing 0x55: its version byte may still arrive.
			if len(buf) > 0 && buf[len(buf)-1] == frameStart {
				return nil, buf[len(buf)-1:]
			}
			return nil, nil
		}
		buf = buf[start:]

		length := int(buf[1])
		if length < 5 || length > maxFrame {
			// A validated start with an implausible length: skip it.
			buf = buf[1:]
			continue
		}
		if len(buf) < length {
			return nil, buf
		}
		return buf[:length:length], buf[length:]
	}
}

// BuildCommand builds the setting command for dialect d that sets command to
// value on target. seq is a per-session sequence number the receiver echoes in
// its ACK; target is only used by v2, where 0x0000 is the receiver, 0xffff
// broadcasts to every transmitter, and 0x0001/0x0002 address one transmitter.
func BuildCommand(d Dialect, seq uint16, target Target, command uint16, value byte) []byte {
	if d == V2 {
		return buildV2Command(seq, uint16(target), command, value)
	}
	return buildV1Command(seq, command, value)
}

// buildV1Command builds a 19-byte v1 command (command id big-endian).
func buildV1Command(seq, command uint16, value byte) []byte {
	body := make([]byte, 0, v1CommandLen)
	body = append(body,
		frameStart,
		v1CommandLen,
		protocolVersion,
		headerCRC8(v1CommandLen),
		0x02,                         // command set: audio subsystem
		0x5a,                         // attribute flags
		byte(seq&0xff), byte(seq>>8), // sequence, little-endian
		0x40,                            // ACK requested
		0x5b,                            // constant
		0x01,                            // constant
		0x00,                            // constant
		byte(command>>8), byte(command), // command id, big-endian
		0x00, // constant
		0x01, // payload length
		value,
	)
	return appendCRC(body, V1.crcInit(), V1.crcResidue())
}

// buildV2Command builds a 22-byte v2 command: target-addressed, and with the
// command id little-endian rather than v1's big-endian.
func buildV2Command(seq, target, command uint16, value byte) []byte {
	body := make([]byte, 0, v2CommandLen)
	body = append(body,
		frameStart,
		v2CommandLen,
		protocolVersion,
		headerCRC8(v2CommandLen),
		0x02,                         // command set: audio subsystem
		0x5a,                         // attribute flags
		byte(seq&0xff), byte(seq>>8), // sequence, little-endian
		0x40,                               // ACK requested
		0x5b,                               // constant
		0x01,                               // constant
		0x02,                               // target-addressed command sub-type
		byte(target&0xff), byte(target>>8), // target unit, little-endian
		0x00, 0x00, // constant
		byte(command&0xff), byte(command>>8), // command id, little-endian
		0x01, // payload length
		value,
	)
	return appendCRC(body, V2.crcInit(), V2.crcResidue())
}

// AckSeq returns the sequence number echoed by an ACK frame.
func AckSeq(frame []byte) (uint16, bool) {
	if len(frame) < 8 || FrameKindOf(frame) != KindAck {
		return 0, false
	}
	return uint16(frame[6]) | uint16(frame[7])<<8, true
}
