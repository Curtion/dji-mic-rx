package duml

import (
	"bytes"
	"testing"
)

// The documented example frames, byte for byte. If these stop matching, either
// the frame builders or the CRC are wrong.

func TestHeaderCRC8(t *testing.T) {
	for _, tc := range []struct {
		length byte
		want   byte
	}{
		{19, 0x03},
		{22, 0xfc},
		{14, 0x66},
	} {
		if got := headerCRC8(tc.length); got != tc.want {
			t.Errorf("headerCRC8(%d) = %#02x, want %#02x", tc.length, got, tc.want)
		}
	}
}

func TestBuildV1Command(t *testing.T) {
	got := BuildCommand(V1, 1, TargetRX, 0x031d, 0x01)
	want := []byte{
		0x55, 0x13, 0x04, 0x03, 0x02, 0x5a, 0x01, 0x00, 0x40, 0x5b, 0x01,
		0x00, 0x03, 0x1d, 0x00, 0x01, 0x01, 0x91, 0xfb,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("v1 command = % x,\n              want % x", got, want)
	}
	if !V1.Verify(got) {
		t.Error("v1 command does not verify")
	}
}

func TestBuildV2Command(t *testing.T) {
	got := BuildCommand(V2, 0xb640, TargetAllTX, 0x0037, 0x00)
	want := []byte{
		0x55, 0x16, 0x04, 0xfc, 0x02, 0x5a, 0x40, 0xb6, 0x40, 0x5b, 0x01,
		0x02, 0xff, 0xff, 0x00, 0x00, 0x37, 0x00, 0x01, 0x00, 0xdd, 0xe2,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("v2 command = % x,\n              want % x", got, want)
	}
	if !V2.Verify(got) {
		t.Error("v2 command does not verify")
	}
}

func TestCommandCRCChangesWithValue(t *testing.T) {
	a := BuildCommand(V2, 7, TargetRX, 0x0003, 0x00)
	b := BuildCommand(V2, 7, TargetRX, 0x0003, 0x01)
	if bytes.Equal(a, b) {
		t.Fatal("different values produced identical frames")
	}
	if !V2.Verify(a) || !V2.Verify(b) {
		t.Error("frames do not verify")
	}
	if a[19] != 0x00 || b[19] != 0x01 {
		t.Errorf("payload byte not where the layout says: %#02x %#02x", a[19], b[19])
	}
}

func TestTakeFrame(t *testing.T) {
	cmd := BuildCommand(V1, 7, TargetRX, 0x0303, 0x01)

	// Leading junk, a complete frame, then an incomplete one.
	buf := append([]byte{0xaa, 0xbb}, cmd...)
	buf = append(buf, 0x55)
	frame, rest := TakeFrame(buf)
	if !bytes.Equal(frame, cmd) {
		t.Fatalf("frame = % x, want % x", frame, cmd)
	}
	if !bytes.Equal(rest, []byte{0x55}) {
		t.Fatalf("rest = % x, want 55", rest)
	}
	if f, rest := TakeFrame(rest); f != nil || len(rest) != 1 {
		t.Fatalf("partial frame should stay buffered, got % x % x", f, rest)
	}
}

func TestTakeFrameSkipsMarkerInPayload(t *testing.T) {
	// A serial number containing "U" must not desynchronise framing.
	cmd := BuildCommand(V1, 3, TargetRX, 0x031d, 0x01)
	buf := append([]byte{0x55, 0x30, 0x31, 0x34, 0x51}, cmd...) // "U014Q"
	frame, rest := TakeFrame(buf)
	if !bytes.Equal(frame, cmd) {
		t.Fatalf("frame = % x, want % x", frame, cmd)
	}
	if len(rest) != 0 {
		t.Fatalf("rest = % x, want empty", rest)
	}
}

func TestTakeFrameSplitsStream(t *testing.T) {
	a := BuildCommand(V2, 1, TargetRX, 0x0008, 0x02)
	b := BuildCommand(V2, 2, TargetRX, 0x0021, 0x01)
	stream := append(append([]byte{}, a...), b...)
	frame, rest := TakeFrame(stream)
	if !bytes.Equal(frame, a) {
		t.Fatalf("first frame = % x, want % x", frame, a)
	}
	frame, rest = TakeFrame(rest)
	if !bytes.Equal(frame, b) {
		t.Fatalf("second frame = % x, want % x", frame, b)
	}
	if len(rest) != 0 {
		t.Fatalf("rest = % x", rest)
	}
}

func TestDialectOf(t *testing.T) {
	v1 := []byte{0x55, 0x38, 0x04, 0xe1, 0x5a, 0x02, 0, 0, 0x00, 0x5b, 0x03, 0x00}
	v2 := []byte{0x55, 0x36, 0x04, 0x3d, 0x5a, 0x02, 0, 0, 0x00, 0x5b, 0x03, 0x03}
	if d, ok := DialectOf(v1); !ok || d != V1 {
		t.Errorf("v1 marker: got %v %v", d, ok)
	}
	if d, ok := DialectOf(v2); !ok || d != V2 {
		t.Errorf("v2 marker: got %v %v", d, ok)
	}
	if _, ok := DialectOf(BuildCommand(V1, 1, TargetRX, 0x031d, 1)); ok {
		t.Error("a command frame must not look like a status frame")
	}
}

func TestFrameKindOf(t *testing.T) {
	cmd := BuildCommand(V1, 1, TargetRX, 0x031d, 1)
	if k := FrameKindOf(cmd); k != KindCommand {
		t.Errorf("command kind = %v", k)
	}
	ack := []byte{0x55, 0x0e, 0x04, 0x66, 0x5a, 0x02, 0x01, 0x00, 0x80, 0x5b, 0x01, 0x00, 0xc0, 0xe8}
	if k := FrameKindOf(ack); k != KindAck {
		t.Errorf("ack kind = %v", k)
	}
	if seq, ok := AckSeq(ack); !ok || seq != 1 {
		t.Errorf("ack seq = %d %v", seq, ok)
	}
}
