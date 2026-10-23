package meowcaller

// rejectEnds records a <reject> from one of the callee's devices on an outgoing
// 1:1 call and reports whether it ends the call. Each device answers the offer for
// itself, and some reject without ringing, so one reject is not the callee's answer:
//   - "unsupported" (a device that cannot take calls) never ends it;
//   - nor does any reject once a device has answered;
//   - a reject from the primary device (:0) or from one that preaccepted (rang) does;
//   - otherwise it ends once every device the offer went to has rejected.
//
// The caller holds the engine lock.
func (m *engineCall) rejectEnds(device uint16, reason string, answered bool) bool {
	if reason == "unsupported" || answered {
		return false
	}
	if m.rejected == nil {
		m.rejected = map[uint16]bool{}
	}
	m.rejected[device] = true
	if device == 0 || m.preaccepted[device] {
		return true
	}
	for d := range m.offered {
		if !m.rejected[d] {
			return false
		}
	}
	return true
}
