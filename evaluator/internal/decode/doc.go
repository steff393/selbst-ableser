// Package decode extracts typed meter readings from decrypted telegram
// payloads. Standard-compliant payloads are parsed generically by data
// record identifier; manufacturer-specific formats are handled by decoders
// registered per manufacturer code.
package decode
