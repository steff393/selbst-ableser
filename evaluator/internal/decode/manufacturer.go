package decode

// ManufacturerDecoder extracts a reading from a manufacturer-specific
// (Klasse 2) telegram, given its full raw wM-Bus bytes. Byte positions are
// entirely manufacturer-defined; there is no shared structure to rely on.
type ManufacturerDecoder func(raw []byte) (Reading, bool, error)

var manufacturerDecoders = map[string]ManufacturerDecoder{}

// RegisterManufacturerDecoder adds a decoder for manufacturer-specific
// telegrams identified by the given three-letter manufacturer code (see
// telegram.Manufacturer). This is the extension point non-standard meter
// families are added through; none are registered by default.
func RegisterManufacturerDecoder(manufacturer string, fn ManufacturerDecoder) {
	manufacturerDecoders[manufacturer] = fn
}

// ManufacturerSpecific dispatches to a registered decoder for the given
// manufacturer code. ok is false if no decoder is registered for it.
func ManufacturerSpecific(manufacturer string, raw []byte) (Reading, bool, error) {
	fn, ok := manufacturerDecoders[manufacturer]
	if !ok {
		return Reading{}, false, nil
	}
	return fn(raw)
}

// ManufacturerRecordDecoder reads a reading out of a manufacturer-specific
// data record (VIF 0xFF) carried inside an otherwise standard, self-
// describing telegram — the case ManufacturerDecoder does not cover,
// because the telegram's CI is regular and only one record in it is not.
//
// It returns the reading together with field: the exact bytes holding the
// value, aliasing the payload records were walked from, so a correction
// can overwrite them in place (see EncodeCurrentValue). found is false for
// a record this decoder does not recognize; that is not an error.
type ManufacturerRecordDecoder func(records []Record) (reading Reading, field []byte, found bool, err error)

var manufacturerRecordDecoders = map[string]ManufacturerRecordDecoder{}

// RegisterManufacturerRecordDecoder adds a record decoder for the given
// manufacturer code — see ManufacturerRecordDecoder.
func RegisterManufacturerRecordDecoder(manufacturer string, fn ManufacturerRecordDecoder) {
	manufacturerRecordDecoders[manufacturer] = fn
}

// ManufacturerRecordValue dispatches to a registered record decoder for
// the given manufacturer code. found is false if none is registered or it
// does not recognize any of records.
func ManufacturerRecordValue(manufacturer string, records []Record) (reading Reading, field []byte, found bool, err error) {
	fn, ok := manufacturerRecordDecoders[manufacturer]
	if !ok {
		return Reading{}, nil, false, nil
	}
	return fn(records)
}

// BillingReset is the value a device froze at its last billing-period
// reset ("Stichtagswert"), together with the date it belongs to. A device
// that carries one lets a billing period be closed off at the reset day
// itself rather than at whatever telegram happened to arrive last before
// it — see billing's monthly calculation.
type BillingReset struct {
	Reading Reading
	Date    Date
	HasDate bool // some families carry the value but no usable date
}

// ManufacturerBillingResetDecoder extracts that frozen value from a
// manufacturer-specific telegram. Optional per manufacturer: a family that
// does not transmit one simply has none registered.
type ManufacturerBillingResetDecoder func(raw []byte) (BillingReset, bool, error)

var manufacturerBillingResets = map[string]ManufacturerBillingResetDecoder{}

func RegisterManufacturerBillingReset(manufacturer string, fn ManufacturerBillingResetDecoder) {
	manufacturerBillingResets[manufacturer] = fn
}

// ManufacturerBillingReset dispatches to a registered decoder. ok is false
// when this manufacturer has none, which is not an error: the caller then
// falls back to the behaviour it had before frozen values existed.
func ManufacturerBillingReset(manufacturer string, raw []byte) (BillingReset, bool, error) {
	fn, ok := manufacturerBillingResets[manufacturer]
	if !ok {
		return BillingReset{}, false, nil
	}
	return fn(raw)
}

// ManufacturerPatcher is a manufacturer decoder's write-back counterpart:
// it overwrites the current-value field of a manufacturer-specific
// telegram's raw bytes in place. Registering one is optional — only needed
// for a manufacturer internal/correction must be able to write a corrected
// value back into, not for read-only decoding.
type ManufacturerPatcher func(raw []byte, newValue int64) error

var manufacturerPatchers = map[string]ManufacturerPatcher{}

// RegisterManufacturerPatcher adds a patcher for manufacturer-specific
// telegrams identified by the given manufacturer code — see
// RegisterManufacturerDecoder for the read-direction counterpart.
func RegisterManufacturerPatcher(manufacturer string, fn ManufacturerPatcher) {
	manufacturerPatchers[manufacturer] = fn
}

// PatchManufacturerSpecific dispatches to a registered patcher for the
// given manufacturer code, overwriting raw in place. ok is false if no
// patcher is registered for it (e.g. a manufacturer this system can decode
// but cannot yet write a correction back into).
func PatchManufacturerSpecific(manufacturer string, raw []byte, newValue int64) (ok bool, err error) {
	fn, ok := manufacturerPatchers[manufacturer]
	if !ok {
		return false, nil
	}
	return true, fn(raw, newValue)
}
