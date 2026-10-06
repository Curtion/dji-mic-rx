// Package duml speaks the DJI Microphone (Mic Mini family) USB protocol.
//
// Every packet is a DUML frame: 0x55, a total length, a body and two CRC
// bytes. Commands go out on Bulk OUT, and the receiver answers with a
// 14-byte ACK; status arrives unsolicited at about 10 Hz on Bulk IN.
//
// The protocol reference this package was written against is
// ShadowBitBasher/DJI-Mic-Control's PROTOCOL.md, cross-checked against
// usokawa/dji-mic-mo's field table.
package duml

// crc8 computes the CRC-8 (polynomial 0x31, initial value 0xEE, reflected in
// and out) over the frame's first three bytes. Since the start marker and the
// protocol version never vary, the result is a function of the frame length
// alone: 0x03 for a 19-byte v1 command, 0xfc for a 22-byte v2 one, and 0xe1,
// 0x8b and 0xf6 for the three v1 heartbeat sizes. The initial value is
// reversed because the reflected form of the algorithm consumes it reversed.
func crc8(b []byte) byte {
	crc := reflect8(crc8Init)
	for _, v := range b {
		crc ^= v
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8c // reflected 0x31
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// crc8Init is the CRC-8's initial value in the normal (unreflected) domain.
const crc8Init = 0xee

// reflect8 reverses the bits of a byte.
func reflect8(v byte) byte {
	var r byte
	for i := 0; i < 8; i++ {
		if v>>i&1 != 0 {
			r |= 1 << (7 - i)
		}
	}
	return r
}

// headerCRC8 is the CRC-8 byte that belongs at frame offset 3 for a frame of
// the given total length.
func headerCRC8(length byte) byte {
	return crc8([]byte{frameStart, length, protocolVersion})
}

// CRC-16 on the reflected 0x8408 polynomial (the same as CRC-16/IBM-SDLC).
// The two trailing bytes are chosen so that checksumming the whole packet,
// CRC bytes included, always lands on a fixed residue: v1 firmware seeds with
// 0x0000 and lands on 0xbb01, v2 firmware seeds with 0x3692 and lands on
// 0x0000. The polynomial and table are the same for both.
const crc16Poly = 0x8408

var crc16Table = func() [256]uint16 {
	var t [256]uint16
	for i := range t {
		crc := uint16(i)
		for b := 0; b < 8; b++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ crc16Poly
			} else {
				crc >>= 1
			}
		}
		t[i] = crc
	}
	return t
}()

// crc16Inv maps a table value back to its byte, for crcBytes.
var crc16Inv = func() map[uint16]byte {
	m := make(map[uint16]byte, len(crc16Table))
	for i, v := range crc16Table {
		m[v] = byte(i)
	}
	return m
}()

func crc16Step(crc uint16, b byte) uint16 {
	return crc>>8 ^ crc16Table[(crc^uint16(b))&0xff]
}

// checksum folds data into a CRC-16 starting from init.
func checksum(data []byte, init uint16) uint16 {
	for _, b := range data {
		init = crc16Step(init, b)
	}
	return init
}

// crcBytes returns the two bytes that, appended to body, make the whole packet
// checksum to residue from init.
func crcBytes(body []byte, init, residue uint16) [2]byte {
	v := checksum(body, init)
	for b0 := 0; b0 < 256; b0++ {
		v1 := crc16Step(v, byte(b0))
		if idx, ok := crc16Inv[residue^(v1>>8)]; ok {
			return [2]byte{byte(b0), byte(v1 ^ uint16(idx))}
		}
	}
	// Unreachable: the table covers every value the lookup can ask for.
	panic("duml: no CRC suffix found")
}

// appendCRC returns body followed by its two CRC bytes.
func appendCRC(body []byte, init, residue uint16) []byte {
	crc := crcBytes(body, init, residue)
	return append(body, crc[0], crc[1])
}

// Verify reports whether packet (body plus its two CRC bytes) checksums to
// the residue of its dialect. This holds for the frames this package builds;
// frames *from* the receiver are deliberately not checked against it, since
// neither reference implementation does and the documented ACK's trailing
// bytes do not match any such convention.
func (d Dialect) Verify(packet []byte) bool {
	return len(packet) >= 2 && checksum(packet, d.crcInit()) == d.crcResidue()
}
