package duml

// Model describes one receiver family and where its control interface lives on
// the wire.
//
// The vendor id 0x2ca3 is shared by every DJI device, so it identifies "a DJI
// device", not a model. The product id identifies a receiver family, and the
// interface and endpoints are decided by firmware: interface 6 with bulk
// 0x06/0x86 for the Mobile RX, interface 4 with bulk 0x04/0x84 (alternate
// setting 1) for the Mic Mini 2S RX. Never guess these: look them up here.
type Model struct {
	// Name is the product family this entry covers.
	Name string
	// Vendor and Product are the USB ids to match.
	Vendor, Product uint16
	// Interface is the vendor-specific interface index carrying the protocol.
	Interface uint8
	// BulkOut and BulkIn are the endpoint addresses of that interface.
	BulkOut, BulkIn uint8
	// AltSetting is the alternate setting the interface needs, if not zero.
	AltSetting uint8
	// Verified is true where this table entry has been confirmed on real
	// hardware by the projects this package was written from.
	Verified bool
	// Note is shown in the app's diagnostics.
	Note string
}

// Models is the known receiver table, keyed by USB ids.
var Models = []Model{
	{
		Name:      "DJI Mic Mobile RX (DMMR01 / DMMR02)",
		Vendor:    0x2ca3,
		Product:   0x4011,
		Interface: 6,
		BulkOut:   0x06,
		BulkIn:    0x86,
		Verified:  true,
		Note:      "DJI Mic Mini and Mic Mini 2 phone receivers",
	},
	{
		Name:       "DJI Mic Mini 2S RX (stereo)",
		Vendor:     0x2ca3,
		Product:    0x4015,
		Interface:  4,
		BulkOut:    0x04,
		BulkIn:     0x84,
		AltSetting: 1,
		Note:       "unverified with this app; interface 4 and alternate setting 1 differ from the Mobile RX",
	},
	{
		Name:       "DJI Mic Mini 2S RX (quadraphonic)",
		Vendor:     0x2ca3,
		Product:    0x4115,
		Interface:  4,
		BulkOut:    0x04,
		BulkIn:     0x84,
		AltSetting: 1,
		Note:       "unverified with this app; interface 4 and alternate setting 1 differ from the Mobile RX",
	},
}

// ModelFor returns the table entry for a USB device.
func ModelFor(vendor, product uint16) (Model, bool) {
	for _, m := range Models {
		if m.Vendor == vendor && m.Product == product {
			return m, true
		}
	}
	return Model{}, false
}

// Target addresses a v2 command. The receiver and the transmitters are
// addressed separately; v1 has no such field and always addresses the
// receiver.
type Target uint16

const (
	// TargetRX addresses the receiver itself.
	TargetRX Target = 0x0000
	// TargetAllTX broadcasts to every connected transmitter.
	TargetAllTX Target = 0xffff
)

// TargetUnit addresses one physical transmitter (1 or 2).
func TargetUnit(unit int) Target { return Target(unit) }
