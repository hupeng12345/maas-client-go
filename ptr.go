package maas

// Ptr returns a pointer to v.
//
// This SDK exposes many tri-state parameters: unset, explicitly true, and
// explicitly false. Those are not equivalent in the wire protocol (some flags
// are rejected when omitted), so such fields are modelled as pointers:
//
//	cfg.Request.EnableNonstream = maas.Ptr(true)
func Ptr[T any](v T) *T {
	return &v
}
