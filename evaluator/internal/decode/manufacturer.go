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
